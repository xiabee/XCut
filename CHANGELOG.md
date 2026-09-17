# Changelog

All notable changes. Format loosely follows Keep a Changelog; versions are
`0.1.0-dev` until the first tagged release.

## [Unreleased] — 2026-09-17 night session #12

### Fixed
- **Upload staging debris was swept from the wrong place**: the startup
  sweep still only scanned the imports root, but staging files live at
  `imports/<project>/.upload-*` — a serve killed mid-upload stranded its
  staged copy (up to the 8 GiB per-file bound) forever. The sweep now
  descends one bounded level into the per-project directories; landed
  files and project dirs stay untouched.
- **`eval --check` resolves styles like a real eval run**: check mode
  honored workspace style overrides that a real run (throwaway workspace)
  would reject — a manifest could pass check and fail the run. Check is
  embedded-presets-only now, pinned by a test.
- **soak.sh re-runs tonight's code, not last night's**: the cached soak
  binary was reused regardless of age; sources newer than the binary now
  force a rebuild. The busy-delete scenario also asserts the gate
  invariant instead of the timing (an analyze that finishes between the
  202 and the DELETE makes a 200 the correct answer — recorded as an
  honest skip; a gate regression still collapses busy409 toward 0).
- Proxy generation finalizes with the retrying rename: cross-project
  analyses of identical content share one proxy path, and the finalize
  could collide with a reader of the previous proxy (Windows). Failure
  was only a lost optimization; the retry keeps the fast path.

### Added
- **`xcut eval <manifest> --check`**: seconds-fast manifest validation
  without running the pipeline — media existence, ffprobe durations vs
  every annotated range (0.05s rounding slack), style resolution; no
  workspace is created. All problems report in one pass; without ffprobe
  the duration check skips loudly.
- **eval results are self-diagnosing**: `results.json` selected clips now
  carry the style engine's `score`, `reason`, `score_breakdown` and hit
  count/density — WHY each moment was picked lands next to the metrics.
- **eval prints a liveness line per started case** (`[n/total] name
  (style)`): a real-media case runs minutes of ffmpeg; the run no longer
  looks hung until the case finishes.

### Removed
- An accidentally committed shell-glue file (`internal/cli/
  icon_windows.go.tmp`) is gone from the repo; `*.tmp` is gitignored.

## [Unreleased] — 2026-09-17 night session #11

### Fixed
- **Streaming downloads no longer die at 60s**: the server's WriteTimeout
  bounded a response's TOTAL write time (the mirror of the session-10
  upload bug) — a multi-GiB render played in the browser is a slow reader
  by design, so playback/downloads were cut mid-transfer. The three
  streaming routes (render download, asset preview, subtitle download)
  now re-arm the connection write deadline per chunk: progressing
  transfers are never cut, stalled readers still trip the idle window.
- **The rally motion floor tracks the video's own active level**: an
  absolute floor assumed a stable signal scale, but real footage drops
  several-fold within one clip (encode/shutter drift; per-frame
  normalization does not remove it) — on the owner's 10-minute match the
  match point itself was silently refused. The floor now clamps to the
  video's active level (P75 of chunk means, 0.4x, capped at 4x relief);
  on that match, candidate coverage went from 14 to 21 of 21 chunks.
- **Highlight selection is no longer "earliest first"**: scoring factors
  normalized against absolute caps (12 hits, 1.5 hits/s) that every real
  sports chunk saturates, flattening the rank. Factors are now min-max
  normalized within the candidate set; on the real match the selected
  scores spread 0.54-0.88 (was a flat ~0.78).
- **Chunk boundaries snap to the quietest onset window** (±6s): equal
  division cut pieces mid-rally; edges now land in the natural break
  between rallies, and piece coverage reached the full match.
- Project deletion's active-jobs gate is atomic (one conditional
  statement): a trigger enqueueing inside the old check-then-act window
  had its job row cascade-deleted under a live runner.
- Concurrent same-name uploads can no longer overwrite one another:
  pick-free-slot + rename is serialized per server (the TOCTOU window was
  realistic — probes synchronize requests right before the landing
  section).
- SRT cues no longer corrupt on sidecar texts with embedded newlines
  (a blank line inside a cue makes players parse phantom cues).

### Added
- **Segmentation stats name the refusing gate**: "no events satisfy the
  style's clip constraints" now comes with per-gate rejection counters in
  the log (chunks_considered / dropped_motion_floor / dropped_min_hits /
  ...), so a too-strict floor is distinguishable from footage with
  nothing in it.
- **Semantic AI seam (OpenAI-compatible HTTP backends in the reference
  sidecar)**: XCUT_SIDECAR_STT_URL routes transcription through any
  /v1/audio/transcriptions server; XCUT_SIDECAR_VISION_URL drives the new
  frame_describe analyzer (image in, description out) — the seam for
  match-phase awareness and content tagging. Env-configured, no bundled
  models (D3); contract-tested against a stub gateway.
- The soak covers tonight's surfaces: render download (200), 1 KiB range
  request (206 + exactly 1024 bytes), busy-project DELETE (409) then
  idle DELETE (200) — 30 rounds green.

## [Unreleased] — 2026-09-16 night session #10

### Fixed
- **Large uploads no longer die on slow disks**: the server's ReadTimeout
  bounded a request's TOTAL body-read time, so the advertised 8 GiB
  upload needed >280 MB/s to land — a multi-GiB drag-drop import on an
  HDD died mid-transfer. Uploads now stream in 1 MiB chunks and re-arm
  the connection read deadline before each read: total-time bound becomes
  an idle-time bound (progress keeps the transfer alive; a stalled client
  is still cut one window after its last byte).
- Two pure-IR validation tests generated real ffmpeg fixtures they never
  read, making bare `go test ./...` fail on machines without ffmpeg —
  against the documented skip contract. Fixed (fixtures were dead weight);
  the standard build command is green again in a clean environment.

### Added
- **Request failures leave server-side traces**: the api package never
  logged, so a failed upload or job trigger was undiagnosable from
  serve.log (responses carry only user-safe messages by design). Failures
  now log method/path/code plus the full error cause; successful uploads
  log project, name, bytes and asset id.
- The formal soak covers the content-upload surface: duplicate-name
  uploads must land distinct copies (never overwrite) and junk uploads
  must be refused without littering imports/ (30 rounds green).

## [Unreleased] — 2026-09-13 night session #8

### Fixed
- Analyzer failures are no longer misclassified as per-call-budget
  timeouts: run.go read the call context after cancelling it, so EVERY
  analyzer error (a missing ffmpeg, a corrupt file) reported "exceeded
  its 30m0s time budget". The verdict is now taken before cancellation;
  genuine hangs still report the budget (regression-tested for all three
  paths).

### Added
- **Drag-and-drop / file-picker import**: the web UI's media panel accepts
  dropped video files and a "pick files…" button (multiple). Browsers
  cannot reveal local paths, so the files go to the new content endpoint
  `POST /api/v1/projects/{id}/assets/upload` — the server lands a copy
  under `<workspace>/imports/<project>/` (name-sanitized, never
  overwriting an existing copy, 8 GiB per-file bound, failed probes
  cleaned up) and runs the standard import. The user's original file is
  untouched.
- **Orphan-process backstop (Windows)**: every ffmpeg/ffprobe joins a
  job object created with KILL_ON_JOB_CLOSE, so a serve killed without
  running its cleanup (task-manager kill, crash) can no longer leave
  orphan encoders behind — the kernel reaps the tree. Best-effort
  alongside the existing context-kill; a no-op stub keeps non-Windows
  builds unchanged.
- **Brand icon on the packaged exe**: the windows binary carries the
  programmatic mark as an embedded resource (resource-manager .syso
  generated from the same runtime drawing the window uses — one source
  of truth, `scripts/genicon` + rsrc). Resource Explorer, taskbar pins
  and shortcuts all show the mark.
- **Brand icon on the desktop client**: the window (title bar, taskbar,
  Alt-Tab) now carries a programmatic xcut mark — drawn at runtime into
  an in-memory ICO (16/32/48), no binary asset in the repo, no resource
  compiler. CreateIconFromResourceEx silently rejects both PNG and BMP
  payloads on current Windows builds, so the path goes through a temp
  file + LoadImageW (verified via WM_GETICON returning live handles).
- **Double-click friendly**: running the exe with no subcommand on
  Windows now opens the desktop client instead of printing usage and
  exiting (the console-flash "crash"). FFmpeg/ffprobe are also looked up
  next to the executable (or its `bin/` folder) before PATH — drop the
  two exes beside xcut.exe and everything works with zero setup. The
  health endpoint reports `ffmpeg: ok|missing`, and the UI shows a
  persistent yellow setup banner (EN/ZH) until the toolchain appears.
  `scripts/make-installer.ps1` packages the distribution zip.
- Per-source court ROI: the court ROI editor now saves per ASSET
  (`GET/PUT/DELETE /api/v1/projects/{id}/assets/{aid}/roi`, stored in the
  new `assets.motion_roi` column, migration v4). Each fixed camera gets
  its own normalized rect; during timeline generation an asset's own
  region overrides the preset's per-preset one, and assets without a
  rect fall back to the preset (or the full frame). Distinct regions are
  distinct analyzer names, so the analysis cache keeps sources strictly
  separated; when the preset leaves `motion_track` on its default, an
  asset with its own ROI segments its events from the ROI track instead
  of the full-frame signal.
- UI language switch (English / 中文) in the web client topbar. The choice
  persists in localStorage and first-time visitors get the browser
  language's match automatically. Zero dependencies: the zh dictionary is
  a plain-JSON table (`i18n.js`) keyed by the English source strings,
  applied through `data-i18n` attributes and a `t()/tf()` lookup in
  app.js; a Go drift gate (`static_i18n_test.go`) refuses any
  HTML/JS key the dictionary does not cover — and any dictionary entry
  nothing references — on every test run.

## [Unreleased] — 2026-09-13 desktop client (C1–C4)

### Added
- Desktop client: `xcut client` opens a native WebView2 window over the
  in-process loopback server (window close drains like serve; `--browser`
  opens the system browser; non-Windows builds degrade to serve + browser;
  WebView2 detection in `xcut doctor`). The shell is github.com/jchv/
  go-webview2 (MIT, pure-Go syscall binding); it embeds Microsoft's
  authorized-for-redistribution WebView2Loader.dll — see
  docs/CLIENT_DESIGN.md §2.
- Modern editing workspace (the same UI served to browsers): three-pane
  editor (media pool / preview / timeline + inspector), redesigned dark
  theme, media cards with client-captured thumbnails, preview transport.
- Visual timeline editor: clip blocks sized by duration, editable
  transition badges on joins, drag reorder, click-select, trim handles on
  block edges, time ruler that seeks the preview, playhead following the
  per-clip preview.
- Inspector for the selected clip: trim in/out, speed, volume, transition
  type + duration, remove/undo (Delete key), with the clip's score and
  "why" surfaced. Keyboard: Space (play), Delete (remove), Ctrl+S (save).

## [Unreleased] — 2026-09-12/13 nightly session #7

### Added
- Court ROI picker: draw the motion-analysis region on a frame of the
  project's first asset in the web UI; saving persists a workspace
  preset override. API: `GET/PUT/DELETE /api/v1/styles/{name}/roi`
  (normalized 0..1 rect, validated against the preset schema).
- Transcript preview in the Subtitles panel: a toggle fetches the
  project's SRT and renders the cue text inline (textContent only).

### Fixed
- Analysis analyzers stream their metadata output (`metadata=print`) —
  the previous 1 MB keep-last stdout capture silently truncated feature
  tracks of media longer than ~40 minutes; an overflowing capture now
  fails loudly and `StreamStdout`'s stderr is capped as documented.
- Cache eviction is true LRU: a cache/proxy hit refreshes the entry's
  recency, so hot analysis results survive budget pressure instead of
  being evicted FIFO-by-creation.
- Render publish survives a client streaming the previous output:
  serve opens downloads share-all and the publisher POSIX-deletes a
  held destination before renaming (old readers keep their bytes).
- `xcut cleanup --dry-run` no longer runs the real cache eviction.
- Two-layer config no longer drops workspace-level `workers.ai_bin`,
  `proxy_threads`, `max_proxy_gb`, `analyzer_call_timeout` (the latter
  three added to the merge; `proxy_enabled` may opt a workspace in).
- A torn workspace lock file self-heals instead of deadlocking the
  workspace until manually deleted.
- Validation closes two silent-degrade holes: a flush join carrying an
  xfade (output would come out shorter than the document) and a
  fade/xfade with zero duration (rendered as a plain cut).
- Karaoke sweeps land on their word when sidecar timestamps start after
  the segment start, sidecar text is escaped against ASS control
  characters, onset plateaus emit one onset (ties to the earliest hop),
  full-scale negative samples count toward the hop peak, `silence_db`
  is validated finite, and the ffmpeg render budget scales with output
  length instead of a fixed 30-minute cap.

## [Unreleased] — 2026-09-11/12 nightly session #6

### Added
- Auto subtitles (KTV/guitar sing-along): `xcut subtitles <media>` runs
  speech-to-text through the AI sidecar and writes SRT, or karaoke ASS with
  word-level `\kf` fills when the transcript carries word timings. The
  reference sidecar (v0.2.0) probes for openai-whisper / faster-whisper /
  whisper-cli and honestly reports unavailable until one is installed —
  installing any backend turns the capability on with zero XCut changes
  (the core never downloads models, D3). The web UI gained a Subtitles
  panel (asset picker, Transcribe, status, downloads) and a "burn
  subtitles" checkbox on the render row; the API exposes
  `POST /projects/{id}/subtitles`, `GET .../subtitles[?format=]` and
  `{"subs": true}` on render. `xcut render --subs file` burns subtitles in
  the same job (audio stream-copied, duration verified, atomic publish).
- Chroma-aware scene-cut detection: the frame_diff analyzer now reads
  signalstats UDIF/VDIF alongside YDIF and emits the per-channel max
  (analyzer version 2, cache-invalidating). Red→green-class chroma-only
  scene switches — missed by luma-only detection — now split events.
- Cross-compile sanity for the new packages is covered by the existing
  gate; no new runtime dependencies.

### Fixed
- Rally detection works on real court audio: gap-based clustering collapsed
  whole recordings into one rally because ambience keeps the onset detector
  firing through every break. Rally mode now clusters by onset density with
  hysteresis (enter/exit rates + sustained-low close) and chunks over-dense
  spans instead of truncating them. First validated end-to-end on a real
  10-minute fixed-camera men's-singles match (previously: "no events
  satisfy the style's clip constraints"; now: an 8-clip 60s highlight).
- Atomic writes retry through Windows scanner holds: Defender briefly
  holding a freshly written file made WriteAtomic/render-publish renames
  fail with "Access is denied" — spurious 500s and lost writes under rapid
  rewrite. All publish points share `workspace.RetryableRename`.
- Timeline regeneration is now actually serialized with manual PUTs and
  backup restores (the mutex the API comment claimed existed): a PUT that
  landed mid-regeneration used to be destroyed at the same revision and
  concurrent renames failed on Windows.
- Re-importing the same media file keeps the asset's ID — a second import
  used to re-key the row and brick every stored timeline referencing it.
- Failed renders keep their scratch for post-mortem (the cleanup decision
  keyed off the job context, which stays live across a plain ffmpeg
  failure, so failed runs hit the success branch and deleted their
  evidence).
- The serve shutdown drain is bounded (30s) and warns loudly on expiry; a
  job wedged outside its cancellation can no longer own the shutdown path.
- CLI command failures print the user-safe message instead of the raw
  error chain (wrapped absolute paths and ffmpeg stderr stay in `-v` logs).
- `handleAssetFile` no longer folds storage failures into "unknown asset"
  404s; timeline backup restore rolls back a failed swap so the undo is
  never silently consumed; eval cases with sanitization-colliding names
  are disambiguated instead of erroring; absurd disk budgets (1e18 GB) are
  clamped instead of overflowing int64 and disabling budgets.

## [Unreleased] — 2026-09-10/11 nightly session #5

### Added
- Job cancellation: `POST /api/v1/jobs/{id}/cancel` plus a Cancel button on
  every queued/running job row in the web UI. Queued jobs cancel before
  their body runs; running jobs lose their ffmpeg children with the job
  context and land in the `cancelled` terminal state. Cancelled renders
  reclaim their scratch; a second cancel on a terminal job is a `409`.
- serve startup sweeps: orphaned queued/running job rows are reconciled
  regardless of age (serve holds the workspace writer lock, so any row it
  sees was left by a dead process) and `temp/` crash debris is reclaimed,
  both reported on stdout. Previously a crash could leave a phantom
  "running" job blocking its project with `409`s for up to
  `job.stale_running_after` (2h), and scratch piled against the temp
  budget while the only remover (`xcut cleanup`) was lock-refused.
- gosec (HIGH severity, HIGH confidence) is wired into the full local
  gate; findings are suppressed only per-site with written justifications.
- UI: job polling self-heals after the server disappears — capped backoff
  retries, a banner after repeated failures, and `watchUntilDone` gives up
  on vanished job rows instead of polling them at 1 Hz forever.

### Fixed
- A cancelled job that wins the concurrency-slot race no longer leaves a
  phantom queued row: the slot-acquisition select can legally pick the
  slot while cancellation is already pending, and `SetJobRunning` then
  failed on the cancelled context without any terminal bookkeeping. The
  job now re-checks cancellation after acquiring a slot, and a failed
  start write falls back to a best-effort `failed` finish; all job
  bookkeeping failures log loudly instead of being discarded.
- Timeline saves are revision-guarded: `PUT /timeline` must send the
  revision it read (GET returns it); a mismatch — another tab saved, or
  the timeline was regenerated — is a `409` instead of silently destroying
  the other writer's clips. Regeneration bumps the revision too, and
  revision-less blind overwrites of an existing document are refused.
- serve sets full HTTP timeouts (read/write/idle): a stalled reader of a
  media response no longer pins a handler goroutine forever, and a second
  Ctrl+C force-exits a wedged graceful drain.
- Applying a rally-mode style (badminton_highlight) to media without audio
  streams fails early, naming the missing signal, instead of burning a
  full analysis pass and dying on the generic "no events satisfy…" error.
- Validation refuses a trailing xfade (nothing to blend with — the
  renderer used to silently drop the declared transition); a trailing
  fade stays legal as the fade-out.

### Docs
- README: the knob table now lists every configuration knob (8 were
  undocumented); the migration count in PROJECT_STATE matches the code.

## [Unreleased] — 2026-09-09/10 nightly session #4

### Added
- `resource.max_render_workers` (default 1) now bounds concurrent render
  jobs with a dedicated queue semaphore; previously renders shared the
  generic job pool, so `max_concurrent_jobs=4` meant up to four
  simultaneous ffmpeg encodes.
- `resource.max_temp_gb` is enforced: no new scratch is created once
  `temp/` sits at the budget (the error names `xcut cleanup` / stopping
  `xcut serve`), and a render aborts if its own scratch outgrows the
  remaining allowance.
- Duplicate-trigger protection: at most one queued/running analyze,
  timeline or render job per project — API duplicates get `409`, backed by
  a partial UNIQUE index (migration v2). Imports stay concurrent.
- Job history retention: `jobs.max_history` (default 500) prunes terminal
  job rows as jobs finish; the jobs list no longer grows forever.
- UI: regenerating the timeline now asks twice ("Replace timeline?
  Click again") — regeneration overwrites manual editor edits; the
  pre-regeneration document remains recoverable via Restore backup.
- Project deletion guard: `DELETE /api/v1/projects/{id}` and
  `xcut project delete` refuse while the project has queued/running jobs.

### Fixed
- `resource.max_ffmpeg_processes` is now real: `media.Run` acquires the
  global process limiter internally and renders run through it too —
  previously only proxy generation and onset streaming were capped while
  probe, analyzers and all three render stages bypassed it.
- `POST /render` with an empty body works; a malformed body returns one
  clean 400 and queues nothing (previously the client got a double-written
  response while the render ran anyway).
- Analysis proxies are keyed by source fingerprint + analysis geometry:
  raising `analysis_width` / changing `frame_sample_fps` regenerates
  instead of silently reusing a proxy built for the old canvas.
- Cache eviction never deletes another writer's in-flight `.tmp-*` scratch
  (on Linux that made concurrent analyses fail with rename ENOENT).
- Clip `speed` is bounded to [0.1, 10] with a 24h per-clip/total render
  cap: tiny speeds used to overflow clip duration to +Inf (timeline PUT
  returned 500) or turn a typo into a many-hour render.
- Render verify tolerance is now two frames + 5% relative; the old flat
  0.5s floor let a 24%-truncated 2s render publish as "verified".
- Worker calls honor `resource.analyzer_call_timeout` (the built-in
  10-minute cap used to win regardless of configuration), and a sidecar
  that answers but never exits no longer holds the call until the deadline.
- Child ffmpeg/ffprobe output capture is capped at 1 MB (last bytes win):
  corrupt media spewing per-packet decode errors can no longer grow host
  memory for the whole render.

## [Unreleased] — 2026-09-08/09 nightly session #3

### Fixed
- Timeline validation rejects placement gaps: the renderer joins clips
  back-to-back and never honors TimelineStart gaps, so gapped timelines
  used to fail only as a confusing post-render duration mismatch.
- Renderer honors clip `speed`: setpts/atempo apply the full source range
  at the requested pace (previously a sped clip was silently truncated to
  its first seconds); frame-color integration tests prove the mapping in
  both directions, offset-seek keeps the tail, speed+xfade composes.
- Timeline editor honors speed: displayed durations, status total and
  save-time placement accumulate playback durations (with an `@Nx` marker).

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
  LRU-evicted) surfaced in `xcut cleanup` and `xcut cache stats|clear`;
  `resource.proxy_threads` gives the one-shot encode its own thread budget.
- `xcut cache stats [--json]` and `xcut cache clear [--dry-run]`: inspect
  and clear the analysis + proxy caches (analysis entries only — projects,
  user media and the DB are never touched).
- Renderer loudly refuses unsupported timeline shapes (audio tracks,
  multi-track timelines, clip effects) instead of silently mis-rendering.
- Per-analyzer call timeout (`resource.analyzer_call_timeout`, default
  30m): a hung ffmpeg fails the job instead of pinning a worker slot.
- `xcut doctor` reports analysis/proxy cache usage against budgets.

### Changed
- Timeline regeneration keeps a one-level undo: the previous document is
  backed up to `timeline.backup.json` and
  `xcut timeline <proj> --restore-backup` swaps it back.
- Renderer: cut/fade joins now render inside the xfade filtergraph via the
  concat filter — `cut`, `fade` and `xfade` transitions may be freely
  mixed within one timeline (previously refused). Join offsets accumulate
  actual output duration; Σ durations − Σ xfade semantics preserved.
- `xcut cleanup` also reclaims stale render partials under `projects/`
  (crash debris; custom `--out` paths untouched).

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
