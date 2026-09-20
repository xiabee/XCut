# XCut Project State

> The single source of truth for "what actually works right now".
> A future agent reading only this file should know the real state.

Updated: 2026-09-20 (morning) — session #14 (API authentication, D12)
checkpoint

## Version / HEAD

- Version: 0.1.0-dev (release artifacts stamped via ldflags)
- HEAD: session #14 (API authentication — D12): a static bearer token gates
  every non-loopback peer of `/api/v1`; loopback stays trusted so the desktop
  client and the double-clicked exe need zero setup. `listen_remote` is now a
  usable option instead of a refusal, but only when paired with a
  >=24-character token (config.Resolve refuses the pair apart, and serveAddr
  re-checks it before binding); constant-time compare, per-peer failure budget
  (20 per 5 min -> 429) with a bounded tracker, rejections logged without the
  token, `config show` masks it as `<set>`, `xcut init` never writes it to
  disk, doctor reports the posture; error model gained unauthorized/forbidden
  -> 401/403) on top of session #13 (owner-directive night: UI v2 visual
  refresh; one-
  click FFmpeg install — pinned Gyan.dev 9.0.1 artifact, size+SHA256 in
  code, zip-slip-guarded extract into <exe>/bin, ffprobe -version gate,
  live neighbor re-probe so no restart is needed; true Windows installer
  via Inno Setup (installer/xcut.iss + scripts/make-setup.ps1) with an
  InfoBefore policy page and real uninstaller; /health ai_sidecar probe;
  CLI output guards refusing subtitles/eval --out onto their own inputs)
  on top of session #12 (eval workflow tooling: `--check` fast manifest
  validation, self-diagnosing results with per-clip score/reason,
  per-case liveness lines, faithful style resolution; the startup
  sweep now reclaims upload staging inside per-project imports dirs —
  it previously never did; soak.sh rebuilds its binary when sources
  move past it and its busy-delete asserts the gate invariant, not the
  timing; proxy finalize uses the retrying rename; stray .tmp debris
  removed) on top of session #11 (streaming download write-idle
  heartbeat, adaptive rally motion floor, within-set relative scoring,
  chunk-boundary snapping, segmentation gate stats, atomic delete gate,
  upload landing mutex, SRT newline fix, semantic AI seam in the
  sidecar — details in CHANGELOG), all pushed
- Branch: main
- CI: local gate (scripts/ci-local.ps1 → check.ps1 fast) is the acceptance
  entry; a remote CI node re-runs the same gate after every milestone. Since
  session #14 the remote leg must include a node on the *other* platform
  (`scripts/check.sh full` on Linux against a clean `git archive` snapshot):
  a node sharing the dev box's OS re-verifies the machine, not the
  cross-platform claim, and that is how eight red Linux tests survived twelve
  sessions.

## Working Architecture

- **Go core** (`cmd/xcut`): CLI + localhost web UI + HTTP API; typed error
  model (13 codes incl. conflict → 409, unauthorized → 401, forbidden → 403);
  two-layer config; loopback-forced
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
- **Remote UI sessions** (session #14, D14): a browser cannot put a header on
  `<video src>`, thumbnails or download links, so `POST /api/v1/session`
  (proven by the bearer token) mints a 256-bit id delivered as
  `HttpOnly; SameSite=Strict; Path=/` and returned once in the body. Reads may
  ride that cookie; every other method must echo the id in `X-Cut-Session`,
  which a cross-site page cannot produce from an HttpOnly cookie — the asymmetry
  is the CSRF defence rather than a token check. The access token is never
  persisted client-side (only the session id, in `sessionStorage`), a reload
  stays signed in, `DELETE /api/v1/session` and a topbar Sign out revoke, and
  the store is in-memory with a 12 h TTL capped at 256 sessions (refusing past
  the cap, never growing).
- **API authentication** (session #14, D12): `/api/v1/*` requires
  `Authorization: Bearer <server.auth_token>` from any peer whose socket
  address is not loopback; loopback peers are trusted, so local clients stay
  zero-config. The token comes from workspace config or `XCUT_AUTH_TOKEN`
  (never a CLI flag), must be ≥24 chars, and `listen_remote` without it is a
  startup error — re-checked in `serveAddr` so no path reaches `net.Listen` on
  a weak pair. Comparison is `crypto/subtle`; rejections carry no detail, are
  logged without token or supplied header, and a per-peer budget (20 failures
  per 5 min → 429, tracker capped at 4096 peers) stops brute force. The UI
  shell (no user data) is deliberately outside the gate. Cleartext transport:
  remote binds belong on a trusted network or a tunnel, and a same-machine
  reverse proxy would bypass the gate by making peers loopback (SECURITY.md).
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
  results (selected clips carry the style engine's score/reason/breakdown
  so runs are self-diagnosing); isolated throwaway workspace per run —
  which also means style resolution is embedded-presets-only there.
  `xcut eval <manifest> --check` validates media existence, ffprobe
  durations against every annotated range and style resolution in
  seconds, no workspace created (session #12). docs/EVAL.md.
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
  copy under `imports/<project>/` (8 GiB bound, failed probes cleaned;
  the startup sweep reclaims `.upload-*` staging debris **inside the
  per-project directories too** — session #12 fixed the sweep that only
  covered the imports root); the path-import form stays alongside. **Modern editing workspace** (2026-09-13): three-pane editor —
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
  terminal-or-orphan), every route reachable from a non-loopback peer
  requiring `Authorization: Bearer` (401, 429 once the failure budget is
  spent; loopback peers and the UI shell need neither),
  async triggers (one active analyze/timeline/render
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
- Secret scanning: gitleaks (repo-local .tools/bin) runs in every gate —
  full git history + working tree (uncommitted edits included); the scan
  fails the gate on any finding (verified with a planted secret)
- `go test -race` all packages green on the Windows host (full gate) —
  a gcc toolchain (windows-gnu, from the Rust setup) now satisfies the
  gate's cgo requirement
- govulncheck clean on go1.26.6 (session #3 bumped the toolchain from
  go1.26.4: four stdlib advisories in crypto/tls, net/http, encoding/asn1
  affected called code); gosec HIGH/HIGH clean in the full gate since
  session #5 (5 path-taint findings annotated with written justifications)
- Remote acceptance: an independent CI node re-runs the project gate PASS
  after every milestone (38 consecutive passes cumulative through session
  #5); the node caught one real concurrency bug local runs had missed (M37)
- Authentication (session #14): a 14-case gate matrix (loopback trusted with a
  bad token, remote no-header/wrong/prefix/extended → 401, correct +
  case-insensitive scheme → 200, no-token-configured → 403 for remote),
  per-peer brute-force budget with window expiry and per-peer isolation, a
  capped failure tracker (2× the cap in distinct peers never grows it past
  4096), a log assertion that neither the configured nor the supplied token
  reaches serve.log, a 7-case config policy table (remote without/short/
  whitespace token refused; token alone never lifts the loopback pin), and
  `serveAddr` refusal/accept tests. **Real socket E2E** on the built binary:
  bound `0.0.0.0:8777` with a 48-char token in a throwaway workspace — loopback
  health 200 without a token, LAN peer 401 (no header, wrong token), 200 with
  the token, UI shell 200 from the LAN peer, 20 failures then 429 (`resource_limit`)
  with the correct token still refused while locked out, loopback unaffected;
  `netstat` confirmed those peer connections carried the LAN source address.
  Startup refusals verified on the binary too (no token; short token).
- **Linux full gate now green on a real Linux node** (session #14): a clean
  `git archive` snapshot run through `scripts/check.sh full` — gofmt, vet,
  build, `go test`, `go test -race` (38 packages), linux amd64+arm64
  cross-compile, `cargo fmt --check` + `cargo clippy -D warnings` + `cargo
  test` — exit 0, against Ubuntu's FFmpeg **6.1.1** (older than the 9.0.1 the
  Windows gate uses, so the suite is now known to pass on two toolchain
  generations). govulncheck/gosec/gitleaks are not installed on that node and
  skipped loudly there; they ran clean on the Windows host at the same HEAD.
- **ARM64 is runtime-verified, not just compile-verified** (session #14): the
  cross-built binary ran a complete workflow on Kylin V10 SP1 aarch64 —
  version/doctor/init/project create/import/analyze (3 tracks, 48 samples,
  1 event)/timeline/render (4.8 MB in 3.1s)/cache/cleanup, and the rendered
  output probed back at 10.02 s with video+audio. Requires a stock FFmpeg:
  see the Kylin vendor-plugin limitation in Known Issues.
- **Remote UI sessions verified in a real browser over a LAN peer address**
  (session #14), not only by unit test: the unauthenticated page shows the
  sign-in modal (localized), signing in with the token returns 201 with
  `Set-Cookie: xcut_session=…; Path=/; Max-Age=43200; HttpOnly; SameSite=Strict`,
  the modal closes and Sign out appears; **a media URL fetched with no request
  header at all returns 200 / `video/mp4` / the full 1,090,855 bytes** (the
  thing D12 could not do); the same-origin mutation with the cookie but no
  echo returns 401, and with `X-Cut-Session` returns 201; `document.cookie`
  cannot see the session (HttpOnly proven from the page); a reload keeps the
  session without re-prompting. Plus: a forged 64-hex cookie 401s, logout
  revokes for both cookie and header, loopback needs nothing, curl-level
  coverage of the same matrix, an 18-case `authorize` table (method × cookie ×
  header × malformed), a TTL/expiry/prune/revoke lifecycle test, and a hard-cap
  test (`maxSessions` issues then refusal, table never grows).
- Pre-existing test flake fixed: `TestSetupStatusShapeWhileDownloading` let the
  install goroutine write into its TempDir after the test returned, racing Go's
  cleanup ("directory is not empty", 1 of 4 runs); cleanup now waits for a
  terminal phase — 5 consecutive `-count=1` runs green.
- `cargo fmt --check`/`clippy -D warnings`/`cargo test` green (windows-gnu
  toolchain fallback — no MSVC Build Tools on this machine)
- govulncheck: installed (repo-local .tools/bin); run in the full gate
- Cross-compile checks: linux amd64+arm64 (compile-verified; linux also
  runtime-verified in session #1 via WSL)
- **Linux test suite now actually runs, and it was red.** Session #14 ran
  `scripts/check.sh full` on a Linux node against a clean `git archive`
  snapshot for the first time since the Windows-only installer landed: 8
  failures, all pre-existing (A/B-confirmed identical at the previous
  session's HEAD), all platform-shaped rather than product-broken — plus one
  genuine product bug (Content-Type came from the host MIME table, so Linux
  served `.webm` previews as `audio/webm` and the player refused them).
  Fixed in session #14: the table is pinned in code, the three Windows-shaped
  assertions now assert what each OS guarantees, the installer tests are
  gated to Windows, and two new tests pin the *refusal* non-Windows users
  actually get. Lesson recorded: a "remote CI node" that runs the same OS as
  the dev box verifies the machine, not the platform claim.

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
- Badminton on real footage — **now measured against derived ground truth**
  (session #14, docs/EVAL.md carries the recipe). The clip is a 10:03 men's
  singles in a **shared six-court hall**, which changes what the signals mean:
  audio onsets are not court-specific, so `hits`+`density` (65% of the preset's
  score) rank when the *hall* was busiest. Measured on it, P 0.742 → **0.886**
  and F1 0.167 → **0.199** from two changes: a per-phase pick cap (the reel had
  been filling entirely from the first two thirds — the closing phase, match
  point included, is now present, confirmed by frame inspection) and placing
  the clip window at the segment start instead of its middle (rally chunks are
  cut on quiet valleys, so the middle of a 30s chunk is often the pause after a
  point). STILL OPEN: (a) climax presence is now structural but *which* rally
  per phase is still ranked by contaminated audio — that needs the vision-AI
  seam to identify the players' own strokes; (b) `ranges_hit` is capped by the
  budget (8 clips × 8 s cannot represent 41 rallies of ~10 s), so representing
  more of a match means either a longer reel or splitting long segments into
  several scored windows — the latter needs a per-window activity profile on
  `event.Segment`, which does not exist yet; (c) the remaining PROVISIONAL
  constants (0.4 floor ratio, P75 baseline, 4x cap, ±6s snap) are still untuned
  against this manifest. The court ROI is per-asset (UI picker,
  assets.motion_roi).
- Render publish vs holds: a client streaming the previous output no
  longer blocks a re-render (share-all downloads + POSIX delete + rename,
  session #7). An EXTERNAL program that opens without the Windows
  delete-share bit (some players) still pins the name — the publish
  reports the rename error honestly instead of pretending to succeed.
- Race detector on Windows hosts needs a cgo/C toolchain (gcc); the gate
  runs it when one is present and skips loudly otherwise (docker runner
  remains the fallback). This machine's windows-gnu gcc satisfies it since
  session #3.
- **Kylin V10 SP1's packaged FFmpeg cannot drive XCut's import** (found
  session #14 on real hardware): its Hisilicon OMX decoder plugin logs to
  stdout while ffprobe writes JSON there, interleaving *inside* lines. XCut
  refuses with a message naming the build and the way out
  (`XCUT_FFPROBE`/`XCUT_FFMPEG` at a stock build — proven to make the whole
  chain work on that machine). No CLI flag avoids it (`-loglevel quiet`
  changes the byte count by zero; `-out_filename` is unsupported; the
  `-show_entries` form still opens the decoder), and filtering the buffer is
  rejected on evidence, not taste: see DECISIONS D13.
- serve authentication is a **static shared bearer token over cleartext HTTP**
  (D12): no TLS, one token for all clients, and no rotation surface (rotate =
  edit config + restart, which also drops every session). The failure budget
  and the session table both reset with the process, and the loopback
  exemption means any local process can still reach the API. Remote binds
  therefore belong on a trusted network or inside a tunnel. The web UI does
  work remotely (D14 sessions), but its media is served unencrypted, and a
  same-origin XSS would still read data through a session — the
  textContent-only rendering rule is what holds that line.
- The remote sign-in panel's **visual layout is unverified**: it was driven and
  asserted in a real browser (modal appears, sign-in succeeds, media loads on
  the cookie, mutations refused without the echo, reload stays signed in), but
  the harness viewport was 0×0 so no screenshot could be taken. Nobody has
  looked at how it renders.
- Manual timeline edits are overwritten by style regeneration (by design;
  the UI two-step confirm warns, a backup keeps one level of undo, and the
  document revision gives stale editors a loud 409 instead of silent loss).
- This machine's WDAC policy intermittently blocks freshly built test
  binaries in %TEMP% (`go test -c -o <path>` + direct run works around it;
  go run may fail) — environmental, not a product issue.

## Performance (measured — docs/PERFORMANCE.md)

- serve idle: 16.4 MB WS flat / 0.00 s CPU over 45 s (session #14 re-check after auth +
  sessions; goals met)
- API payload: `GET /projects/{id}` 5936 → 558 bytes (−90.6%) once the stored
  ffprobe blob stopped being serialized into responses (session #14)
- analyze on real footage: 603 s 720p30 in 28.1 s = 0.047x realtime, xcut peak
  23.8 MB / ffmpeg child peak 55.7 MB (session #14)
- analyze 30-min 1080p30: 35.9 s wall / **0.12x realtime** (session #4
  re-check after limiter + capped output capture; no regression)
- render (concat): 3.3 s wall for a 10 s 720p30 clip (session #4 A/B
  against the pre-M20 baseline: identical, no regression)
- audio onset 0.121 s / RMS 0.176 s per 60 s audio (decode-bound)

## Next Priorities

1. Remote-access hardening, in the order the risk suggests (the remote web UI
   itself shipped as D14): (a) a documented tunnel recipe so a bearer token
   never crosses an untrusted wire — the product has no TLS and D12 says so;
   (b) actually look at the sign-in panel (see the unverified-layout note
   above); (c) token rotation as an operator action rather than
   edit-config-restart.
2. Real-footage evaluation (UNBLOCKED, recipe ready): docs/EVAL.md now
   has the badminton worked example — the burned-in scoreboard makes
   rally annotation mechanical (~41 rallies), a manifest template sits in
   the gitignored /eval/, and every provisional rally constant
   (floor ratio, P75, snap range) plus the climax-guarantee question is
   waiting on exactly these numbers.
3. Subtitles with a real Whisper: install faster-whisper locally and run
   `xcut subtitles` on real singing content (the plumbing is tested; the
   model load is deliberately not night work).
4. (done, session #8) Per-source court ROI — per-asset override shipped
   (assets.motion_roi, v4) with the UI picker saving per asset; measured
   4x signal vs full-frame dilution (NIGHTLY_PROGRESS M100).
5. Re-enable push/PR + tag CI when the GitHub account billing issue is
   resolved (Actions jobs are refused at start; restore notes in
   ci.yml/release.yml unchanged — the files are fine).
6. Phase 4 leftovers: tray/auto-update and model registry — need
   maintainer decisions; desktop packaging itself (zip, icons) shipped
   in session #8 and the true installer in session #13.
7. TLS for the API (or a documented tunnel recipe in an OPERATIONS doc):
   D12's bearer token crosses the wire in plaintext, which is why remote
   binds are documented as trusted-network/tunnel-only today.
