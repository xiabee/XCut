# Changelog

All notable changes. Format loosely follows Keep a Changelog; versions are
`0.1.0-dev` until the first tagged release.

## [Unreleased] — 2026-09-08/09 nightly session #3

### Added
- Timeline editor polish (Phase 3 item complete): per-clip source preview
  (▶ seeks the clip's source to its start offset) and drag-to-reorder rows
  (HTML5 DnD; save recomputes timeline_start). Backed by the new
  `GET /api/v1/projects/{id}/assets/{assetID}/file` endpoint — DB-registered
  paths only, project-ownership enforced, range-capable; the client never
  supplies a path.
- Render output overwrite guard: `xcut render --out` (and the API's
  `{"out"}`) refuse paths matching imported assets, timeline-referenced
  clip sources, or the timeline document (same-file detection via
  os.SameFile plus normalized comparison). Imports are referenced in
  place, so a clobbered source is unrecoverable.
- Analysis proxies (opt-in `resource.proxy_enabled`): fingerprint-keyed
  low-res proxies under `cache/proxy` encoded at the analysis geometry so
  analyzer passes decode sampled frames instead of the full source; cache
  key carries a proxy bit; `resource.max_proxy_gb` budget (default 2 GB,
  LRU-evicted) surfaced in `xcut cleanup` and `xcut cache stats|clear`.
- `xcut cache stats [--json]` and `xcut cache clear [--dry-run]`: inspect
  and clear the analysis + proxy caches (analysis entries only — projects,
  user media and the DB are never touched).

### Changed
- Renderer: cut/fade joins now render inside the xfade filtergraph via the
  concat filter — `cut`, `fade` and `xfade` transitions may be freely
  mixed within one timeline (previously refused). Join offsets accumulate
  actual output duration; Σ durations − Σ xfade semantics preserved.

## [Unreleased] — 2026-09-07/08 nightly session #2

### Added
- Evaluation harness: `internal/eval` metrics (temporal IoU, precision/
  recall/F1, range hits, duplicate rate) + `xcut eval` command driven by
  annotated manifests; runs in an isolated throwaway workspace; JSON
  results; docs/EVAL.md.
- Audio onset/transient analyzer (`audio_onset` track): streamed PCM
  decode -> Go DSP (peak envelope, positive flux, median+k*MAD adaptive
  threshold, local-max picking); deterministic, bounded memory.
- Rally segmentation mode (`event_config.mode = "rally"`): clusters audio
  transients into rally candidates with gap split, padding, min-hits and
  motion gating; segments carry hit_count/hit_density.
- Explainable selection: every clip's metadata carries score,
  score_breakdown and dominant-factor reason; web UI shows score + why.
- Diversity selection: preset `diversity` (min_gap, max_overlap_iou)
  suppresses near-duplicate picks; enabled in badminton v2 and ktv v2.
- Court ROI: preset `motion_roi` -> cropped motion analyzer
  (`frame_diff_roi`), cache-safe; event `motion_track` auto-wired.
- Presets: badminton_highlight v2 (rally mode, hit-driven scoring),
  ktv_mv v2 (onset-density weighted).
- Cross-process workspace lock (`xcut.lock`): writers serialize, readers
  lock-free, stale locks of dead owners auto-reclaimed; CodeConflict ->
  HTTP 409.
- AI sidecar protocol v1: capabilities/health/analyze ops, bounded
  response/stderr caps, per-call timeouts, .py sidecar interpreter
  probing, config `workers.ai_bin` + `XCUT_AI_BIN`, doctor discovery;
  reference sidecar `scripts/xcut-ai-sidecar.py` (stdlib, no models).
- True crossfade: `xfade` transition type with overlapping timeline
  placement (duration semantics Σ − transitions), chained
  xfade+acrossfade render combine using probed part durations;
  `generic_xfade` preset.
- Local quality gate `scripts/check.sh|ps1` (fast/full) +
  `scripts/race-docker.sh` (linux race in container).

### Changed
- CI: `ci.yml` now workflow_dispatch-only (Actions quota policy, D11);
  validation moved local-first (DECISIONS D11).
- style scoring accepts hits/density weights (zero = legacy behavior).
- analyze CLI output includes onset track in track/sample counts.

### Fixed
- testmedia formatFloat stripped integer trailing zeros (10s fixtures
  silently became 1s).
- audio analyzer: astats `-inf` (digital silence) broke analysis-cache
  JSON serialization; mapped to -120 dBFS.
- rally heuristic removed (cut-inside-window no longer drops rallies);
  diversity gates trimmed clip windows (padded segments over-suppressed).
- web UI: asset filenames and job error messages rendered via
  textContent (XSS via hostile filenames no longer possible).
- check scripts: race step skipped loudly without cgo; rust falls back to
  windows-gnu when the msvc linker is missing; docker path mangling fixed.

### Measured
- 30-min 720p analyze: 0.035x realtime, ffmpeg child ~33 MB peak RSS.
- onset 0.121s vs astats RMS 0.176s per 60s audio (decode-bound).

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
