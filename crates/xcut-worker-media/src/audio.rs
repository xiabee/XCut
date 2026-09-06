//! Audio RMS analysis over symphonia decoding.
//!
//! Pure-Rust decode avoids the FFmpeg filter machinery for this hot path
//! (DECISIONS D2) and keeps the worker dependency-light and sandboxable.

use crate::error::WorkerError;
use crate::protocol::{AudioRmsParams, AudioRmsResult, Sample};
use std::fs::File;
use symphonia::core::audio::SampleBuffer;
use symphonia::core::codecs::{DecoderOptions, CODEC_TYPE_NULL};
use symphonia::core::errors::Error as SymphoniaError;
use symphonia::core::formats::FormatOptions;
use symphonia::core::io::MediaSourceStream;
use symphonia::core::meta::MetadataOptions;
use symphonia::core::probe::Hint;

/// Digital-silence floor (dBFS). Matches the Go event builder's silentDB.
pub const SILENT_DB: f64 = -120.0;

/// Decode `path`'s audio track and compute per-window RMS in dBFS.
///
/// Channels are averaged to mono; windows are `window_sec` long in stream
/// time; the final partial window still emits (trimmed to real content).
/// Deterministic: identical input ⇒ identical samples.
#[allow(unused_assignments)] // final flush_window!() resets don't feed reads
pub fn audio_rms(path: &str, params: &AudioRmsParams) -> Result<AudioRmsResult, WorkerError> {
    if !(params.window_sec > 0.0 && params.window_sec.is_finite()) {
        return Err(WorkerError::Params("window_sec must be > 0".into()));
    }

    let file = File::open(path)?;
    let mss = MediaSourceStream::new(Box::new(file), Default::default());
    let mut hint = Hint::new();
    // Extension hint helps the probe pick the demuxer for extensionless stdin
    // cases later; harmless for files.
    if let Some(ext) = std::path::Path::new(path).extension().and_then(|e| e.to_str()) {
        hint.with_extension(ext);
    }

    let probed = symphonia::default::get_probe()
        .format(&hint, mss, &FormatOptions::default(), &MetadataOptions::default())
        .map_err(|e| WorkerError::Decode(format!("probe failed: {e}")))?;

    let mut format = probed.format;
    let track = format
        .tracks()
        .iter()
        .find(|t| t.codec_params.codec != CODEC_TYPE_NULL)
        .ok_or(WorkerError::NoAudio)?;

    let sample_rate = track.codec_params.sample_rate.ok_or(WorkerError::Decode(
        "audio track has no sample rate".into(),
    ))?;
    let channels: usize = track
        .codec_params
        .channels
        .map(|c| c.count())
        .unwrap_or(1)
        .max(1);

    let mut decoder = symphonia::default::get_codecs()
        .make(&track.codec_params, &DecoderOptions::default())
        .map_err(|e| WorkerError::Decode(format!("decoder init failed: {e}")))?;

    let window_samples = (params.window_sec * sample_rate as f64).round() as usize;
    if window_samples == 0 {
        return Err(WorkerError::Params("window_sec too small for sample rate".into()));
    }

    let mut result = AudioRmsResult {
        kind: "audio_rms_db",
        unit: "dBFS",
        sample_rate,
        window_sec: params.window_sec,
        samples: Vec::new(),
    };

    // Accumulators for the current window.
    let mut sum_sq = 0f64;
    let mut count = 0u64;
    let mut window_start = 0f64;
    let mut total_frames = 0u64;

    macro_rules! flush_window {
        () => {
            if count > 0 {
                let mean_sq = sum_sq / count as f64;
                let db = if mean_sq <= f64::MIN_POSITIVE {
                    SILENT_DB
                } else {
                    let v = 10.0 * mean_sq.log10();
                    if v < SILENT_DB { SILENT_DB } else { v }
                };
                result.samples.push(Sample {
                    t: window_start,
                    v: (db * 10000.0).round() / 10000.0,
                });
            }
            sum_sq = 0.0;
            count = 0;
        };
    }

    loop {
        let packet = match format.next_packet() {
            Ok(p) => p,
            Err(SymphoniaError::IoError(ref e))
                if e.kind() == std::io::ErrorKind::UnexpectedEof =>
            {
                break
            }
            Err(SymphoniaError::ResetRequired) => {
                return Err(WorkerError::Decode("stream reset required".into()))
            }
            Err(SymphoniaError::IoError(ref e))
                if e.kind() == std::io::ErrorKind::InvalidData =>
            {
                // Tolerate a malformed final packet: stop decoding here.
                break
            }
            Err(e) => return Err(WorkerError::Decode(format!("packet read failed: {e}"))),
        };

        match decoder.decode(&packet) {
            Ok(decoded) => {
                let spec = *decoded.spec();
                let mut sbuf = SampleBuffer::<f32>::new(decoded.capacity() as u64, spec);
                sbuf.copy_interleaved_ref(decoded);

                for chunk in sbuf.samples().chunks(channels) {
                    // Average channels → mono sample in [-1, 1].
                    let mut s = 0f64;
                    for x in chunk {
                        s += *x as f64;
                    }
                    let mono = s / chunk.len() as f64;
                    sum_sq += mono * mono;
                    count += 1;
                    total_frames += 1;

                    if count == window_samples as u64 {
                        flush_window!();
                        window_start = total_frames as f64 / sample_rate as f64;
                    }
                }
            }
            Err(SymphoniaError::DecodeError(_)) => continue, // skip bad packet
            Err(e) => return Err(WorkerError::Decode(format!("decode failed: {e}"))),
        }
    }
    // Final partial window.
    flush_window!();

    if result.samples.is_empty() {
        return Err(WorkerError::NoAudio);
    }
    Ok(result)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::protocol::AudioRmsParams;

    fn silent_params() -> AudioRmsParams {
        AudioRmsParams { window_sec: 0.5 }
    }

    #[test]
    fn rejects_bad_window() {
        assert!(audio_rms("x", &AudioRmsParams { window_sec: 0.0 }).is_err());
        assert!(audio_rms("x", &AudioRmsParams { window_sec: f64::NAN }).is_err());
    }

    #[test]
    fn missing_file_is_io_error() {
        let err = audio_rms("definitely-missing.mp3", &silent_params()).unwrap_err();
        assert_eq!(err.code(), "io");
    }

    #[test]
    fn silence_floor_constant_matches_go() {
        assert_eq!(SILENT_DB, -120.0);
    }

    // RMS math itself is covered through the Go-side integration test against
    // a real fixture (see internal/analysis rust worker test), which also
    // validates the whole process boundary.
}
