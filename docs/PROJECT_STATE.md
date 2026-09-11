# XCut Project State

> The single source of truth for "what actually works right now".
> A future agent reading only this file should know the real state.

Updated: 2026-09-11 08:40 (+08:00) — nightly session #5, close

## Version / HEAD

- Version: 0.1.0-dev (release artifacts stamped via ldflags)
- HEAD: ef43063+ (local commits; not pushed — CI is workflow_dispatch-only,
  D11; push decision for the maintainer)
- Branch: main
- CI: quota-constrained (D11). `ci.yml` is workflow_dispatch-only;
  `release.yml` stays tag-triggered. Validation is local
  (scripts/ci-local.ps1 → check.ps1 fast) plus the remote-node node
  (night-automation ci run) as independent acceptance. Session #5: 18 remote
  runs = 17 PASS + 1 FAIL (that FAIL caught a real race — see
  NIGHTLY_PROGRESS M37); cumulative 46 runs = 45 PASS + 1 FAIL.

## Working Architecture

- **Go core** (`cmd/xcut`): CLI + localhost web UI + HTTP API; typed error
  model (11 codes incl. conflict → 409); two-layer config; loopback-forced
  listen; resource budgets (jobs/ffmpeg/threads/cache/temp/log rotation/
  job-history retention) — all enforced, see config.json defaults.
- **Workspace lock** (`xcut.lock`, O_EXCL): writer commands serialize;
  readers lock-free; stale locks of dead PIDs auto-reclaimed (crash-safe);
  E2E verified with serve + CLI + forced kill.
- **Storage**: SQLite (modernc, no CGO), WAL, migrations v2 (v2 adds the
  partial unique index for exclusive active jobs), FK, cascade.
- **Jobs**: DB-backed queue; bounded concurrency; sync (CLI) + async (API);
  panic→failed; startup orphan reconciliation (age-gated for CLI opens;
  serve sweeps ALL rows — it holds the writer lock, so any row it sees at
  startup is dead-process debris, fresh or not); **job cancellation**
  (`POST /api/v1/jobs/{id}/cancel` + UI button): per-job cancel contexts,
  queued jobs cancel before their body runs, running jobs' ffmpeg children
  die with the context, cancelled renders reclaim their scratch.
- **Pipeline** (`internal/pipeline`): import/analyze/timeline/render shared
  by CLI and API. Timeline builds append style-driven analyzers (court ROI).
  Render outputs are guarded against overwriting source media, timeline-
  referenced clip sources, or the timeline document.
- **Media**: ffprobe/ffmpeg arg-vector exec, timeouts, global process
  limiter; `StreamStdout` for bounded streaming passes.
- **Analysis** (`internal/analysis`): frame_diff (motion + cuts), audio RMS
  (astats), **audio onsets** (PCM pipe → Go DSP: 20 ms peak envelope → flux
  → median+k·MAD adaptive threshold → local-max peaks), **court-ROI motion**
  (crop before signalstats; ROI in the analyzer name = cache-safe).
  Fingerprint-keyed cache with budget eviction; optional **analysis
  proxies** (opt-in `resource.proxy_enabled`: fingerprint+geometry-keyed low-res
  proxies at the analysis geometry under `cache/proxy`, own LRU budget
  `resource.max_proxy_gb`, own encode thread budget `resource.proxy_threads`,
  proxy bit in the cache key, per-call analyzer timeout
  `resource.analyzer_call_timeout` default 30m); optional Rust worker
  (auto: worker-first with ffmpeg fallback; rust: strict; ffmpeg: builtin).
  The renderer refuses unsupported timeline shapes (audio/multi-track,
  effects) loudly; clip speed is fully honored (setpts + atempo).
- **Events** (`internal/event`): activity segmentation (default) and rally
  mode (`mode: "rally"`: transient clustering → gap split → padding →
  min-hits + motion gating). Segments carry hit_count/hit_density (activity
  segments too, as onset density).
- **Style** (`internal/style`): presets are data (embedded + workspace
  overrides); explainable selection — every clip carries score,
  score_breakdown and reason in its metadata; diversity block
  (min_gap / max_overlap_iou) suppresses near-duplicates; hits/density
  scoring weights (zero = legacy).
- **Presets**: generic_highlight, badminton_highlight v2 (rally mode),
  ktv_mv v2 (onset-density weighted). Optional motion_roi block.
- **Timeline → Render**: versioned timeline IR + server-managed document
  **Revision** (PUT saves must send the revision they read; mismatch → 409;
  regeneration bumps it too — stale editors can no longer silently destroy
  a doc), strict validation (xfade overlaps validated against transition
  duration; trailing xfade refused — nothing to blend with), manual editing
  (GET/PUT + UI editor), renderer with trim/normalize/concat **or a single
  join filtergraph chaining xfade+acrossfade (transition joins) and concat
  (hard joins) — cut/fade/xfade may be mixed freely within one timeline**,
  ffprobe verify, atomic publish; render output refused if it would
  overwrite a source media file, a timeline-referenced clip source, or the
  timeline document.
- **Eval** (`internal/eval` + `xcut eval`): annotated manifests → temporal
  IoU / precision / recall / F1 / range hits / duplicate rate; JSON
  results; isolated throwaway workspace per run. docs/EVAL.md.
- **Web UI** (go:embed, zero deps): project CRUD, import, analyze →
  timeline → render with job progress, clip editor with per-clip score +
  why, MP4 playback/download. Untrusted text rendered textContent-only.
- **Workers**: Rust media worker (protocol v1, audio_rms) optional;
  AI sidecar protocol v1 (capabilities/health/analyze; bounded response
  caps, per-call timeouts, .py sidecar support); reference sidecar in
  `scripts/xcut-ai-sidecar.py` (stdlib, no models — honest baseline).

## Implemented & Working (browser- or CLI-verified)

- CLI: `version|config show|init|doctor|cleanup [--dry-run]|cache
  stats|clear [--dry-run]|project create|list|show|delete|jobs|import|
  analyze [assetIDs]|timeline|render|auto|serve|eval`
- HTTP `/api/v1`: health, projects CRUD (delete guarded while jobs are
  active → 409), jobs (+ `POST /jobs/{id}/cancel`: 202 / 404 / 409
  terminal-or-orphan), async triggers (one active analyze/timeline/render
  per project — duplicates → 409), timeline GET/PUT (revision-guarded
  saves; stale revision → 409), styles list, render download
  (range-capable playback)
- Web UI: project CRUD, import, analyze/timeline/render with per-job
  Cancel button and polling that self-heals after serve restarts
  (capped backoff + reconnect banner); two-step regenerate confirm;
  per-clip preview, drag reorder, speed-aware durations
- Full E2E paths re-verified this session: `xcut auto` (generic), render
  refusing `--out` onto source media (source byte-identical after), mixed
  transition renders (xfade+cut, xfade+fade), proxy-backed analyze
  (proxy generated, original untouched), `xcut cache` stats/clear,
  cancel-mid-analyze on the release binary (zero leftover ffmpeg
  processes), crash recovery (kill -9 serve mid-analyze → restart sweeps
  the orphan and accepts a new analyze immediately), serve startup temp
  sweep (seeded debris reclaimed)

## Actually Tested

- `go test ./...` all packages green (full suite re-run after each
  milestone; integration tests run against `.tools` ffmpeg 9.0.1)
- `go test -race` all packages green on the Windows host (full gate) —
  a gcc toolchain (windows-gnu, from the Rust setup) now satisfies the
  gate's cgo requirement
- govulncheck clean on go1.26.6 (session #3 bumped the toolchain from
  go1.26.4: four stdlib advisories in crypto/tls, net/http, encoding/asn1
  affected called code); gosec HIGH/HIGH clean in the full gate since
  session #5 (5 path-taint findings annotated with written justifications)
- Remote acceptance: `night-automation ci run XCut --node remote-node` PASS after
  every milestone (38 consecutive passes cumulative through session #5);
  the node caught one real concurrency bug local runs had missed (M37)
- `cargo fmt --check`/`clippy -D warnings`/`cargo test` green (windows-gnu
  toolchain fallback — no MSVC Build Tools on this machine)
- govulncheck: installed (repo-local .tools/bin); run in the full gate
- Cross-compile checks: linux amd64+arm64 (compile-verified; linux also
  runtime-verified in session #1 via WSL)

## Known Issues

- symphonia (Rust worker) cannot decode ffmpeg-encoded AAC; auto mode's
  ffmpeg fallback covers it.
- Luma-based cut detection misses chroma-only cuts (e.g. red→green).
- Renderer transitions: `cut`, `fade` (through black) and `xfade` (real
  crossfade with overlapping placement; transitions may now be freely
  mixed within one timeline — xfade joins blend, cut/fade joins join
  back-to-back in the same filtergraph).
- Analysis proxies re-encode audio (AAC 96k), so proxy-based results
  differ slightly from original-audio analysis; the cache key keeps the
  two strictly separated. Proxy decision is width-based only — a
  high-resolution low-fps source still benefits, a tiny-fps source
  already decodes cheaply.
- Badminton v2's rally detection is validated on synthetic fixtures only —
  real annotated match footage is the missing ingredient (use
  `xcut eval` + docs/EVAL.md workflow; court ROI needs per-source manual
  rects).
- Race detector on Windows hosts needs a cgo/C toolchain (gcc); the gate
  runs it when one is present and skips loudly otherwise (docker runner
  remains the fallback). This machine's windows-gnu gcc satisfies it since
  session #3.
- serve has no auth: loopback-only by construction; remote bind refused.
- Manual timeline edits are overwritten by style regeneration (by design;
  the UI two-step confirm warns, a backup keeps one level of undo, and the
  document revision gives stale editors a loud 409 instead of silent loss).
- This machine's WDAC policy intermittently blocks freshly built test
  binaries in %TEMP% (`go test -c -o <path>` + direct run works around it;
  go run may fail) — environmental, not a product issue.

## Performance (measured — docs/PERFORMANCE.md)

- serve idle: 14.2 MB WS / ~0% CPU (session #5 re-check; goals met)
- analyze 30-min 1080p30: 35.9 s wall / **0.12x realtime** (session #4
  re-check after limiter + capped output capture; no regression)
- render (concat): 3.3 s wall for a 10 s 720p30 clip (session #4 A/B
  against the pre-M20 baseline: identical, no regression)
- audio onset 0.121 s / RMS 0.176 s per 60 s audio (decode-bound)

## Next Priorities

1. Real-footage evaluation: annotate a few real badminton/KTV clips, run
   `xcut eval`, tune badminton v2 (rally_gap/pad/min_hits, ROI rect) on
   measurements — the harness exists, it needs real data.
2. AI sidecar: first real analyzer (Whisper transcript → event labels) on
   top of protocol v1 if a local Whisper exists; else capability remains
   honestly absent.
3. Optional: re-enable push/PR CI when quota recovers (restore notes in
   ci.yml; consider concurrency cancel + docs-only paths-ignore).
4. Phase 4 (desktop packaging, model registry, FFmpeg sandbox) — needs
   maintainer decisions; deliberately untouched by night work.
