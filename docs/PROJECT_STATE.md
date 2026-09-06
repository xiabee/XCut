# XCut Project State

> The single source of truth for "what actually works right now".
> A future agent reading only this file should know the real state.

Updated: 2026-09-07 01:40 (+08:00) — nightly session #1, post-M9d

## Version / HEAD

- Version: 0.1.0-dev (release artifacts stamped via ldflags)
- HEAD: 7445ae0+ (pushed; CI green on every push since first fix)
- Branch: main

## Working Architecture

- **Go core** (`cmd/xcut`): CLI + localhost HTTP API; typed error model (10
  codes → HTTP mapping); config precedence defaults<JSON<env<flags;
  loopback-forced listen; resource budgets (jobs/ffmpeg/threads/cache/temp).
- **Storage**: SQLite (modernc, no CGO), WAL, migrations v1 (projects/assets/
  jobs), FK on, cascade delete.
- **Jobs**: DB-backed queue; bounded concurrency; sync (CLI) + async (API,
  wg-tracked) execution; panic→failed; startup orphan reconciliation.
- **Pipeline** (`internal/pipeline`): import/analyze/timeline/render as
  functions shared by CLI and API — one behavior, two entry points.
- **Media**: ffprobe/ffmpeg arg-vector exec, timeouts, global process limiter.
- **Analysis**: frame_diff (signalstats YDIF @ sample fps / downscaled width)
  + audio RMS (astats 0.5s windows); fingerprint-keyed cache with mtime-LRU
  eviction to `max_cache_gb`; optional Rust worker for audio (auto: worker
  first, ffmpeg fallback).
- **Events**: deterministic activity segments (cuts split, gaps merge, min
  duration, explainable score).
- **Timeline**: versioned JSON IR v1, strict validation, size-capped loader.
- **Style**: schema-validated JSON presets (embedded + workspace overrides),
  deterministic greedy selection.
- **Render**: per-clip normalize → concat demuxer → ffprobe verify → atomic
  rename from .partial.
- **Rust worker** (`crates/xcut-worker-media`): protocol v1 (describe,
  audio_rms) over stdin/stdout JSON; symphonia decode; parity benchmark vs
  ffmpeg (see PERFORMANCE.md). Optional by design.

## Implemented & Working

- CLI: `version|config show|init|doctor|cleanup [--dry-run]|project
  create|list|show|delete|jobs|import|analyze|timeline|render|auto|serve`
- HTTP API (`/api/v1`, loopback-only): health; projects CRUD; jobs list/detail;
  async triggers — POST projects/{id}/assets (local-path import), /analyze,
  /timeline, /render → 202 + job_id → poll GET /jobs/{id}
- Full E2E verified three ways: CLI commands, `xcut auto`, and pure HTTP flow

## Actually Tested

- `go test ./...` all packages green (incl. `-race` in CI), plus:
  - HTTP async flow test (create→import→analyze→timeline→render, MP4 verified)
  - CLI E2E on real ffmpeg; artifact smoke tests in CI `package` job
  - timeline property tests (300 iterations), safejoin security matrix,
    hostile-filename argv test, cache eviction ordering, orphan recovery
- `cargo test`/`clippy -D warnings`/`fmt --check` green
- `govulncheck`: 0 vulnerabilities affecting code

## CI / Release

- GitHub Actions: go (fmt/vet/test -race on ubuntu+windows), rust
  (fmt/clippy/test), package (cross-build windows/linux amd64+arm64 + artifact
  smoke). Green as of HEAD.
- `scripts/build-release.ps1|.sh` → `dist/` binaries; artifacts validated
  (doctor + full auto pipeline from release exe).

## Known Issues

- symphonia (Rust worker) cannot decode ffmpeg-encoded AAC ("predictor data");
  auto mode's ffmpeg fallback covers it. mp3/flac/wav work in the worker.
- Luma-based cut detection misses chroma-only cuts (e.g. red→green).
- Renderer supports `cut` transitions only; other types are rejected loudly.
- Concurrent xcut processes on one workspace unsupported (reconcile assumes
  single live process). File lock pending.
- serve has no authentication: loopback-only, remote bind refused (by design).
- Mixed audio/no-audio sources handled per-clip via anullsrc (uniform concat).

## Performance (measured, 60s 1080p30 fixture — see docs/PERFORMANCE.md)

- Analysis cold ≈ 0.05x realtime; cache hit ≈ instant
- Render ≈ 0.15x output duration
- Audio RMS: rust worker 0.127s vs ffmpeg 0.143s on 60s mp3 (parity)

## Next Priorities

1. Web UI (Vite + lightweight framework) embedded via go:embed, on top of the
   async API (all backend capabilities already exposed).
2. Proxy generation decision logic (short/small files skip proxying).
3. Model/worker registry UX for the AI sidecar protocol (v1 spec exists).
4. File lock for multi-process safety; log file rotation for serve mode.
5. Badminton preset tuning against real footage; more fixtures.
