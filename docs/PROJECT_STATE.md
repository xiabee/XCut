# XCut Project State

> The single source of truth for "what actually works right now".
> A future agent reading only this file should know the real state.

Updated: 2026-09-07 02:55 (+08:00) — nightly session #1, end of feature work

## Version / HEAD

- Version: 0.1.0-dev (release artifacts stamped via ldflags)
- HEAD: fbe87b4+ (pushed; CI green through 1169a0f — see blocker below)
- Branch: main
- ⚠️ CI blocker (external): GitHub Actions stopped starting jobs mid-night
  with "recent account payments have failed or your spending limit needs to
  be increased". All pushes after ~18:21Z are locally verified (build/vet/
  full tests/gofmt/JS check) but have no CI run. First morning task: check
  Actions billing, then re-run the latest workflow.

## Working Architecture

- **Go core** (`cmd/xcut`): CLI + localhost web UI + HTTP API; typed error
  model (10 codes → HTTP mapping); two-layer config (bootstrap +
  `<workspace>/config.json` overlay); loopback-forced listen; resource
  budgets (jobs/ffmpeg/threads/cache/temp/log rotation).
- **Storage**: SQLite (modernc, no CGO), WAL, migrations v1, FK, cascade.
- **Jobs**: DB-backed queue; bounded concurrency; sync (CLI) + async (API,
  wg-tracked) execution; panic→failed; startup orphan reconciliation.
- **Pipeline** (`internal/pipeline`): import/analyze/timeline/render shared
  by CLI and API; sync + async variants per operation.
- **Media**: ffprobe/ffmpeg arg-vector exec, timeouts, global process limiter.
- **Analysis**: frame_diff + audio RMS analyzers; fingerprint-keyed cache
  with budget eviction; optional Rust worker (auto: worker-first with ffmpeg
  fallback; rust: strict; ffmpeg: builtin).
- **Events → Style → Timeline → Render**: deterministic segmentation,
  schema-validated presets (generic_highlight / badminton_highlight /
  ktv_mv), versioned timeline IR with strict validation + manual editing
  (GET/PUT API, UI editor), renderer with ffprobe verify + atomic publish.
- **Web UI** (`internal/api/static`, go:embed vanilla JS): project CRUD,
  local-path import, analyze→timeline→render with live job progress,
  timeline clip editor (remove/reorder/save), in-browser MP4 playback +
  download. Zero npm dependencies.
- **Rust worker** (`crates/xcut-worker-media`): protocol v1 (describe,
  audio_rms); optional by design.

## Implemented & Working (all browser- or CLI-verified)

- CLI: `version|config show|init|doctor|cleanup [--dry-run]|project
  create|list|show|delete|jobs|import|analyze [assetIDs]|timeline|render|
  auto|serve`
- HTTP: health, projects CRUD, jobs, async triggers, timeline GET/PUT,
  styles list, render download (range-capable playback)
- Full E2E verified four ways: CLI steps, `xcut auto`, HTTP-only flow
  (Go test), and real-browser session (create → import → analyze → timeline
  → edit → render → playback, output probed at each step)

## Actually Tested

- `go test ./...` all packages green (CI ran with `-race` until the billing
  blocker), including: HTTP async flow, CLI E2E, artifact smoke in CI,
  timeline property tests, safejoin security matrix, hostile filenames,
  cache eviction ordering, orphan recovery, log rotation, config layering
- `cargo test`/`clippy -D warnings`/`fmt --check` green
- `govulncheck`: 0 vulnerabilities affecting code

## Known Issues

- **CI billing blocker** (see above) — external, needs account owner.
- symphonia (Rust worker) cannot decode ffmpeg-encoded AAC; auto mode's
  ffmpeg fallback covers it (mp3/flac/wav work).
- Luma-based cut detection misses chroma-only cuts (e.g. red→green).
- Renderer supports `cut` transitions only; others rejected loudly.
- Concurrent xcut processes on one workspace unsupported (file lock pending).
- serve has no auth: loopback-only by construction; remote bind refused.
- Manual timeline edits live outside style regenerations (regenerate
  overwrites manual edits — by design, documented in UI copy? NO: not yet
  surfaced; minor UX note).

## Performance (measured — docs/PERFORMANCE.md)

- serve idle: **12.3 MB RAM / ~0% CPU** (goals <100 MB / ~0%: met)
- analyze ≈ 0.05x realtime (60s 1080p); render ≈ 0.15x output duration
- audio RMS rust vs ffmpeg: parity (0.127s vs 0.143s on 60s mp3)

## Next Priorities

1. Check GitHub Actions billing; re-run CI on HEAD.
2. Fade transitions in renderer (xfade) — style presets already carry the
   intent; renderer rejects non-cut today.
3. Per-asset analysis parallelism inside a single job (queue-level for now).
4. AI sidecar protocol v1 implementation (spec exists in ARCHITECTURE.md).
5. Badminton preset tuning on real footage; court-ROI analyzer sketch.

