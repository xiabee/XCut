# XCut Project State

> The single source of truth for "what actually works right now".
> A future agent reading only this file should know the real state.

Updated: 2026-09-22 10:38 +0800 (the clock of the last recorded commit, not a wall-clock guess).
This section is a session log, read oldest first: the state that holds now is the
last paragraph before `## Version / HEAD`.

Session #18: the rally-end gap closed with imported data instead of a new
heuristic. The sidecar can now read a burned-in
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

**Same session, later still:** the client says the same thing the CLI says when a
reel came out short because the footage ran out of rallies, not because the
budget did — the document now carries `target_duration` beside
`candidate_events`/`candidate_limit`, and the timeline panel prints the sentence
(verified on the owner's match through the real path: 240 s asked → 27 clips /
216.0 s, note shown and recomputed against the served document; 60 s asked → 8
clips, note hidden because the budget cut it). Pointing the browser at a workspace that *already* had a project in it
found a worse defect, now fixed: **an existing project could not be opened at
all** after a reload — the picker button was revealed only by selecting a
project, and the list was hidden by an attribute no code cleared while the
script flipped a class the stylesheet never read. A structural gate
(`TestToggledClassesAreStyled`) now refuses a class the script switches on and
nothing styles; it was checked by renaming `.proj-menu.open` (red, naming
`open`) and it caught one more dead class on the way in. Not covered by any
test: the visibility *logic* itself (Go cannot run the script) — that rests on
the browser run above, which drove DOM clicks, not pointer hit-testing (the
connector still reports a 0×0 viewport).

**The gate caught one of my own assertions.** The first run over this work came
back red on `TestCallReturnsBeforeWorkerExits` (clean 10.96 s, hung 16.73 s
against a 5 s bound) — a load detector rather than a product failure: the
differential subtracts the spawn cost but not the kill-and-reap tail, and the
spawn is this package's own test suite. Replaced by an observation — the hung
worker's loopback listener must be gone by the time the call returns, with an
in-run control that a live listener is dialable on this host — mutation-checked,
and 7× cheaper (27.7 s → 3.9 s). The trade is written into the test: nothing now
notices the grace window itself growing, which is a PERFORMANCE.md number, not a
gate.

**A claim this session made about the footage was wrong, and the fix came out of
measuring it.** The note's story was "this match offers 21 candidate rallies";
measured with `rally_chunk` moved, the 21 was `603 s ÷ 30 s` — the slice length,
not the match (whose 43 labelled point-ended ranges were always the larger
number). Relaxing the event *floor* was measured first and rejected: identical
P/R/F1 and `dropped_min_duration=0`, i.e. the gate nobody passed. The shipped
rule narrows the slice only when the ask needs more candidates than the current
slice can supply, never widens, and stops at two clip lengths — an 8 s floor was
tried first and the suite refused it (it still split the fixture's 10 s rallies
and took rally recall to 0.455). Result: 60 s and 120 s rows unchanged to the
decimal, 240 s 21 clips/F1 0.426 → 27/0.511 and 300 s → 0.523 (docs/EVAL.md).
The configured path (the `score_roi` manifest, 44 scoreboard marks) was measured
afterwards because the change could have broken the boundary rule and did not:
240 s goes 21/0.463 → **27 clips/0.562** with precision 0.977 → 0.987 and 26 of
27 clips still ending on a scored point, while the 60 s row is bit-identical to
before it (P 0.998, 8 clips, 8/8 on a point).
The note was rewritten to stop recommending the remedy that measurement showed
does nothing, and to report the pool honestly (`offered 30 candidate rallies and
the cut took 27 of them` — the selector works through the list, it does not take
every piece of it). Acceptance for `17eb130`: local fast gate PASS (458/7),
win-devops PASS (job `20260921-213700-829911`, same 458/7), and the Linux full
leg PASS with `-race` clean and the Rust worker actually run this time
(`cargo test` rc=0, 3 passed) because the dispatch appended to `PATH` instead of
replacing it.

**The one-shot was run end to end on the owner's match** (`xcut auto … --style
badminton_highlight --duration 240 --score-crop 0.4297,0.7778,0.1406,0.0972`):
77.6 s wall on the laptop for import → analyze (44 scoreboard marks measured in
the same pass) → timeline → a 158.6 s / 39.0 MB reel, 23 clips of which 22 end on
a scored point, and it printed the footage note. That run is what surfaced two
gaps now fixed: `xcut auto` had been the one path that reported a short reel
without explaining it, and an unreadable worker answer (a mis-set
`workers.ai_bin`) named no binary. docs/PERFORMANCE.md carries the row.
Accepted at `c1af91b` on all three channels: local fast gate PASS (459 passed /
7 skipped), win-devops PASS (job `20260921-225624-b28f3e`, the same 459/7), Linux
full gate PASS with zero `DATA RACE` lines and the Rust worker run.

The follow-up hypothesis — let clips be shorter so more rallies fit the same
seconds — was measured on the marked manifest and **rejected**: at 60 s the
`max_clip_duration` arms 8/6/4 s give F1 0.225/0.219/0.224 with 8/10/15 clips,
and at 120 s the shipped 8 s wins outright (0.389 vs 0.341/0.367). The reel's
seconds are the budget, so splitting them redistributes rather than adds. No
preset changed; the table is in docs/EVAL.md.

A new eval metric, `longest missed run` (how many annotated rallies in a row the
reel did not touch at all), replaced a seconds-based first version that misled:
its 164 s figure at the 60 s default was mostly between-point dead time, only 39 s
of it rally content. Counting rallies says the default reel leaves **10
consecutive rallies** unrepresented (120 s: 4, 240 s: 2) because
`diversity.phases` caps clips per window and floors nothing. That is a product
judgement — "sample every phase" against "take the best eight wherever they fall"
— so it is recorded as an open question for the owner, with the numbers in
docs/EVAL.md, rather than tuned in passing.

Both halves of the Windows 500 fix were then accepted on all three channels at
`5c63ffe`: local fast gate PASS (464 passed / 7 skipped), win-devops PASS (job
`20260922-014911-99fb0e`, the same 464/7). The count is the evidence that the
node ran this code rather than the previous leg's: `c1af91b`, whose win-devops
leg reported 459, gained exactly five test functions between it and here — two
`internal/eval` missed-run tests and three `internal/workspace` retry tests —
and 459 + 5 is 464. The Linux full gate PASSed on a clean `git archive` snapshot
(`452 passed, 13 skipped`, zero `DATA RACE` lines, `cargo test` rc=0 with 3
passed, `not run: govulncheck`). Scope of that Linux claim, stated plainly
because the skip list is what shows it: all four retry tests are
Windows-semantics tests and skip there, so the Linux leg proves only that the
change did not break POSIX (where a rename over an open file simply succeeds) —
the 2 s sharing-class budget is guarded by the two Windows channels alone.

The gate that watches dependencies for known vulnerabilities could not fail.
`check.sh` read the scan status as `if ! govulncheck ./...; then rc=$?`, and
`$?` in that position is the status of the *negation* — always 0 — so every
non-zero result fell through to `exit 0`: the gate went green and stopped before
writing its own verdict line (the `policy-blocked` branch was unreachable the
same way). Measured on both sides of the fix on linux-ci: at `5c63ffe` a stub
returning 3 gave `gate_rc=0` with `grep -c "gate (full)"` = 0 in the log; at
`b6ba01c` a throwaway copy of the snapshot holding one package that calls
`html.Render` from `golang.org/x/net v0.12.0` made the real scanner exit 3 naming
`internal/vulncontrol/control.go:15:20`, and the gate answered `gate_rc=3` with
`== govulncheck: FAILED (rc=3)`. That exit code is measured, not remembered, and
the same fixture is the positive control that gives this repo's
`No vulnerabilities found` some meaning.

Two coverage facts fell out of the investigation. No *milestone* leg had run that
step — the control-plane local leg and win-devops both use the *fast* gate, and
the Linux full leg could not see `~/go/bin/govulncheck`, which had been installed
on the node for weeks but is not on a non-login ssh PATH. (The Windows nightly
does run a full gate, and its `.tools/bin` carries the binary, so scans have
happened there — see `docs/NIGHTLY_LOG.md`; that is a nightly pass, not the
channel every milestone closes through.) The gate now looks
where `go install` actually puts things, and the Linux verdict line reads
`not run: nothing` for the first time (452 passed / 13 skipped, zero `DATA RACE`
lines, `cargo test` 3 passed). Looking there also inverted the documented pinning
order — a `.tools/bin` stub got shadowed by the node's own binary and reported a
green gate, which is how the inversion was caught — repaired at `15a1712`, where
the stub wins again and still fails the gate with rc=3. Scope, so this entry is
not over-read: `check.sh` had no `gosec` step at all then, so static security
analysis was a Windows-nightly affair (the next entry closes that, and finds the
node had a system `gosec` waiting unused), while the Windows channel already
carries both tools in its `.tools/bin`. Those three
commits touch only `scripts/check.sh`, so no Windows leg was re-run — the Go tree
is byte-identical to `5c63ffe`, accepted in the paragraph above.

The next step was the missing half, and measuring it changed what was worth
adding. `gosec` turned out to be installed system-wide on the node already
(`/usr/local/bin/gosec`), so the only work was a step in `check.sh`; configured
as the Windows gate had it — severity HIGH *and* confidence HIGH — a scan of this
repository reports **90 findings and none in that cell**, which means the step
cannot fail for lack of a threshold. Proof, not inference: a scratch module
narrowing `uint8(v)` with no bound exits 0 under the old flags and 1 naming
`G115 … int -> uint8` under severity-only filtering. So the 14 HIGH-severity
findings were triaged first and the bar then raised in both gates (`90b7f41`):
`brandicon.ICO` no longer writes a header that lies (a 512 px image used to
return no error with dimension bytes `0,0` — measured), `Brand(1)` no longer
feeds a division-by-zero NaN into a platform-defined integer conversion,
`workspace.CleanupPartials` deletes through `os.OpenRoot` instead of a walk
resolved path (the G122 symlink-swap window), and `DiskFree` refuses a
non-positive block size rather than turning it into an enormous free-space
figure. The rest carry `#nosec G115` with the reason on the line. Accepted on all
three channels: local fast gate 466/8 PASS, win-devops job
`20260922-032420-7d7ccd` 467/7 PASS — one more passing test than the laptop,
because the symlink cases that skip under a plain developer session run there,
which is the `os.Root` cleanup behaving on a real symlink — and the Linux full
leg at `b839c95` reporting 455/13, `== gosec: clean (severity=high, any
confidence)`, `not run: nothing`, zero `DATA RACE` lines. Disclosed as required:
that win-devops snapshot was taken while one documentation sentence was still
uncommitted (it is `b839c95`, prose only, no code difference from `90b7f41`),
because I stopped the client after it had already uploaded and killing the client
does not stop the node.

Two follow-ups to that step, both small and both about trusting a green scan.
The suppression convention (`#nosec GXXX -- reason`) had nothing enforcing it, so
both gates now pass `-nosec-require-justification -nosec-require-rules`; the
control was planted rather than assumed (a package with `// #nosec G104` and one
with a bare `// #nosec` exits 1 naming `missing justification` and
`missing rule ID`, and it fires even under `-severity high`, so the rule is not
tied to the findings filter). And because `gosec --version` answers "dev" for any
build, a log could not say what produced its verdict — the gates now print the
scanner's sha256 prefix and path before running it, with the pinned install
command (`@v2.29.0`, chosen after measuring that v2.29.0 and the incumbent agree
here: both `Issues : 0`). That paid for itself immediately: the node carries two
gosec binaries, and the Linux leg at `68e13a2` is the record of which one scanned
(`== gosec scanner: ad6ae7a445f0 at /home/ci/go/bin/gosec`, the pinned copy, not
`/usr/local/bin`'s `fe2af13f1924`), with `gate (full): PASS`, `455 passed /
13 skipped`, zero `DATA RACE` lines and `not run: nothing` — the same verdict the
enforced flags passed on at `ff1b2d6`. The PowerShell side of both changes was
verified by running its commands directly, in both directions, not through a full
Windows gate on the laptop.

Coverage sweep at `5a8a4ff` (whole-repo `-coverpkg`, with FFmpeg on PATH so the
integration tests were live): 47 functions at 0%. Three had **no callers at all**
and are deleted rather than tested (`event.scoreRally`, `event.intervalMax`, the
exported-but-unused `analysis.Result.FindTrack`); removing `intervalMax` also
surfaced a doc comment describing `intervalMean` sitting above the wrong
function — residue of a mis-anchored edit, with no date attached to it — back
where it belongs now. A first pass at the HTTP layer covered two routed handlers
that no test had reached (`TestRenderDownloadHeaderCarriesNoUserBytes`,
`TestStylesEndpointListsTheEmbeddedPresets`, 0% → ~74%); the header test pins
that a project name — validated for length only, so a stored CR, LF and quote
survive creation — cannot reach `Content-Disposition` un-sanitised, and both
mutations (let a `"` through, let 13/10 through) fail it. `runFFmpeg`'s failure
branch had never run either — both existing "render failure" cases return before
FFmpeg is spawned — so the test binary now plays a chatty failing FFmpeg through
`TestMain` plus an env var, the pattern `internal/media` already uses; three
mutations were killed with the test proven to have executed (`ran=1`): dropping
the tail, taking the head instead, widening the 500-byte budget tenfold.
Accepted at `3cbf11d` on all three channels (local 469/8, win-devops job
`20260922-043143-38722e` 470/7, Linux full 458/13, pinned scanner,
`not run: nothing`, zero `DATA RACE`), and the fake-FFmpeg test was run
explicitly on the node because the gate has no per-test output — `ran=1
verdict=--- PASS`, so the mechanism holds on Windows and Linux alike.
`api.purgeLocked` came next: the auth tracker's ceiling had never executed, and
it now has a test for both halves (evict what aged out at `authMaxPeers`; when
everything in there is live, refuse to track rather than grow), killed by two
mutations — evicting nothing leaves a fresh peer untracked among 4096 expired
windows, and dropping the second cap check makes it displace live peers instead.
Accepted at `b7c268d`: local fast gate 470/8, win-devops job
`20260922-044746-e9c9a9` 471/7, Linux full gate 459/13 with the pinned scanner,
`not run: nothing` and zero `DATA RACE` lines.
The trap in that work was the fixture, not the product: the fill loop compared
against `authMaxPeers - len(g.failures)` *inside* the condition, so the target
shrank as the map grew and the fill stopped at exactly half the cap — visible
only because the assertion named the number.


`xcut eval` was reporting a number it had thrown away. The scoreboard-mark count
(`score_marks`, the field introduced so "scanned nothing" cannot read like
"scanned everything") was measured and stored, then discarded on the reel-failure
path — `evalRunCase` returned `nil, 0, err` — so a case that had found two marks
published `score_marks: 0` next to its error. Found while collapsing the two
copies of that write (`cli` had its own duplicate of the analyze fan-out's
scan-and-store, now `pipeline.ScoreScan`) and giving the eval copy its first
test; the instrumented run printed `times=[4 12] err=<nil>` while the document
said 0. Fixed and pinned by both directions: restoring `return nil, 0, err` fails
`TestEvalScoreROIMeasuresThroughTheProductionWrite`, and disabling the
missing-sidecar refusal turns the named refusal into
`analyzer_failure: cannot start worker : exec: no command`, which is
`TestEvalScoreROIRefusesWithoutSidecar`'s reason for existing. Accepted at
`c254ac0`: local 472/8, win-devops job `20260922-095335-2f8e21` 473/7, Linux full
gate 461/13 with both new tests run explicitly on the node (`--- PASS` each, not
skipped) and the pinned scanner reporting clean.


Three private copies of "keep the last N bytes of a child's output" (`render.tail`,
`analysis.tailStr`, `worker.tail`) are now one exported `media.Tail`, next to the
capture cap whose tail each of them takes. The render failure test is the guard
for all three call sites — mutating the shared helper to slice from the front
fails it (`ran=1`, named assertion) — which is the argument for one implementation
rather than three that have to be fixed in parallel. No behaviour change: the
render and subtitle excerpts are byte-identical, `worker` keeps its own
`TrimSpace` at its call site because that is presentation, not the rule. Accepted
at `763b70d`: local fast gate 472/8, win-devops job `20260922-102857-a74b0f`
473/7, Linux full gate 461/13 with `not run: nothing`, zero `DATA RACE` lines and
the tail guard plus both eval score_roi tests run explicitly (`ran=1`, each
`--- PASS`). Cross-compiled here for darwin/arm64 and linux/amd64 first, because
the change adds a `worker` → `media` import that only the build can rule on.
The Linux leg had to be dispatched twice: the first attempt's script was empty,
because cleaning this session's scratch had deleted the template the dispatch
reads — a `ssh_rc=0` with no output at all, which is what a self-inflicted
no-op looks like from the client side.

Three of the sweep's real gaps are closed at `5d2eaad` and two dead twins are
gone: `api`'s save response now has to report the clip count the UI prints
(`save reported clips=0, want 1` under mutation), `worker.stderrTail` is reached
by a new stub mode (a worker answering an empty envelope, exiting 1 and flooding
stderr) whose test and the render test both fail on one mutation of `media.Tail`
to the head — the consolidation paying for itself — and `BrandICO`, the icon the
Windows binary embeds, is parsed for magic, count, sizes and contiguous offsets.
`pipeline.countClips` and `storage.TouchProject` had zero callers and were
deleted. Accepted: local 474/8, win-devops job `20260922-121047-5fc392` 475/7,
and the re-run sweep reporting **40** functions at 0.0% against 46 before with
statement coverage 79.0% → 79.3% — measured by running it, not inferred from how
many files changed.

**Session #20 opens Phase 5** (the owner's directive: keep pushing auto-editing —
new styles, camera motion, beat-synced music, UI and resource work). B1 is the
beat grid the `卡点` cut needs: `analysis.EstimateBeatGrid` folds an onset track
to one phase, keeps the *longest* period explaining ≥90% of the onsets, refines it
by two least-squares passes over the integer beat index, and stops at the last
onset plus half a period instead of at the requested horizon. It is deliberately a
derivation, not a fourth `FeatureTrack` and not a cache entry — from the cached
onset track it costs microseconds, so a cache would be one more thing to
invalidate for no measurable gain; the wiring is B2's. Six tests: five on the
estimator (click grid, ±30 ms jitter, ≤3 onsets refused, every-other-click,
horizon clamp) and one through the real chain — `testmedia.GenerateRally`
clicks at 0.5 s → the shipped `AudioOnsetAnalyzer` → the estimator, reporting
`period=0.4999 bpm=120.0 coverage=1.00 beats=24 onsets=23` (0.02% period error).
Four mutations each named the assertion they broke (`ran=6` per replay, every run
a test failure rather than a build failure): the 4-onset floor →
`[1 1.5] produced a grid (period 0.5000, 4 beats); want a refusal`;
shortest-instead-of-longest → `period = 0.2500, want 0.5000 within 5%` (and the
real chain reporting 0.2499 for a 0.5 s click track); dropping the refinement →
`beats = 12, want one per second across 13 s`; trusting the horizon instead of
the last onset → `grid extrapolates past the last onset: last beat 12.000, last
onset 11.500`.

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

- **The Windows job-object memory-cap test is closed, and this is the record of
  what its failures were.** Three win-devops runs on 2026-09-21 morning went red
  in `internal/media`: jobs `20260921-033656-2fd56e` and `20260921-035742-63824e`
  hit `panic: test timed out after 10m0s` (606.8 s and 603.1 s for the package),
  and `20260921-043220-d9f236` failed as
  `TestJobObjectMemoryCapKillsRunawayChild (60.09s)`. Those were the old test —
  it waited on the child without a bound and treated "stalled at the cap" as
  "cap failed". The current `TestJobObjectMemoryCapStopsRunawayChild` bounds the
  wait at 60 s, accepts a stall as the cap binding, and names the only shape that
  is genuinely uncapped (all 24 × 64 MB blocks landed). Evidence it is settled:
  37 of the 42 XCut jobs visible on the node are PASS and every run after the
  hardening is among them — nothing has recurred. Read this entry before opening
  a new investigation: a load-sensitive Windows test that has already been made
  deterministic does not need a second one. Scope of the claim: the hardening
  landed at 2026-09-21 04:38 (`6d65321`), and no `internal/media` failure has
  appeared in the node's 42 visible jobs since — the one later red,
  `20260921-173929-cfff4f`, is the API 500 in the next entry, a different package.

- **The 500 on the Windows CI node is reproduced, root-caused and closed.**
  `api/TestTimelineRegenAndPutRevisionUniqueness` failed on win-devops at
  `984a1d4` (job `20260921-173929-cfff4f`) reporting
  `unexpected PUT status 500`, and stayed green through 40+ local runs — so it
  sat open as "unreproduced" until a gate run at `d811ac2` reproduced it locally,
  which in turn exposed the reason the report was misleading: **that branch of
  the test fails on its `GET`, and stored only the status**, so the message
  blamed a PUT and its detail was empty. Two things were true and neither had
  been looked at:
  - reading a document whose name is being replaced answers
    `ERROR_ACCESS_DENIED` on Windows, so `timeline.LoadFile` could return
    `cannot access timeline file` → 500 for a plain GET; and
  - publishing is the mirror case — a rename over a destination a reader still
    holds fails the same way, and the retry window was ~420 ms of escalating
    sleeps, which a Defender scan or four concurrent savers can outlast.
  Both now wait the same explicit 2 s through `workspace.RetryTransient`, which
  retries *only* the sharing class of error: a missing file is still a fast
  NotFound (`TestRetryTransientWaitsOnlyForSharingConflicts` fails with "8
  attempts in 2.54s" the moment the predicate is widened to
  `ERROR_FILE_NOT_FOUND`), and the give-up stays bounded with the source file
  intact (`TestRetryableRenameWaitsOutABriefHolder` reproduces the old rename
  window verbatim: `Access is denied. (waited 627ms)` against a holder releasing
  at 900 ms). `RetryableReplace` (delete, then rename) remains deliberately
  unused for the document — that trades revision integrity for the same
  convenience, as `workspace/rename.go` records. The test now names which verb
  failed and keeps its response body either way, so a future report cannot
  misdirect the search the way this one did.
- **The Linux-leg `DATA RACE` is reproduced, root-caused and fixed (closed).** It
  fired again at `680d707`, this time with both halves of the report: two worker
  goroutines in `analyzeBody`'s per-asset fan-out were calling the analyze
  callback at the same moment, and `cli.cmdAnalyze`'s callback writes a
  multi-line block per asset to one shared writer (`a.Stdout`) — a
  `bytes.Buffer` in tests, so the race was on the buffer itself. Neither of the
  two shapes this entry suspected (a writer outliving `Run`, the e2e's reused
  buffers) was involved. The fan-out's own comment claimed "errors and callbacks
  are marshalled back to this goroutine", which the code never did.
  Fixed at the layer that creates the concurrency: the pipeline serialises
  `onAsset` under a mutex, so any caller may write to one stream.
  The detector is a counted overlap check in
  `TestAnalyzeProjectParallelMultiAsset` — the callback asserts it is alone and
  yields inside the region, so the violation is observable rather than lucky:
  deleting the mutex fails locally (`onAsset ran concurrently on 2 assets`),
  with it the test is green. Note why it is not a `-race` hope: the same
  mutation stayed invisible through 8 local `-race` runs of the e2e that
  originally caught it, while the node caught it on its first pass — load on
  4 cores is part of the trigger, so the only honest local guarantee is an
  assertion that measures overlap directly. Closed on the same channel that
  opened it: the Linux full leg at `aef2ace` reports `gate (full): PASS` with
  zero `DATA RACE` lines (it failed with two at `680d707`), and win-devops
  passed the same sha (job `20260921-202622-d0e4e2`, 457 passed / 7 skipped).
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

1. Live paths still without a test, from the coverage sweep recorded in the
   session log is closed: `scripts/cover-sweep.sh` (max-merge across 22 test
   binaries) shows those wrappers were a *measurement* artifact — merged profiles
   report the last writer's count per block, so code covered only by another
   package's tests reads 0.0%. `pipeline.AnalyzeProjectAsync`, the loudest of
   them, is 100% when profiled through its own consumer (`internal/api`,
   `TestAsyncJobFlow` posting `/analyze`). Remaining 0.0% entries after that
   correction: 46, of which the ones still worth work are the CLI command
   (`cmdVersion`, `cmdConfig`, `usage`) and serve-lifecycle
   (`startServeCore`, `shutdownServe`, `newServeLogger`) paths, and the
   environment-conditional ones (`analysis.RustAudioAnalyzer.*` need the built
   Rust worker; `setup.*` runs only on Windows). Platform stubs
   (`*_other.go`) and interface shims (`Error`, `String`, `Name`) are not gaps.

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

4. Subtitles with a real Whisper: install faster-whisper locally and run
   `xcut subtitles` on real singing content (the plumbing is tested; the
   model load is deliberately not night work).

5. Re-enable push/PR + tag CI when the GitHub account billing issue is
   resolved (Actions jobs are refused at start; restore notes in
   ci.yml/release.yml unchanged — the files are fine).

6. Phase 4 leftovers: tray/auto-update and model registry — need
   maintainer decisions; desktop packaging itself (zip, icons) shipped
   in session #8 and the true installer in session #13.

7. TLS for the API, or an explicit statement that the tunnel recipe in
   `docs/OPERATIONS.md` is the answer: D12's bearer token crosses the wire in
   plaintext, which is why remote binds are documented as trusted-network or
   tunnel-only today. Owner-level call, and the only survivor of the old
   remote-access thread.

