# XCut Performance & Resource Policy

## Resource Goals (targets, not achievements)

| Metric | Target |
|---|---|
| Core idle RAM (serve mode) | < 100 MB (stretch < 50 MB) |
| Core idle CPU | ~0% — no background scanning loops |
| Analysis CPU | bounded by `resource.max_analysis_workers × ffmpeg_threads` |
| Temp disk | bounded by `resource.max_temp_gb`, cleaned on job exit |
| Cache disk | bounded by `resource.max_cache_gb`, LRU eviction |
| Log growth | bounded (size-based rotation) |

Numbers below are marked **measured** only when actually measured on this
machine (Windows 11, 32 cores, 32 GB RAM). Otherwise "Not measured".

## Method

- **Analysis ratio** = analysis wall time ÷ video duration (lower is better;
  0.3x means a 10-min video analyzes in 3 min).
- **Render ratio** = render wall time ÷ output timeline duration.
- Benchmarks live in Go tests (`-bench=.`) and `scripts/`; results recorded in
  this file with date + machine context.

## Baseline (measured)

Machine: Windows 11, 32 cores (AMD), 32 GB RAM, NVMe, FFmpeg 8.1.2.

| Date | Stage | Input | Result | Ratio | Notes |
|---|---|---|---|---|---|
| 2026-09-07 | analyze (cold) | 60s 1080p30 testsrc2 | ~2.5–3.0 s wall | **0.05x realtime** | 2 analyzers (frame_diff @2fps/640w + audio RMS 0.5s), 241 samples |
| 2026-09-07 | analyze (cold, soak) | **300s** 1080p30 | ~14.7 s wall incl. process starts | **0.05x realtime** | 5× input length: ratio unchanged, no degradation |
| 2026-09-08 | analyze (cold, long) | **1800s** 720p30 (30 min) | 62.7 s wall | **0.035x realtime** | ffmpeg child peak RSS ~33 MB, xcut process negligible; 3 tracks incl. onsets (steady tone → 0 false onsets) |
| 2026-09-07 | analyze (cache hit) | same | <0.1 s | ~0x | fingerprint-keyed cache |
| 2026-09-07 | render | 10s timeline from 1080p source | ~1.5 s | **0.15x** output duration | normalize×1 + concat copy, 2 threads/ffmpeg |
| 2026-09-07 | audio RMS: rust worker | 60s mp3 | 0.127 s | — | release build, symphonia decode + DSP |
| 2026-09-07 | audio RMS: ffmpeg astats | 60s mp3 | 0.143 s | — | same filter chain as AudioAnalyzer |
| 2026-09-08 | audio RMS: ffmpeg astats (this machine) | 60s aac | **0.176 s** | ~0.003x | `BenchmarkAudioRMS60s`, i7-10875H, ffmpeg 9.0.1 |
| 2026-09-08 | audio onset: PCM pipe + Go DSP (this machine) | 60s aac | **0.121 s** | ~0.002x | `BenchmarkAudioOnset60s`; faster than astats — streaming s16le beats per-window metadata printing |
| 2026-09-07 | **serve idle RAM** | — | **12.3 MB working set / 14.6 MB private** | — | goal <100 MB, stretch <50 MB: **met** |
| 2026-09-07 | **serve idle CPU** | 30 s idle | **0.031 s total, unchanged** (~0%) | — | no background scanning loops: **met** |
| 2026-09-09 | **serve idle RAM (re-check)** | — | **11.9 MB WS / 46.6 MB private** | — | after session #3 (asset file endpoint, proxies): **met** |
| 2026-09-09 | **serve idle CPU (re-check)** | 10 s idle | **0.000 s** (~0%) | — | **met** |
| 2026-09-09 | render (concat path) | 10s 1-clip 720p30 timeline | 1.7 s wall | **0.17x** output duration | normalize ×1 + concat copy, 2 threads |
| 2026-09-09 | render (xfade combine path) | 18s 2-clip 720p30 timeline, one 2s xfade | 3.5 s wall | **0.19x** output duration | normalize ×2 + chained xfade/acrossfade re-encode; output probed exactly 18.000s |
| 2026-09-09 | analyze, proxy OFF (cold) | 300s 1080p30 testsrc2 | 42.8 s wall | **0.14x realtime** | default 2-thread cap; frame_diff dominates (1080p decode × 9000 frames) |
| 2026-09-09 | analyze, proxy ON (cold, incl. proxy encode) | same | 43.4 s wall | **0.14x realtime** | one-time proxy encode ≈ 35 s under the 2-thread cap (1080p decode-bound); analyzer passes collapse to ~2 s |
| 2026-09-09 | analyze, proxy ON (proxy warm, analysis cold) | same | **40.0 → breakdown: encode 35 s, analyzers 1.9 s** | **~0.01x for the analysis passes** | frame_diff 0.76 s + RMS 0.74 s + onset 0.35 s (debug-log timing); 10–20× less per repeated analysis |
| 2026-09-10 | **session #4 re-check: analyze (cold)** | 300s 1080p30 testsrc2 | 35.9 s wall | **0.12x realtime** | after M20 (limiter on every exec) + M28 (capped output capture): no regression, within run variance of the 42.8 s row |
| 2026-09-10 | session #4 re-check: analyze (cache hit) | same | 0.25 s wall (CLI incl. process start) | ~0x | — |
| 2026-09-10 | session #4 re-check: render (concat path) | 10s 1-clip 720p30 | 3.3 s wall | **0.33x** output duration | identical to the pre-M20 baseline binary (3.1–3.5 s both, A/B against bf45b52): the 1.7 s row above was measured under different conditions, not comparable; no regression from tonight's changes |
| 2026-09-10 | session #4 re-check: serve idle RAM | — | 12.9 MB WS / 49.2 MB private | — | **met** (<100 MB, stretch <50 MB for WS) |
| 2026-09-10 | session #4 re-check: serve idle CPU | 10 s idle | Δ0.000 s (~0%) | — | **met** |

Analysis proxies (session #3): the win is on **repeated** analysis (style
changes, re-runs, multi-project sharing) — analyzer passes drop from
~40 s to ~2 s on a 5-min 1080p source because they decode a 640-wide
2 fps proxy instead of the full source. The one-time proxy encode is
itself decode-bound under the default `ffmpeg_threads` cap, so a single
cold analyze is break-even. A higher thread budget for the one-time
encode is a possible future knob (resource policy decision, not a defect).
Raw ffmpeg component timings at default threads (32-core machine):
decode original 8.1 s, decode proxy 0.5 s, frame_diff chain 13.3 s
(original) vs 1.0 s (proxy).

Rust vs FFmpeg audio baseline: parity on this workload (decode/IO bound).
The Go onset DSP (2026-09-08) settles the "which runtime for transients"
question the same way: the decode pass dominates, in-process Go math on a
16 kHz mono envelope is microseconds-scale — no Rust rewrite is justified
(D2). Memory stays flat by construction (`media.StreamStdout` bounded
intake, fixed-size buffers).

Definition: ratio = processing wall time ÷ media duration (analyze) or ÷
timeline duration (render). Wall time includes process startup.

## Known Hotspots / Policy

- Video analysis never decodes every frame at full resolution: sampling at
  `frame_sample_fps` (default 2) with a downscaled proxy (`analysis_width`,
  default 640) is the first-order cost control. Coarse-to-fine: cheap full-video
  pass → refined analysis only on candidate segments.
- No whole-video-in-memory anywhere. Streaming/pipe only; frames are consumed
  and discarded.
- Optimization requires evidence: benchmark → profile (pprof / cargo flamegraph)
  → hotspot → optimize → benchmark again. No rewrites on vibes; Rust ports must
  beat the Go baseline measurably.
