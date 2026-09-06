# Changelog

All notable changes. Format loosely follows Keep a Changelog; versions are
`0.1.0-dev` until the first tagged release.

## [Unreleased] — 2026-09-06/07 nightly session #1

### Added
- Deterministic pipeline: import → analyze → events → style → timeline →
  render, executable via CLI (`xcut auto` or step commands) or localhost HTTP.
- CLI: `version`, `config show|path`, `init`, `doctor`, `cleanup [--dry-run]`,
  `project create|list|show|delete`, `jobs`, `import`, `analyze`, `timeline`,
  `render`, `auto`, `serve`.
- HTTP API `/api/v1` (loopback-only): health, projects CRUD, jobs list/detail,
  async triggers (assets/analyze/timeline/render → 202 + job_id).
- Baseline analyzers: frame_diff (motion + cuts, sampled/downscaled) and audio
  RMS windows; fingerprint+config cache with size-budget eviction.
- Event segmentation with deterministic scoring; style presets
  (`generic_highlight`, `badminton_highlight`) with schema validation;
  timeline IR v1 with strict validation; renderer with ffprobe verification
  and atomic publish.
- SQLite persistence (no-CGO driver), migrations, job lifecycle + orphan
  reconciliation after crashes.
- Rust worker `xcut-worker-media` (protocol v1: describe, audio_rms) with Go
  client; strictly optional, ffmpeg fallback in `auto` mode.
- GitHub Actions CI: go (linux+windows, race), rust (fmt/clippy/test),
  packaging job with artifact smoke test.
- Release scripts (`scripts/build-release.ps1|.sh`) → cross-compiled binaries
  under `dist/`.
- Docs set: ARCHITECTURE, SECURITY, PERFORMANCE, DECISIONS, ROADMAP,
  PROJECT_STATE, ACCEPTANCE, NIGHTLY_LOG.

### Security
- No-shell exec contract (arg-vector only) with regression tests; hostile
  filename test.
- Workspace SafeJoin (absolute/`..`/drive/UNC/reserved names/symlink escapes);
  backslash-rooted paths rejected on all platforms.
- Loopback-only server; remote bind refused until auth exists.
- Size caps on config/style/timeline parsing; upload-style body caps.
- No telemetry; secrets via env or git-ignored files only.

### Known gaps
- see docs/PROJECT_STATE.md "Known Issues".

### Added (late session)
- Embedded web UI (`go:embed` vanilla JS): project CRUD, local-path import,
  analyze→timeline→render with live job progress, in-browser MP4 playback;
  timeline clip editor (remove/reorder) with server-side validation.
- `xcut serve`: rotated file logging (`log.max_size_mb`/`max_files`).
- `ktv_mv` style preset (audio-led scoring).
- Tag-triggered release workflow (`v*` → build + binaries attached).
- Measured idle footprint: 12.3 MB RAM, ~0% idle CPU (PERFORMANCE.md).

### Fixed
- `--workspace X` now honors `X/config.json` (two-layer config with
  MergeLayer); previously silently ignored.
- `xcut analyze <project> [assetID...]` positional filters actually filter.
- Rust worker: all `presets/*.json` embedded via glob (ktv_mv initially
  missing from binary).
- SafeJoin rejects backslash-rooted paths on all platforms (CI-found).
