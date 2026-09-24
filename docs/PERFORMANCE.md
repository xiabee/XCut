# XCut Performance & Resource Policy

## Resource Goals (targets, not achievements)

| Metric | Target |
|---|---|
| Core idle RAM (serve mode) | < 100 MB (stretch < 50 MB) |
| Core idle CPU | ~0% — no background scanning loops |
| Analysis CPU | bounded by `resource.max_ffmpeg_processes` × `resource.ffmpeg_threads`; `max_analysis_workers` bounds how many *assets* are in flight, and cannot get past the process cap (measured 2026-09-23 below) |
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
| 2026-09-24 | **serve idle RAM (gate)** | 5 s window | **18 MB RSS** | — | goal met; now measured by `scripts/idle-check.sh` on every gate, not by hand |
| 2026-09-24 | **serve idle CPU (gate)** | 5 s window, no traffic | **0 ms of 5000 ms (0%)** | — | ceiling 2%; a 50 ms workspace-scanning loop injected into a copy reads 6% and fails the gate |
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
| 2026-09-21 | **render, default reel** | same 603 s match → 8 clips / 60.02 s out | **15.0 s wall** | **0.25x** output duration | peak working set: xcut **20.2 MB**, ffmpeg child **323.1 MB** (100 ms sampling, 105 samples); output 14.2 MB. No court ROI in this run, so its analyze cost is not comparable with the session #14 row below |
| 2026-09-21 | **render, 4x longer reel** (`--duration 240`) | 20 clips / 144.02 s out | **45.8 s wall** | **0.32x** output duration | peak working set: xcut **21.9 MB** (+8% for 2.4x the duration), ffmpeg child **323.7 MB** (flat) — the encoder's canvas, not the reel length, sets the child peak. Output 34.5 MB. This is the cost curve behind the length knob: time grows roughly linearly, the Go core stays flat |
| 2026-09-21 | **scoreboard scan (sidecar), once per asset** | the same 603 s / 720p match, crop over the score digits | **8.5 s wall** | 0.014x source duration | the whole cost of the boundary feature: one ffmpeg `crop+fps+scene` pass at 4 fps inside the sidecar, run by `analyze` only when a region is set, result stored on the asset row (re-analyzes cost nothing until the region moves) |
| 2026-09-21 | **render, marked reel** (`--duration 120`) | 44 boundaries → 14 clips / 105.7 s out | **17.8 s wall** | **0.17x** output duration | same match, full product path (`import → boundaries → analyze → timeline → render`); output 25.6 MB. In line with the 0.25x / 0.32x rows above — ending clips at point boundaries changes which seconds are encoded, not the per-second cost |
| 2026-09-21 | **session #16: analyze, 4K (first 4K row)** | 300 s 3840×2160 30fps testsrc2 + tone, 3 analyzers, no proxy | **163 s wall** | **0.54x realtime** | xcut peak **16.9 MB** (200 ms sampling), ffmpeg child peak **131.9 MB** — 4× the 1080p pixels at ~4× the ratio, decode-bound as designed; the Go core stays flat at 4K. A same-input earlier run measured 62.7 s; that run and this one shared the box with concurrent CI work, so the sampled (higher) figure is recorded as the honest bound |
| 2026-09-21 | **the whole one-shot on REAL footage** — `xcut auto … --style badminton_highlight --duration 240 --score-crop …` | the same 603 s 1280×720@30 broadcast: import → analyze (including the 44-mark scoreboard scan) → timeline → render | **77.6 s wall** end to end, of which the render line reported **36.4 s** | **0.13x** of the source duration | output 158.6 s / 39.0 MB from 26 candidate rallies (23 taken). No court ROI was set on this run, which is why 26 candidates rather than the eval harness's 30 for the same match. This is what a user's first command costs; the phase rows above are its parts |
| 2026-09-22 | **render, a framing plan on every clip** (运镜, session #20) | the default reel of the same match — 8 clips / 60.02 s out — rendered twice from the same timeline file, once with `motion` (zoom 0.8, center drifting across 30% of the frame) on all 8 clips | **8.6 s wall** against **8.1 s** without a plan | **0.143x** against 0.135x output duration | the CLI's own render line, same project and machine, one JSON field between the two runs. Output **16.57 MB against 14.86 MB (+11.5%)**: at a fixed CRF a magnified frame carries more detail, so the size cost is about twice the time cost (+0.5 s per minute of output). The 8.1 s baseline is itself faster than the 15.0 s row above for the same reel — that run shared the box — so only the pair inside this row is comparable |
| 2026-09-22 | **render, a music bed mixed under the same reel** (卡点音乐, session #20) | the default 8-clip / 60.02 s reel rendered twice from one project: once with no bed, once with a 60 s AAC track named by the document (looped, mixed at 0.9 / 0.35) | **11.3 s wall** against **10.2 s** | **0.19x** against 0.17x output duration | the CLI's own render line, same machine and project. Output **15.36 MB against 14.86 MB (+3.3%)**, duration identical to the millisecond (60.021334 s both) — the mix costs about a second a minute and a little size, and the video stream is copied, so there is no second encode. The bed used here is the match's own audio extracted, which is why nothing snapped: crowd noise still carries no grid (docs/EVAL.md) |
| 2026-09-23 | **B7a: serve idle, re-checked after the Phase 5 arc** (captions, pacing, the export job type) | HEAD `08f09a9`, fresh workspace, `GET /health` once, then 60 s with nothing asked of it | **18.1 MB and 17.7 MB working set on two runs** (T0 = T60 within 0.1 MB), **CPU delta 0.000 s both** | — | goals met (<100 MB, ~0%); within run variance of the 17.3 MB session-#16 row, so the arc's additions cost nothing standing. No media child was alive at any sample |
| 2026-09-23 | **B7a: which knob owns the analysis fan-out** | four 150 s clips of the same match (600 s of 720p30) in one project, `xcut analyze`, sampled at 0.25 s | at the defaults (workers 2): **18.2 s, core 21.9 MB, 2 ffmpeg children, children together 107.0 MB**. workers 4: **18.1 s, 107.5 MB, still 2 children**. workers 4 + jobs 4: **18.4 s, 107.3 MB, still 2**. `max_ffmpeg_processes` 4: **12.1 s, 215.1 MB, 4 children** | 0.030x realtime at the cap, **0.020x** at 4 | the knob that bounds analysis is `max_ffmpeg_processes` — ~54 MB per 720p child, linear in the cap, and the wall time follows it. `max_analysis_workers` only decides how many *assets* the pipeline keeps in flight, which the process cap then throttles; raising it alone changed nothing measurable. Each case's effective config was read back from `xcut config show` first, because a knob that never reached the process reads exactly like a knob that does not bind |
| 2026-09-23 | **B7a: what the one tap costs** (B6d) | the same 603 s match in its own fresh workspace (its ~33 s analyze excluded), a 60 s `badminton_highlight` reel delivered two ways: `POST …/export` (one job, subtitles skipped for want of a sidecar) against `xcut timeline` + `xcut render` | tap: **9.2 s** end to end, peak **343.2 MB** across core and its one media child, 14.9 MB out. Two commands: **1.1 s + 9.0 s = 10.1 s**, render peak **339.3 MB**, same 14.9 MB out | the render step alone is 9.0 / 60.02 = **0.15x** output duration, the same path the 2026-09-22 rows measured | the orchestration is free — what the tap saves is a process start, and its memory is the encoder's, not its own. The plan it returned read `timeline=create \| subtitles=skip (no AI sidecar configured…) \| render=create`, which is the same 9.2 s made explainable. The tap's 9.2 s is a total across both stages, so it is not itself a render ratio |
| 2026-09-23 | **B7a: the xfade render's child, and what can bound it** | 45 s reel through `generic_xfade` (1.0 s transitions), rendered twice with `max_ffmpeg_processes` at 2 and at 4 | **2.7–3.1 s**, peak **583 MB** total of which the single ffmpeg child is **566 MB**, **one child both times**, 5.2 MB out | 0.06x output duration | the combining pass is one process running a filter graph, so the process cap cannot reach it: the only ceiling for a 566 MB encoder is `resource.ffmpeg_max_memory_mb`, which ships at 0 = uncapped (an opt-in since session #16) and which `xcut doctor` reports as `OPTIONAL … memory uncapped`. 566 MB is 1.75x the 323 MB the concat path reaches, which is the number any default cap has to be set against — an owner decision, not a change to make quietly |


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
| 2026-09-22 | **analysis, the beat-grid fit** (`EstimateBeatGrid`, B1b, session #20) | perfect 0.5 s click lattices of 120 / 360 / 1 440 / 4 800 / 20 000 onsets, timed inside the package | **1 / 10 / 129 / 131 / 127 ms** | — | flat past 1 440 because the fit reads at most `maxBeatFitOnsets` = 1 200 onsets and projects the grid over the rest (measured with the budget switched off: **158 / 1 999 / 44 700 ms** at 1 440 / 4 800 / 20 000). The intermediate version of this code — a 2% candidate ladder, each rung judged by folding — was measured at 54 ms / 1.8 s / 17 s / **2 m 7 s** and did not finish 3 600 onsets inside 600 s, and the quadratic cluster search alone cost 57 s for the 271-tempo sweep versus **0.63 s** now. Rule 4 of `AGENTS.md` is why the cap is a constant and not a hope |
| 2026-09-23 | **analysis cache, in bytes** (B7b, shipped posture: `proxy_enabled: false`) | 603.43 s 1280×720 broadcast + 300 s 1920×1080 synthetic, 3 analyzers — i7-10875H, 16 threads, FFmpeg 9.0.2, xcut `0aed2e6` | one entry **115,752 B** (the match, 1,493 onsets) and **36,387 B** (the tone, 0 onsets); 2 entries = **152,139 B** | **0.0014%** of the 10 GiB `max_cache_gb` | cold analyze 26.6 s and 13.7 s; the same analyses served from cache in **184 ms** and **169 ms** (CLI wall incl. process start) with the bytes unchanged. An entry tracks its *samples*, not the source's pixels: the 1080p file's entry is a third of the 720p one because a pure sine contributes no onsets |
| 2026-09-23 | **proxy bytes, per source-minute** (`resource.proxy_enabled: true`, 640 px @ 2 fps) | the same two assets, the same box | **26,623,394 B** for 603.43 s of match (**2.5 MiB per minute**) and **7,933,742 B** for 300 s of 1080p (**1.5 MiB/min**) | 35% and 1.4% of the two sources' own bytes | the share of source depends on how the source was encoded, the share of *minute* does not, so the budget is quoted per minute: at 2.5 MiB/min the default 2 GiB holds **≈13 h** of footage like this match at one geometry. The one-time encode is ~3.1 s of the match's 29.7 s cold run and ~2.0 s of the synthetic's 15.7 s. A hit still pays the proxy decision — **283–381 ms** warm against 169–184 ms with the knob off, because `Ensure` probes the source before the cache is consulted |
| 2026-09-23 | **the geometry axis**: a second `analysis_width` is a second full copy | the match analyzed at 640, then at 480 | **+19,167,798 B** of proxy (72% of its 640 copy) and **+116,267 B** of cache entry, analyze wall **28.8 s** | 3 files, **53,724,934 B** | the geometry is in the filename (`…w640.f2.mp4`, `…w480.f2.mp4`) deliberately — a proxy built for another canvas would answer a new question with old pixels — and the price of that honesty is that every width/fps change re-encodes and re-enters. 480 px cost 72% of 640, not the 56% the pixel area suggests |
| 2026-09-23 | **the proxy ceiling, enforced** | those 3 files (51.2 MiB) under `max_proxy_gb` = 11,900,613 B | `xcut cleanup --dry-run` planned "would evict 3 entries (51.2 MB)" and left the on-disk bytes **identical**; `xcut cleanup` then evicted **3 in 208 ms** → **0 B** | ceiling holds: 0 ≤ 11,900,613 | all three went because the *newest* file (19.2 MB) is itself larger than an 11.9 MB budget — a proxy budget under one proxy's size means no proxy survives. Re-analyzing the asset that lost its file succeeded (rc=0), cost **15.3 s** against the 283 ms cached path, and landed at 6,162,710 B: under budget again, so enforcement is not a one-time purge |
| 2026-09-24 | **a caption re-lay, measured** (B5e: the tap's and the panel's cheapest caption fix) | `BenchmarkReStyleCaptions` on this host (i7-10875H, Windows, `go test -bench -benchtime=20x`, no `-race`): the product's own path — read `transcript.json`, re-validate it, lay it out for the reel's canvas, `WriteAtomic` the `.ass` — over a 1080×1920 `beat_shortform` reel | 200 cues: **11.4 ms/op** (worst 10.5 ms, 15.0 KiB in → 28.3 KiB out, 0.99 MB allocs). 3000 cues (~2 h of speech): **40.3 ms/op** (worst 51.2 ms, 230.7 KiB in → 416.5 KiB out, 14.9 MB allocs) | against the tap's own measured **9.2 s** end to end (B7a), a re-lay is 0.1–0.6% of what it stands in for; the benchmark asserts `worst <= 2 s` so a regression that puts the re-lay within an order of magnitude of a transcription fails the gate rather than the prose | **What is NOT measured here:** the cost of the arm it replaces. A re-transcription runs through the user's own sidecar and model, so no number in this repository covers it and none is invented; the claim being measured is only that a re-lay is milliseconds, which is what makes preferring it over a re-transcription safe to leave in code. The 2 s bound is a headroom assertion, not a target. |

**What B7b settles.** Of the two analysis budgets, only one is a real disk axis: the
sample cache costs ~11.5 KB per minute of source (115,752 B for the match's 10.06 min),
the proxy ~2.5 MiB per minute — two orders of magnitude apart, which is why
`max_cache_gb` (10 GiB) has never been approached by anything and `max_proxy_gb` (2 GiB)
is the one a long project can reach. And in the posture the product actually ships in,
`proxy_enabled` defaults to false, so the 2 GiB proxy budget governs an empty directory:
the rows above are what turns it on costs, not what a default install spends.
