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
| 2026-09-16 | **serve idle RAM (re-check)** | 30 s idle | **15.7 MB WS / 47.3 MB private, CPU 0.000 s / 10 s** | session #10 HEAD (81cee42) | upload heartbeat (M112) adds no steady-state cost: **met** |
| 2026-09-10 | **session #4 re-check: analyze (cold)** | 300s 1080p30 testsrc2 | 35.9 s wall | **0.12x realtime** | after M20 (limiter on every exec) + M28 (capped output capture): no regression, within run variance of the 42.8 s row |
| 2026-09-10 | session #4 re-check: analyze (cache hit) | same | 0.25 s wall (CLI incl. process start) | ~0x | — |
| 2026-09-13 | **session #7 re-check: analyze (cold)** | 300s 1080p30 testsrc2 + sine | 39.5 s wall | **0.13x realtime** | first run through the streaming metadata transport (M76): within run variance of the 35.9 s row — no regression |
| 2026-09-13 | **session #7 re-check: serve idle RAM** | — | **14.0 MB working set** (14667776 B after 60 s idle) | — | goal <100 MB: **met** (session #5: 14.2 MB — flat) |
| 2026-09-13 | **session #7 re-check: serve idle CPU** | 60 s idle | **0.047 s total since start** (~0%) | — | no background scanning loops: **met** |
| 2026-09-10 | session #4 re-check: render (concat path) | 10s 1-clip 720p30 | 3.3 s wall | **0.33x** output duration | identical to the pre-M20 baseline binary (3.1–3.5 s both, A/B against bf45b52): the 1.7 s row above was measured under different conditions, not comparable; no regression from tonight's changes |
| 2026-09-10 | session #4 re-check: serve idle RAM | — | 12.9 MB WS / 49.2 MB private | — | **met** (<100 MB, stretch <50 MB for WS) |
| 2026-09-12 | session #6: analyze on REAL footage (cold) | real 604s 1280x720@30 badminton broadcast (720p h264+aac) | 35.9 s wall | **0.06x realtime** | frame_diff + RMS + onsets, 1493 onsets; one-time, fingerprint-cached; the first real-footage run (all prior rows are synthetic) |
| 2026-09-12 | session #6: render rally highlight on REAL footage | 8-clip 60s timeline from the same match (mixed trims+concat) | 9.3 s wall | **0.15x** output duration | badminton_highlight v2, cut transitions; output probed 60.02s / 15.8 MB |
| 2026-09-10 | session #4 re-check: serve idle CPU | 10 s idle | Δ0.000 s (~0%) | — | **met** |
| 2026-09-10 | session #4 audio bench A/B (same host, same command) | 60s aac | RMS 0.40 s, onset 0.56 s | ~0.007x | baseline binary (bf45b52) measures the same (0.56/0.46 s): the faster 2026-09-08 rows were a different machine/context (i7-10875H note), not a code regression — tonight's limiter/cap changes are free on these paths |
| 2026-09-15 | **session #9 re-check: analyze (cold)** | 300s 1080p30 testsrc2 + sine | 26.9 s wall | **0.09x realtime** | after session #8 (per-asset analyzer assembly, job-object attach, streaming metadata from M76): faster than the 39.5 s row — fixture encoded ultrafast decodes easier than the previous preset; within-method comparison, no regression |
| 2026-09-15 | session #9 re-check: analyze (cache hit) | same | 1.03 s wall incl. CLI process start | ~0x | — |
| 2026-09-15 | session #9 re-check: render (concat path) | 10s 1-clip 1080p30 timeline | 2.5 s wall (probe: 10.02 s output) | **0.25x** output duration | vs 3.3 s / 0.33x session #4 row — no regression |
| 2026-09-14 | session #8 re-check: serve idle RAM | — | 17.2 MB WS / 10 s CPU delta 0 | — | goals met (<100 MB, ~0%); +3 MB vs session #7 rows (health ffmpeg lookup + new endpoints) |
| 2026-09-18 | session #12 re-check: analyze (cold) | 300s 1080p30 testsrc2 + sine | 17.5 s wall | **0.058x realtime** | session #12 HEAD (92f0f5c), CLI wall incl. process start; faster than the 26.9–39.5 s band (idle-night machine), no regression — tonight's changes are CLI/eval/scripts-side |
| 2026-09-18 | session #12 re-check: analyze (cache hit) | same | 0.31 s wall | ~0x | fingerprint-keyed cache across projects in the workspace |
| 2026-09-18 | session #12 re-check: render (concat path) | 10s 1-clip 1080p30 timeline | 2.2 s wall | **0.22x** output duration | vs 2.5 s / 0.25x session #9 row — no regression |
| 2026-09-20 | **session #14: analyze on REAL footage** | 603 s 720p30 broadcast in a shared 6-court hall, 3 analyzers + court ROI | 28.1 s wall | **0.047x realtime** | through the API on a running serve; xcut peak WS **23.8 MB**, ffmpeg child peak **55.7 MB** — streaming analyzers keep the core flat in memory under real load |
| 2026-09-20 | **session #14: API payload after dropping the probe blob** | `GET /api/v1/projects/{id}` with 1 real asset | **5936 → 558 bytes (−90.6%)** | — | `storage.Asset.ProbeJSON` is stored for diagnostics but was serialized into every response; the UI polls this endpoint, so a 20-asset project went from ~100 KB to ~11 KB per poll. Measured on the live server, not estimated |

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
| 2026-09-11 | **session #5 re-check: serve idle RAM** | — | 14.2 MB WS / ~49 MB private | — | after M32-M48 (cancel registry, revision mutex, startup sweeps): **met** |
| 2026-09-11 | **session #5 re-check: serve idle CPU** | 8 s idle | 0.078 s total since start, ~0% while idle | — | **met** |
| 2026-09-12 | session #6: serve idle re-check (after subtitles endpoints + new routes) | — | 13.0 MB WS / cpu delta 0.000s over 5s | — | **goals met** (<100 MB RAM, ~0% CPU) |
| 2026-09-17 | session #11: serve idle re-check (after M120 write-idle wrapper, M-B upload mutex, M-A atomic gate) | — | 16.6 MB WS / 48.1 MB private / cpu delta 0 ms over 10 s | — | **goals met** (<100 MB RAM, ~0% CPU); streaming/mutex additions carry no standing cost |
| 2026-09-18 | session #12: serve idle re-check (after eval tooling, sweep descent, retrying proxy rename) | — | 17.5 MB WS / 48.1 MB private / cpu delta 0 ms over 10 s | — | **goals met** (<100 MB RAM, ~0% CPU); flat vs session #11 |
| 2026-09-20 | **session #14: serve idle re-check (after the API auth gate, remote sessions, UI sign-in)** | 45 s idle | 16.4 MB WS flat across the window / cpu delta **0.00 s** (0.0%) / 10 threads / 188 handles | — | **goals met**; the bearer gate and the lazily-created session store add no standing cost — a loopback serve never allocates the map it would need |
| 2026-09-21 | **session #16: serve idle re-check, REMOTE BIND with a live session and failure-tracker entries** | 60 s idle after one sign-in (201) + 3 wrong-token rejections | **17.8 MB WS at T0 and T60 / cpu 0.12 s total, delta 0.01 s** | — | the state surfaces sessions #14–16 added (session store holding one entry, failure tracker holding one peer window) are flat in memory and CPU-idle: **goals met** in the remote posture, which the loopback-only re-checks above never exercised |
| 2026-09-21 | **session #16: serve idle re-check, loopback posture (desktop default)** | 60 s idle | **17.3 MB WS flat / cpu delta 0.00 s** | — | **goals met**; within run variance of every prior row |
