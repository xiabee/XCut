//! JSON protocol types (versioned; see ARCHITECTURE.md worker protocol).

use serde::{Deserialize, Serialize};

pub const PROTOCOL: u32 = 1;

/// Sent once on stdin as a single JSON line/object.
#[derive(Debug, Deserialize)]
pub struct Request {
    pub protocol: u32,
    pub op: String,
    #[serde(default)]
    pub input: String,
    #[serde(default)]
    pub params: serde_json::Value,
}

/// Written once to stdout.
#[derive(Debug, Serialize)]
pub struct Response {
    pub protocol: u32,
    pub ok: bool,
    pub op: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub result: Option<serde_json::Value>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error: Option<ErrorBody>,
}

#[derive(Debug, Serialize)]
pub struct ErrorBody {
    pub code: String,
    pub message: String,
}

impl Response {
    pub fn success(op: &str, result: serde_json::Value) -> Self {
        Response { protocol: PROTOCOL, ok: true, op: op.to_string(), result: Some(result), error: None }
    }

    pub fn failure(op: &str, err: &crate::WorkerError) -> Self {
        Response {
            protocol: PROTOCOL,
            ok: false,
            op: op.to_string(),
            result: None,
            error: Some(ErrorBody { code: err.code().to_string(), message: err.to_string() }),
        }
    }
}

/// Result payload of the `describe` op.
#[derive(Debug, Serialize)]
pub struct Describe {
    pub name: &'static str,
    pub version: &'static str,
    pub protocol: u32,
    pub ops: &'static [&'static str],
}

#[derive(Debug, Deserialize, Serialize)]
pub struct AudioRmsParams {
    /// RMS window length in seconds (default 0.5).
    #[serde(default = "default_window")]
    pub window_sec: f64,
}

fn default_window() -> f64 {
    0.5
}

impl Default for AudioRmsParams {
    fn default() -> Self {
        AudioRmsParams { window_sec: default_window() }
    }
}

/// Result payload of the `audio_rms` op (matches the Go analysis FeatureTrack
/// sample shape: {t, v} pairs; v in dBFS with digital silence pinned to -120).
#[derive(Debug, Serialize)]
pub struct AudioRmsResult {
    pub kind: &'static str,
    pub unit: &'static str,
    pub sample_rate: u32,
    pub window_sec: f64,
    pub samples: Vec<Sample>,
}

#[derive(Debug, Serialize)]
pub struct Sample {
    pub t: f64,
    pub v: f64,
}
