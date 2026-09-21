# XCut Project State

> The single source of truth for "what actually works right now".
> A future agent reading only this file should know the real state.

Updated: 2026-09-21 (late morning) — session #18: the rally-end gap closed with
imported data instead of a new heuristic. The sidecar can now read a burned-in
scoreboard (`score_changes`), `xcut boundaries` stores the point ends per asset,
and the style engine ends a clip at the nearest reachable mark — measured on the
owner's match: P 0.886 → **0.998**, all 8 clips ending within 0.05 s of a point
end where before they missed by 0.5–5.6 s (docs/EVAL.md; D15). The A/B also
caught a real defect the unit tests could not see: a manifest-relative media
path reached the sidecar and failed as "no such file" on a file that exists.
Session #17 (the secret-scan gate now says what it actually scanned (and fails
rather than warning), the Tailscale recipe measured on a real tailnet peer with
the published arm64 binary, and a `phase=done`-before-cleanup race in the
FFmpeg installer found by running the gate on a host without FFmpeg) is
unchanged below. Session #16 (sign-in visual pass + the budget
defect it caught) is unchanged below.

**Same session, later:** the region is now drawable in the web UI (the court
picker gained a target select; "scoreboard region" writes
`assets.score_crop`), and **analyze** is what measures it — marks stored on the
asset row, dropped when the region moves, and reported `stale` if a scan
predates the current rect. Verified end to end in a real browser session (draw →
`marks: 0` + "run analyze" → analyze → `marks: 2` at the drawn crop, the fixture's
two changes), on the DOM/HTTP path — screenshots stay unavailable through the
connector (0×0 viewport), so no pixel claim is made. The same marks turned out
**not** to fix the clip head: snapping the start forward to the next boundary
cost the tail rule its boundary (P 0.977 → 0.641, 21/21 → 3/21 ends on a point)
— measured, recorded, and rejected in `docs/EVAL.md`.

**Same session, later still:** the one-shot path gained the region —
`xcut auto clip.mp4 --score-crop x,y,w,h` writes it during import and analyze
measures it, so the footage no longer needs a `boundaries scan` round trip.
Acceptance at `dc7b4b7`: local fast gate PASS (456 passed / 7 skipped, gitleaks
history 328 commits + worktree 3295 paths clean), `--node win-devops` PASS
(job `20260921-184732-fcf4c9`, same 456/7 — the node runs the fast gate, so it
covers no Rust), and `scripts/check.sh full` on the Linux node PASS with
`-race` across all 22 packages. That Linux run reported
`not run: rust` because the dispatch replaced `PATH` instead of appending to it;
the Rust worker was then verified separately on the same snapshot
(`cargo fmt --check`, `cargo clippy -D warnings`, `cargo test` rc=0 / 3 passed).

## Version / HEAD

- Version: 0.1.0-dev (release artifacts stamped via ldflags); v0.1.8-alpha
  tagged from an earlier session
- HEAD: session #16/#17 (2026-09-20 night) — the auth-gate failure budget
  charges only requests that presented a credential (the sign-in page's own
  health poll could previously lock its address out and then 429 the correct
  token); a reel length shorter than the style's minimum clip is refused
  where both numbers are at hand instead of "rejected all events" after a
  full analysis pass; the path-import submit handler the workspace redesign
  dropped is restored (clicking Import used to navigate the page away),
  with a form-wiring completeness gate; `resource.ffmpeg_max_memory_mb` caps
  each ffmpeg child through the Windows job object (opt-in, uncapped
  default; verified kernel-side and against real ffmpeg, dies cleanly);
  doctor reports the sandbox posture; MergeLayer carries the new knob (plus
  a reflection completeness gate over every Config field) — all on top of
  the session #15 work (reel length override + the phase-quota fix it
  exposed, `docs/OPERATIONS.md`, UI id-resolution gate) and session #14
  shipped as **v0.1.8-alpha** (API authentication — D12, remote UI sessions
  — D14): a static bearer token gates every non-loopback peer of `/api/v1`;
  loopback stays trusted so the desktop client and the double-clicked exe
  need zero setup. `listen_remote` is a usable option only when paired with
  a >=24-character token (config.Resolve refuses the pair apart, and
  serveAddr re-checks it before binding); constant-time compare, per-peer
  failure budget (20 presented-credential rejections per 5 min -> 429) with
  a bounded tracker, rejections logged without the token, `config show`
  masks it as `<set>`, `xcut init` never writes it to disk, doctor reports
  the posture; error model gained unauthorized/forbidden -> 401/403. Earlier
  layers (session #13 installer + one-click FFmpeg install, session #12 eval
  tooling, session #11 streaming/heartbeats) — details in CHANGELOG, all
  pushed
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
  listen; resource budgets (jobs/ffmpeg/threads/child memory cap (Windows job
  object, opt-in `resource.ffmpeg_max_memory_mb`)/cache/temp/log rotation/
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
  media pool with client-captured thumbnails (kept across reloads in
  localStorage, keyed by asset id and invalidated by the content fingerprint —
  a cached frame is painted without touching the media, measured 3 requests per
  view → 0 after reload), visual timeline (clip
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
- **v0.1.8-alpha Release Gate — every artifact really ran.** Windows exe
  (`version`, `serve`, `/health`, UI index), linux-amd64 on the Linux node
  (`doctor`, FFmpeg 6.1.1), linux-arm64 on real Kylin V10 SP1 (`doctor`,
  vendor FFmpeg 4.2.2 reported), and the portable zip (extracted to a clean
  dir, `xcut.exe version`, QUICKSTART.txt present). The installer completed a
  full unattended cycle: `/CURRENTUSER /VERYSILENT` install → `version` →
  `serve` on a non-loopback address with the token posture live (401 without a
  token, 401 with a wrong one, 200 with the right one, `POST /api/v1/session`
  → 201) → `unins000.exe /VERYSILENT` removed the directory and the HKCU
  uninstall key. Two things learned: a silent run must name its scope
  (`PrivilegesRequiredOverridesAllowed=dialog` otherwise waits on the
  "just me / all users" choice — the first attempt returned success and
  installed nothing), and `pkill -f` from Git Bash does not match a Windows
  process, so a smoke-test server kept `xcut.exe` locked and the first
  uninstall left it behind. The scope flag is now documented in the README
  install recipe; the locked-file cause was my own leftover test server, not
  the uninstaller.
  NOT VERIFIED: the interactive wizard path (no UI session available in CI)
  and all-users elevation — same script, different scope flag.
- Acceptance for the tagged sha itself came from two channels: the control
  plane (`xnightops ci run XCut` → local PASS with the secret scan, then
  `--node win-devops` → remote PASS recorded against the same sha) and a
  cross-platform leg (`git archive v0.1.8-alpha` onto the Linux node,
  `check.sh full` with `GOFLAGS=-count=1`, green including `-race` and the
  Rust worker; that node skips `govulncheck`, so only the Windows leg covers
  it). An SSH local-forward into a loopback-bound `xcut serve` was also driven
  end to end — which is the recipe the docs tell remote users to run, so it
  should not be documented from a blog post.

## Known Issues

- **One unreproduced 500 on the Windows CI node (open).** `api/TestTimelineRegenAndPutRevisionUniqueness`
  failed on win-devops at `984a1d4` (job `20260921-173929-cfff4f`) with
  `timeline_api_test.go:458: unexpected PUT status 500` — the test's contract is
  "every concurrent PUT is 200 or 409". Same commit: the local fast gate passed
  (454 tests), and the test itself passed 40 consecutive local runs and again
  under `-count=8`. The node's own SSH account cannot run builds (`Permission
  denied` for the CI user's key), so it has not been hammered where it failed.
  **What changed instead of a guess:** the test now keeps each rejected PUT's
  response body (it printed a bare status before), and
  `TestPutFailureCarriesItsReason` proves that channel carries a named reason —
  so the next occurrence says which step refused rather than joining this list.
- **One unreproduced `DATA RACE` on the Linux leg (open).** Session #18's
  `check.sh full` at `1f7c899` failed with `testing.go:1712: race detected
  during execution of test` in `cli.TestE2EAutoScopesToRunInputs`; the surviving
  stack half is the inline job path
  (`cmdAuto → cmdAnalyze → Deps.AnalyzeProject → job.Queue.RunInline → safeRun`,
  `internal/job/queue.go:112/132/300`). Re-runs at the same commit: 12× the
  package alone, 3× the whole suite, 25× that test — all clean, so the window is
  narrow and load-dependent (the leg runs packages in parallel on 4 cores).
  **Not fixed and not closed**: the report's other half was cut off because the
  leg script trimmed the gate output with `| tail -45`, which also read `tail`'s
  exit code and printed `LEG_EXIT=0` over a failing run. The leg now keeps the
  gate log whole on the node and copies it back, so the next sighting yields the
  pair. Nothing was relaxed meanwhile — the two suspect shapes (a writer
  outliving `Run`, or that test's reused `stdout/stderr` buffers) are recorded
  here instead of being papered over by a change to either.
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
  per phase is still ranked by contaminated audio, and the window still ends
  where the segment says, not where the point ended — measured in session #15:
  anchoring to the true rally end is worth **+14 points of precision and three
  more rallies** (oracle P 0.963 vs 0.822), and neither audio onsets, court-ROI
  motion decay, nor the engine's own segment end can reach it (segment-end
  anchoring is *worse*: 0.759). So the first vision capability to ask the
  sidecar for is **score-overlay change detection**, not stroke ownership — this
  footage's own ground truth came from those digits, so the ask is concrete and
  checkable; (b) `ranges_hit` was capped by the budget, and that cap is now
  **lifted** (session #15: `--duration` plus the phase-quota fix take a 240 s
  request from 10 clips / 8 rallies to 21 clips / 18 rallies), so the old
  "split long segments into scored windows" idea (which needed a per-window
  activity profile `event.Segment` does not carry) is no longer the lever —
  length is; **the arithmetic for the shipped 60 s default**: 473 s of rally
  time in the match bounds recall at 0.127, and the committed state measures
  0.112, i.e. 88% of what that budget allows. Decomposing the reel (inside a rally / adjacent to
  its own rally / far from any) gives 53.2 s / 6.8 s / **0.0 s** — no clip is a
  wrong pick, so what remains is boundary coarseness, which is a
  segmentation-resolution problem (vision), not a scoring one; (c) the PROVISIONAL constants were
  then swept against this manifest and **none of them binds usefully** —
  rally_pad and merge_gap are inert in rally mode, min_hits never binds below
  ~60 on this footage, `rally_chunk` measures best at its 30 s default,
  `max_clip_duration` best at its 8 s default (11 s and 14 s lose precision *and*
  distinct rallies — a longer window cannot fit a 10.5 s median rally, so it
  spills, and the budget then holds fewer clips), and
  scoring onsets corroborated by ROI motion is bit-for-bit a no-op because
  players move continuously inside a rally. The sweep and its null results are
  recorded in docs/EVAL.md so nobody re-spends the evening; the remaining error
  is not reachable by re-weighting these signals. `rally_chunk` shipped anyway
  as a default-preserving, validated, floored knob — a tuning surface for other
  sources (a broadcast with shorter dense spans), not an improvement here.
  The court ROI is per-asset (UI picker, assets.motion_roi).
- **Reel length is now a per-run choice, and that is where the coverage was
  hiding (session #15)**: `--duration` (CLI: timeline/auto/eval; API:
  `duration`; UI: "reel length (s)") overrides the style's target without
  touching the preset file, bounded 1–14400 s in `pipeline` so no client can
  disagree about the limit. Measuring it found the real defect: the diversity
  phase quota was a *fixed clip ceiling*, so a 240 s request returned the same
  10 clips / 80 s as 120 s. Windows now scale with the budget instead of the
  per-window discipline loosening, and on the same match a 240 s reel becomes
  21 clips covering 18 of 43 rallies (R 0.140 → **0.289**, F1 0.239 → **0.426**,
  P 0.886 → 0.813) while the shipped 60 s default is bit-identical. Precision
  falling with length is the trade, not an accident to tune away.
- **End-to-end reel acceptance (session #14, real match)**: `import → roi →
  analyze (29.0 s) → timeline (8 clips, 60.0 s) → render (14.2 MB in 9.3 s)`,
  output probed back at 60.02 s. The reel was then inspected frame-by-frame
  (one frame per 2 s, scoreboard crop) rather than trusted from the metric:
  every clip sits inside a single score state, the score advances only across
  clip boundaries, and the reel traverses 0:0 → 20:19 — first point to match
  point. A suspected defect (the final clip truncated to 4 s) turned out to be
  the lowest-scoring of the eight, i.e. intended budget behavior, so nothing
  was changed for it.
- **Same acceptance, longer reel (session #15)**: `xcut auto --duration 240` on
  the real match produced 18 clips / 144.0 s (rendered 34.5 MB in 16.7 s, probed
  144.02 s). Scoreboard crops at 0.3 s inside each clip's start and end read as
  a montage: the score is non-decreasing across the reel and traverses
  0:0 → 21:19, 14 clips sit inside a single score state, and the 4 that change
  do so *within the final third of a second* — the clip ends right where the
  point is decided (the final one is match point). That is the shape a highlight
  should have; a flip in a clip's middle would mean a cut through a rally, and
  none was found — with points ~10-20 s apart, an 8 s clip cannot change and
  change back. The eval harness reported 21 clips for the same 240 s request:
  that manifest case sets a court ROI per asset and this `auto` run did not, so
  the two score different motion signals. Stated rather than smoothed over —
  the numbers are not comparable across the two entry points.
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
- **The same machine's FFmpeg also lacks the `xfade` filter** (session #15,
  found by running the full suite there rather than `doctor`): 4.2.2 built
  without it, so `generic_xfade` cannot render with the distro binary. The
  render now fails with a message naming the missing filter and the two ways
  out (`generic_highlight`, or a full build) instead of "ffmpeg failed".
  Consequence for verification: **the ARM64 test suite is NOT VERIFIED** — 15+
  tests fail on that box for these two environmental reasons, not product
  ones, and closing the gap needs a stock arm64 FFmpeg on the node. The repo
  pins a checksummed Windows build for the one-click path; nothing equivalent
  exists for arm64, and downloading an unpinned binary onto the node was not
  done. Owner decision needed: pin an arm64 build for CI, or accept
  "arm64 = compile-verified + artifact smoke-tested" as the standing bar.
- serve authentication is a **static shared bearer token over cleartext HTTP**
  (D12): no TLS, one token for all clients, and no rotation surface (rotate =
  edit config + restart, which also drops every session). The failure budget
  and the session table both reset with the process, and the loopback
  exemption means any local process can still reach the API. Remote binds
  therefore belong on a trusted network or inside a tunnel. The web UI does
  work remotely (D14 sessions), but its media is served unencrypted, and a
  same-origin XSS would still read data through a session — the
  textContent-only rendering rule is what holds that line.
- The remote sign-in panel **passed its visual verification** (session #16,
  closing the item every earlier session could not because the harness viewport
  was 0×0): a real browser over a non-loopback bind with a real token, at
  1280×800 and 390×844. The modal is centered and unclipped in English and
  中文; the wrong-token error renders inside the dialog without overflow;
  sign-in proceeds to the full three-pane UI; the narrow viewport wraps without
  horizontal scroll. The pass also caught a real defect (next bullet).
- **The sign-in page could lock its own address out — fixed in session #16.**
  The UI's background health poll runs without a token, and every
  credentialless 401 used to be charged against the per-peer brute-force
  budget: leave the sign-in dialog open for five minutes and the *correct*
  token got 429 afterwards (measured live, serve.log showed the 15 s poll
  spending the budget). The budget now charges only requests that presented a
  credential; a credentialless request keeps its plain 401 without spending
  anything. Verified at the binary level: 25 credentialless polls → still 401,
  correct token → 200; 20 wrong tokens → 429 as before.
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

1. Remote-access hardening: **(a) and (b) are both done** — the runbook with
   the SSH-tunnel recipe in `docs/OPERATIONS.md`, and the sign-in panel's
   visual pass with the budget defect it caught (session #16). What remains of
   this thread is the owner-level TLS question: D12's bearer token crosses the
   wire in plaintext, so remote binds stay trusted-network/tunnel-only.
2. Real-footage evaluation — **one match is done, and that is the limit of what
   can be concluded.** 43 rallies were derived from the burned-in scoreboard and
   the provisional constants swept against them (docs/EVAL.md carries the
   negatives). What remains is not more tuning on this footage: every
   audio-driven idea dies on the same wall (shared six-court hall), and the
   ceiling arithmetic says the 60 s default is already at 88% of what its budget
   allows. The next useful measurement needs a *different* input — ideally a
   single-court recording, so "our strokes" and "the hall's strokes" stop being
   the same signal. Owner-supplied footage, or the vision sidecar.
3. Two decisions that are the owner's, not mine, and both block work:
   **(a) arm64 in CI** — Kylin's distro FFmpeg cannot run the suite (ffprobe JSON
   corruption + no `xfade`), so arm64 is compile-verified and artifact-smoke-
   tested only. Fixing it means pinning a stock arm64 build the way the Windows
   one-click pins Gyan.dev with a SHA256; that was not done unilaterally.
   **(b) release cadence** — v0.1.8-alpha shipped tonight, and the reel-length
   override plus the quota fix that makes it work are already on `main`. Whether
   that becomes v0.1.9-alpha now or rides along with the next batch is a
   packaging call (and packaging is laptop load the control plane asked to keep
   down).
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
