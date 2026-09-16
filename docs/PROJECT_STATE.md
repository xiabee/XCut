# XCut Project State

> The single source of truth for "what actually works right now".
> A future agent reading only this file should know the real state.

Updated: 2026-09-17 03:45 (+08:00) — nightly session #11 checkpoint

## Version / HEAD

- Version: 0.1.0-dev (release artifacts stamped via ldflags)
- HEAD: session #11 (real-footage review fixes: streaming download
  write-idle heartbeat, adaptive rally motion floor, within-set
  relative scoring, chunk-boundary snapping; segmentation gate stats;
  atomic delete gate; upload landing mutex; SRT newline fix; semantic
  AI seam in the sidecar; soak covers the new surfaces) on top of
  session #10 (upload read-deadline heartbeat, API failure traces),
  all pushed; the FULL gate ran green at the session #11 HEAD
- Branch: main
- CI: local gate (scripts/ci-local.ps1 → check.ps1 fast) is the acceptance
  entry; remote-node remote runs after every milestone

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
- **Media/API**: uploads stream with a read-deadline heartbeat and the
  three streaming routes (render download, asset preview, subtitle
  download) with a write-idle heartbeat — neither the server's total-time
  ReadTimeout nor WriteTimeout caps a progressing transfer any more; both
  are idle windows (a stalled peer is still cut one span after its last
  byte). Same-name upload landing is serialized (never-overwrite holds
  under concurrency); project deletion's active-jobs gate is one atomic
  statement. Request failures land in serve.log (method/path/code +
  cause) while responses stay user-safe, and segmentation logs per-gate
  rejection counts.
- **Media**: ffprobe/ffmpeg arg-vector exec, timeouts, global process
  limiter; `StreamStdout` for bounded streaming passes. Every child joins a
  KILL_ON_JOB_CLOSE Windows job object at Start (session #8): a serve killed
  without its cleanup can no longer leave orphan encoders — the kernel reaps
  the tree; any attach failure degrades to the context-kill path.
- **Analysis** (`internal/analysis`): frame_diff (motion + cuts; chroma-
  aware since session #6 — max of YDIF/UDIF/VDIF normalized per channel
  span, catching chroma-only scene switches), audio RMS
  (astats), **audio onsets** (PCM pipe → Go DSP: 20 ms peak envelope → flux
  → median+k·MAD adaptive threshold → local-max peaks; plateaus emit one
  onset, ties to the earliest hop), **court-ROI motion**
  (crop before signalstats; ROI in the analyzer name = cache-safe).
  All three metadata-print analyzers **stream** their ffmpeg output
  (session #7: `metadataCollector` as the StreamStdout sink) — analyzer
  memory is flat in media duration; the old 1 MB keep-last capture
  silently truncated long-media tracks. Fingerprint-keyed cache with
  budget eviction — **true LRU since session #7** (a hit refreshes the
  entry's recency; hot results survive budget pressure); optional
  **analysis proxies** (opt-in
  `resource.proxy_enabled`: fingerprint+geometry-keyed low-res
  proxies at the analysis geometry under `cache/proxy`, own LRU budget
  `resource.max_proxy_gb`, own encode thread budget `resource.proxy_threads`,
  proxy bit in the cache key, per-call analyzer timeout
  `resource.analyzer_call_timeout` default 30m); optional Rust worker
  (auto: worker-first with ffmpeg fallback; rust: strict; ffmpeg: builtin).
  The renderer refuses unsupported timeline shapes (audio/multi-track,
  effects) loudly; clip speed is fully honored (setpts + atempo).
- **Events** (`internal/event`): activity segmentation (default) and rally
  mode (`mode: "rally"`: onset-density hysteresis walk → chunking with
  quiet-valley boundary snapping → pad → min-hits + adaptive motion
  gating). Segments carry hit_count/hit_density (activity segments too,
  as onset density). Build returns per-gate rejection stats so an empty
  result is diagnosable (logged per asset). The rally motion floor is
  ADAPTIVE (clamped to the video's own active level) — real footage
  drifts several-fold within one clip; chunk boundaries snap to the
  quietest onset window nearby instead of the arithmetic grid.
- **Style** (`internal/style`): presets are data (embedded + workspace
  overrides); explainable selection — every clip carries score,
  score_breakdown and reason in its metadata; scoring factors are
  min-max normalized WITHIN the candidate set (absolute caps saturate on
  real footage and flatten the rank to "earliest first"); diversity block
  (min_gap / max_overlap_iou) suppresses near-duplicates; hits/density
  scoring weights (zero = legacy).
- **Presets**: generic_highlight, badminton_highlight v2 (rally mode),
  ktv_mv v2 (onset-density weighted). Optional motion_roi block.
- **Timeline → Render**: versioned timeline IR + server-managed document
  **Revision** (PUT saves must send the revision they read; mismatch → 409;
  regeneration bumps it too — stale editors can no longer silently destroy
  a doc), strict validation (xfade overlaps validated against transition
  duration; trailing xfade refused — nothing to blend with; a flush join
  carrying an xfade refused — the blend would shorten the output; a
  zero-duration fade/xfade refused — it renders as a plain cut), manual
  editing (GET/PUT + UI editor), renderer with trim/normalize/concat **or a
  single join filtergraph chaining xfade+acrossfade (transition joins) and
  concat (hard joins) — cut/fade/xfade may be mixed freely within one
  timeline**, ffprobe verify, atomic publish; the ffmpeg budget scales with
  output length (30-minute floor + headroom per output second — a fixed
  30-minute cap could not render multi-hour timelines); render output
  refused if it would overwrite a source media file, a timeline-referenced
  clip source, or the timeline document. Publish uses replace semantics
  (`RetryableReplace`): a client streaming the previous output no longer
  fails a re-render — serve opens downloads share-all, so the publisher
  POSIX-deletes the held name and renames; the old reader keeps its bytes
  until EOF (session #7). Publish uses replace semantics (`RetryableReplace`):
  a client streaming the previous output no longer fails a re-render —
  serve opens downloads share-all, so the publisher POSIX-deletes the held
  name and renames; the old reader keeps its bytes until EOF (session #7).
- **Eval** (`internal/eval` + `xcut eval`): annotated manifests → temporal
  IoU / precision / recall / F1 / range hits / duplicate rate; JSON
  results; isolated throwaway workspace per run. docs/EVAL.md.
- **Subtitles** (`internal/subs` + `xcut subtitles` + AI sidecar, session
  #6): speech-to-text through the AI sidecar protocol v1 (reference sidecar
  probes openai-whisper / faster-whisper / whisper-cli; honest "unavailable"
  until one is installed — the core never downloads models, D3). Produces
  SRT plus karaoke ASS (word-level `\kf` fills; gaps belong to the previous
  word). In serve: `POST /projects/{id}/subtitles` (recorded job), status +
  download endpoints, `{"subs": true}` render burn; UI: Transcribe button
  with asset picker, status line, download links, burn-subs checkbox —
  browser-verified end to end.
- **Web UI** (go:embed, zero deps): project CRUD, import, analyze →
  timeline → render with job progress, MP4 playback with transport,
  download. **UI language switch** (2026-09-13 night #8): English / 中文
  via the topbar selector — localStorage-persisted, browser-language
  auto-detect on first visit, zero deps (plain-JSON dictionary in
  i18n.js + data-i18n attributes + t()/tf() in app.js), drift-gated by
  static_i18n_test.go.
  **Drag-drop / file-picker import** (session #9): the media panel accepts
  dropped or picked files — content uploads to
  `POST /projects/{id}/assets/upload`, landing a sanitized, non-overwriting
  copy under `imports/<project>/` (8 GiB bound, failed probes cleaned,
  staged debris swept at startup); the path-import form stays alongside. **Modern editing workspace** (2026-09-13): three-pane editor —
  media pool with client-captured thumbnails, visual timeline (clip
  blocks sized by duration, editable transition badges, drag reorder,
  edge-handle trimming, time ruler seeking the preview, playhead), and an
  inspector for the selected clip (trim/speed/volume/transition, score +
  why; Delete/Space/Ctrl+S shortcuts). Subtitles panel with asset picker,
  transcript preview, status + downloads; court ROI picker (draw the
  motion region on a reference frame, saved as a workspace preset
  override). Project-scoped refreshes carry stale-response guards
  (switching projects discards in-flight responses — a slow response can
  no longer render project A's data under project B). Untrusted text
  rendered textContent-only.
- **Desktop client** (`xcut client`, 2026-09-13): the same UI in a native
  WebView2 window over the in-process loopback server — window close
  drains like serve, `--browser` falls back to the system browser,
  non-Windows builds degrade to serve + browser, doctor reports the
  WebView2 runtime. Design: docs/CLIENT_DESIGN.md.
- **Workers**: Rust media worker (protocol v1, audio_rms) optional;
  AI sidecar protocol v1 (capabilities/health/analyze; bounded response
  caps, per-call timeouts, .py sidecar support); reference sidecar in
  `scripts/xcut-ai-sidecar.py` (stdlib, no models — honest baseline).

## Implemented & Working (browser- or CLI-verified)

- CLI: `version|config show|init|doctor|cleanup [--dry-run]|cache
  stats|clear [--dry-run]|project create|list|show|delete|jobs|import|
  analyze [assetIDs]|timeline|render|auto|serve|eval|subtitles`
- HTTP `/api/v1`: health, projects CRUD (delete guarded while jobs are
  active → 409), jobs (+ `POST /jobs/{id}/cancel`: 202 / 404 / 409
  terminal-or-orphan), async triggers (one active analyze/timeline/render
  per project — duplicates → 409; subtitles jobs are not deduplicated),
  timeline GET/PUT (revision-guarded saves; stale revision → 409; restore
  endpoint), subtitles trigger/status/download, styles list, render
  download (range-capable playback), render `{"subs": true}` burn
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
  sweep (seeded debris reclaimed). Session #6 additions: real-footage
  rally highlight end to end (10-min match → 8-clip 60s render, probed),
  subtitle burn verified at the pixel level (frame with vs without subs),
  web-client subtitle loop browser-verified (transcribe → downloads →
  burn-subs render → player refresh).

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
- Cut detection is chroma-aware since session #6 (max of YDIF/UDIF/VDIF);
  thresholds remain tuned for hard cuts — long crossfades are
  deliberately NOT cuts (tested) and extremely slow dissolves could
  still read as gradual motion rather than a scene change.
- Renderer transitions: `cut`, `fade` (through black) and `xfade` (real
  crossfade with overlapping placement; transitions may now be freely
  mixed within one timeline — xfade joins blend, cut/fade joins join
  back-to-back in the same filtergraph).
- Analysis proxies re-encode audio (AAC 96k), so proxy-based results
  differ slightly from original-audio analysis; the cache key keeps the
  two strictly separated. Proxy decision is width-based only — a
  high-resolution low-fps source still benefits, a tiny-fps source
  already decodes cheaply.
- Badminton v2 on real footage (session #11 review, owner's 10-min
  match REDACTED): the pipeline now covers the whole match (21/21
  chunks candidates; the match point included), selection is driven by
  genuine score differences, and piece boundaries snap to natural
  breaks. STILL OPEN: (a) the reel's 60s budget fills from the
  highest-scoring chunks, which are the early bright ones — the
  match-point section is a candidate but not guaranteed selection;
  guaranteeing climax presence is match-phase semantics (needs the
  vision-AI seam, whose gateway model was 503 during the session);
  (b) every PROVISIONAL constant (0.4 floor ratio, P75 baseline, 4x cap,
  ±6s snap) awaits annotated eval — docs/EVAL.md now carries the
  worked-example recipe (scoreboard = ground truth). The court ROI is
  per-asset (UI picker, assets.motion_roi) and validated on this
  footage.
- Render publish vs holds: a client streaming the previous output no
  longer blocks a re-render (share-all downloads + POSIX delete + rename,
  session #7). An EXTERNAL program that opens without the Windows
  delete-share bit (some players) still pins the name — the publish
  reports the rename error honestly instead of pretending to succeed.
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

- serve idle: 16.6 MB WS / 0 ms CPU per 10 s (session #11 re-check; goals met)
- analyze 30-min 1080p30: 35.9 s wall / **0.12x realtime** (session #4
  re-check after limiter + capped output capture; no regression)
- render (concat): 3.3 s wall for a 10 s 720p30 clip (session #4 A/B
  against the pre-M20 baseline: identical, no regression)
- audio onset 0.121 s / RMS 0.176 s per 60 s audio (decode-bound)

## Next Priorities

1. Real-footage evaluation (UNBLOCKED, recipe ready): docs/EVAL.md now
   has the badminton worked example — the burned-in scoreboard makes
   rally annotation mechanical (~41 rallies), a manifest template sits in
   the gitignored /eval/, and every provisional rally constant
   (floor ratio, P75, snap range) plus the climax-guarantee question is
   waiting on exactly these numbers.
2. Subtitles with a real Whisper: install faster-whisper locally and run
   `xcut subtitles` on real singing content (the plumbing is tested; the
   model load is deliberately not night work).
3. (done, session #8) Per-source court ROI — per-asset override shipped
   (assets.motion_roi, v4) with the UI picker saving per asset; measured
   4x signal vs full-frame dilution (NIGHTLY_PROGRESS M100).
4. Re-enable push/PR + tag CI when the GitHub account billing issue is
   resolved (Actions jobs are refused at start; restore notes in
   ci.yml/release.yml unchanged — the files are fine).
5. Phase 4 leftovers: tray/auto-update and model registry — need
   maintainer decisions; desktop packaging itself (zip, icons) shipped
   in session #8.
