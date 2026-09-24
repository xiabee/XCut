# XCut Project State

> The single source of truth for "what actually works right now".
> A future agent reading only this file should know the real state.

Updated: 2026-09-24 01:27 +0800 (the clock of the last recorded commit, not a wall-clock guess).
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
onset 11.500`. Accepted at `fbe5ab4`: control-plane local gate 480/8 with
`steps not run: none` (recorded as `local CI PASS at 12:42:25 for fbe5ab48`),
win-devops `OVERALL PASS` for that same head (`policy: after_local_pass`,
`exit=0 duration=1m39.38s` — the node reports exit and duration, not counts, so
no count is claimed for it), and the Linux full gate 469/13 with
`not run: nothing`, zero `DATA RACE` lines, gosec clean on scanner `ad6ae7a445f0`
and the six beat-grid tests run explicitly on the node (`ran=6 skipped=0`, the
real-chain line reproduced there as `period=0.4999 … onsets=23`).

**Same session, later:** the owner answered the two open product questions
(defaults may be raised; the floor follows best practice), and answering them
corrected a third thing I had written down. `diversity.min_per_window` now exists
— each window's first clip before any window takes a second — because the ceiling
alone genuinely permits one: `TestPhaseFloorTakesAnEmptyWindowBeforeDoublingUp`
builds a source where a five-clip reel puts two in each of two windows and nothing
in the other three, and the same reel with the floor covers all five. Inventing
that fixture first was the right call, because **on the real match the floor is
almost nothing**: the 120/180/240 s reels come out the same selection byte for
byte, and the 60 s reel keeps its eight rallies with two different trims (the
budget reaches them in another order: `344.0..352.0` → `345.8..352.0`,
`573.5..577.5` → `571.7..577.5`, total still 60.00 s), whose longer last head
touches one more rally — 7/43 → 8/43 at unchanged precision.
So the sentence in `docs/EVAL.md` that blamed the ten-rally missed run on "nothing
floors it" was wrong — the ceiling had already spread those picks, and the missing
rallies sit *inside* windows that do have a clip. The full ladder was re-measured
at this commit (60/120/180/240 s → F1 0.225/0.389/0.455/0.562, missed run
10/4/2/2, precision flat at 0.97–0.99 the whole way), which is also what the
default raise rests on: **`badminton_highlight` now asks for 120 s**, where the
match's own rally arithmetic allows 0.254 recall and the reel delivers 0.243
(96% of the budget, against 88% at 60 s), and the render cost of exactly that
length was measured in session #18 (17.8 s wall for 105.7 s out, 0.17x). The knob
is inert by default — `min_per_window` unset means today's ranking byte-for-byte,
which two pre-existing tests (`TestPhaseQuotaScalesWithReelBudget`,
`TestBuildWindowCapSpreadsPicks`) found out immediately when I first wired the
filter into the single-pass path: my condition had also gated it on
`pass == 0`, and the one-pass case *is* pass 0, so the floorless reel rejected
every event. Generic presets keep their 45 s ask: nothing annotated
in generic content has been measured, and "the sports ladder says longer" is not
evidence about a drill montage. Accepted at `69a1ad6` (the floor in `4357497`, the
preset and these docs in `69a1ad6`): control-plane local gate 481/8 with
`steps not run: none` (`local CI PASS at 13:12:45 for 69a1ad60`), win-devops
`OVERALL PASS` for that head (`exit=0 duration=1m51.309s`, counts not reported by
the node), Linux full gate 470/13 with `not run: nothing`, zero `DATA RACE` lines,
gosec clean on `ad6ae7a445f0`, and both new suites run explicitly on the node
(`floor_rc=0 ran=1`, `beats_rc=0 ran=6`).

**Same session, later:** B2 gave the beat grid its consumer — `卡点` snapping, with
`beat_snap_tolerance` on the preset and `--beat-snap seconds|off` / `beat_snap` on
the surface — and the measurement came back **inert on the flagship footage, for two
different and both good reasons**. On the marked manifest all 16 of the 120 s reel's
ends are already pinned to a measured point, and a beat does not get to move one
(precedence was written that way on purpose; the mutation that lets the grid win
fails with `boundary-pinned end moved to 13.45`). On the unmarked manifest the ends
are free and still nothing moves, because the hall's **1493 onsets carry no grid the
estimator will believe** — no period in 30–300 BPM explains ≥90% of crowd noise plus
shuttle contact, which is what B1's refusal rule exists for. Three eval runs
(off / 0.12 / 0.25) return F1 0.389 marked and 0.330 unmarked to the digit. What
that buys is the finding that reorders B4: a reel cuts to the music laid under it,
not to its own location audio, so the next slice starts by importing a **music bed**
and taking the grid from *that* file. The mechanism itself is proven where a grid
does exist: `TestBeatSnappingThroughTheRealAnalysisPath` asks for the same 7.7 s
reel off and on through the shipped analyzer and cache, controls that the ends start
off the lattice, and requires every one to finish on it. Two process notes: the API
boundary test caught `TimelineRequest.validate` returning early on an unset
duration, which had skipped every later check — `{"beat_snap": 2}` queued a job the
preset would have refused; and the click fixture first proved nothing at all,
because a round 8 s ask on a 0.5 s lattice puts the reel's only end *on* a beat, so
a rule that never ran looked green — the odd ask and the off-lattice control are
what make it bite. Accepted at `5b8c7d7` (code `6fc0d93`, docs `9b943e4`+`5b8c7d7`):
local gate 488/8 with `steps not run: none` (`local CI PASS at 14:06:48 for
5b8c7d79`), win-devops `OVERALL PASS` at that head (`exit=0 duration=1m33.24s`),
Linux full gate 477/13 with `not run: nothing`, zero `DATA RACE` lines, gosec clean
on `ad6ae7a445f0` — and there the snap suite really executed rather than skipped
(`snap_rc=0 ran=6 skipped=0`, logging `1 of 1 ends moved onto the grid`), which is
the cross-platform half of the proof.

**Same session, later still:** B3 shipped the framing half of 运镜 — a per-clip
plan in the IR (`{"motion":{zoom,from,to}}`), the renderer cropping to it, and the
style policy `camera_motion` (`punch_in`/`drift`/`roi`). The proof is three-level on
purpose (text, argv read back from the child through `Render()`, then pixels:
YAVG 94.17 → 19.24 as a window drifts off the only lit quadrant, 39.25 → 39.36 with
no plan), because a filter string is not a picture. Cost recorded in
`docs/PERFORMANCE.md`: 8.6 s against 8.1 s per 60 s of output, +11.5% bytes — the
size is the number to watch, not the wall time. Two FFmpeg details are now
comments where they were found: crop's x/y expressions have no `w`/`h` (the window
is `ow`/`oh`, and asking for the other thing fails at configure time), and a
`metadata=print:file=` target cannot be a Windows path because `:` splits filter
options — `file=-` writes the stats to stdout. Nothing is enabled by default and
that is a decision, not an omission: cropping a broadcast can cut the score bug out
of the shot, and no aesthetic claim has been measured to trade against it. And what
the plan does *not* promise is written into `docs/ROADMAP.md` rather than implied —
centering a window on the ROI keeps its *center* there, because only the renderer
knows the source's pixel aspect. One gate observation, recorded rather than tuned
away: the first `go test ./internal/...` after the render tests landed failed
`api/TestSubtitlesFlow` at its 30 s cold-sidecar bound, while the same test passes
in 0.45 s alone and a four-package concurrent rerun is green — new concurrent ffmpeg
load met a known slow path. If it recurs it is the test's shape to fix, not the
deadline to raise. Accepted at `ed0b0c9` (IR+renderer in `948cc2e`, policy in
`ed0b0c9`): local gate 497/8 with `steps not run: none` (`local CI PASS at
15:17:24 for ed0b0c93`), win-devops `OVERALL PASS` at that head
(`exit=0 duration=1m48.015s`), Linux full gate 486/13 with `not run: nothing`,
zero `DATA RACE` lines, gosec clean on `ad6ae7a445f0` — and the three framing
suites run explicitly on the node, so the pixel proof is not a Windows-only one
(`2 clips framed on the drawn region's center`, `drift YAVG 94.17 → 19.24 (20%);
still 39.25 → 39.36`).

**Same session, later still:** B4a gave 卡点 the input B2 proved it needs — a
**music bed** (`--music`, `"music"` on the API). The pipeline analyzes the named
file with the shipped analyzer through the same cache, estimates *its* grid, and
that grid outranks the location audio's; naming a bed implies the product's
±0.12 s snap (`--beat-snap off` keeps the music but not the moving), the render
loops the track and mixes it at recorded levels (0.9 / 0.35 by default, both in
(0,1], video stream copied so a bed costs no second encode), and the timeline
document carries the choice (`music`, `music_gain`, `source_gain`, `music_bpm`) so
`xcut render` is not told twice — a document whose track moved is a refused
render, not a silent musicless one. Measured on synthetic truth: footage clicking
at 0.4 s under a 0.5 s bed, its one free end moved onto the **bed's** lattice
(`1/1 moved, 1 on the bed grid, 0 on the footage's`, `music_bpm 120.04`); and the
mix is audible in the rendered file itself — **9 transients only the bed explains
against 0** in the same cut rendered bedless, duration identical. Cost: 11.3 s
against 10.2 s per minute of output, +3.3% bytes.
Two lessons from building it, both now comments where they were found: a mix of
two metronomes leaves **no** single grid in the output (the footage's 0.4 s and the
bed's 0.5 s are both there), so the "the rendered file's pulse is the bed's"
assertion I first wrote was unmeasurable on this fixture and was replaced by the
transient-fingerprint claim above; and `amix` at the default mix made the file
*quieter* (−34.3 dB against −32.9), which is why the promise is "the bed is
present and audible", not "louder". A mutation harness bug worth remembering:
backing up `internal/pipeline/music.go` and `internal/render/music.go` under their
basename meant the second copy overwrote the first, so the "restore" installed the
render package into the pipeline package and every later case reported
`INVALID(build failed)` while the harness's own `cmp` said everything was
byte-identical. Accepted at `930b49b`: control-plane local gate 505 passed / 8
skipped with `steps not run: none` (`local CI PASS at 16:56:42 for 930b49b2`),
win-devops `OVERALL PASS` at that head (`exit=0 duration=1m39.735s`), Linux full
gate 494/13 with `not run: nothing`, zero `DATA RACE` lines, and the four
bed/mix/grid/snap suites run explicitly on the node (`ran=3/3/6/6`, `skipped=0` in
each). The node's numbers are a little different from the laptop's and are recorded
as the range they are: **8** bed-unique transients against 0 (not 9), mixed −34.4 dB
against bare −33.0 dB (not −34.3/−32.9). The assertion is "more than none", so both
builds satisfy it; the exact count belongs to whichever ffmpeg measured it.

**Same session, later still:** B4b built the measuring stick before the next cut —
a **pacing readout** (`timeline.Pacing`, printed by `xcut timeline` and `xcut auto`)
that reports shot count, mean/median/longest played shot, and where the reel's
top-scored shot begins. The reason it exists is that every metric in the harness is
set-based, so a 15 s stretch and five 3 s cuts score identically; without this line,
"new style, same F1" cannot be told apart from "new style, different edit". Two
choices are load-bearing and each is pinned by a test: the hook is the shot's
**output** position, because "when was it filmed" is a different question, and a
score that does not parse is not read as zero, because a typo must not outrank a
real 0.4 — nor may an unscored, hand-edited document be handed a top shot it never
claimed. Verified on 7 tests over real documents (equal-length control; a half-speed
clip so source span ≠ played span; an out-of-order score tie; the empty and unscored
cases) and 7 mutations, every one killed by the assertion it targets
(`median = 6, want 3.5`, `HookSeconds = 900, want 200`, `ScoredShots = 3, want 1`,
and the CLI's `pacing line … claims a top shot the document never scored`).
What the readout says about the shipped preset, measured on the owner's match:
`14 shots, mean 8.0s, median 8.0s, longest 8.0s, top shot starts at 24.0s` at the
120 s default, and `8 shots, mean 7.5s, median 8.0s, longest 8.0s, top shot starts
at 16.0s` when asked for 60 s. Mean equals median equals longest equals the preset's
`max_clip_duration`: **every shot sits on the ceiling**, so the ceiling — not the
scoring — is what sets the pace, and the best moment arrives a quarter of the reel
in. Those two numbers are B4c's acceptance targets, recorded before anyone chose a
preset value to hit them. Accepted at `0c79ab6`: control-plane local gate 515 passed
/ 8 skipped with `steps not run: none` (`local CI PASS at 17:28:02 for 0c79ab62`),
win-devops `OVERALL PASS` at that head (`exit=0 duration=1m48.695s`), Linux full gate
504/13 with `not run: nothing`, zero `DATA RACE` lines, and both new suites run
explicitly on the node (`ran=7 skipped=0` for the measurement, `ran=3 skipped=0`
for the printed line) — the readout is not a Windows-only claim. GitHub's port 22
refused the push during this milestone; it went over `ssh.github.com:443` after
comparing the three host keys against the `github.com` entries already in
`known_hosts` (byte-identical, same fingerprint set).

**Same session, later still:** B4c cut two presets on the ruler B4b built, and one
new IR knob to make the second one meaningful: `clip_order`
(`chronological` — the unset value, so no existing preset changes — or
`hook_first`, which moves the style's own top-scored shot to the front and leaves
the rest in match order; an unrecognised name is refused at load because a typo
would otherwise keep playing the old order while promising a hook).
`sports_vertical` (9:16, `max_clip_duration` 4.0, `roi` framing) and
`beat_shortform` (1080×1920, 1.0–2.8 s shots, ±0.12 s snap, hook first) are
embedded, so `GET /api/v1/styles` and the UI picker list them without a hand-kept
registry.

Measured on the owner's match, both styles at the *same* 60 s budget as the
incumbent (`docs/EVAL.md` has the table): 15 clips against 8, recall 0.100 against
0.108, precision 0.785 against 0.850, and the **longest run of untouched annotated
rallies 4 instead of 10** — the axis that moved in the new preset's favour. Its
`ranges_hit` fell (4 vs 6) and that is the yardstick, not the edit: a hit needs IoU
≥ 0.3 against a rally, and a shot inside one scores `len(shot)/len(rally)` — 4 s in
15 s is 0.267, under the bar, where 8 s is 0.533. Per the rule written before this
was built, **a preset that does not beat the incumbent on the annotated case does
not become the default**, and it did not: `badminton_highlight` stays the shipped
sports style, `sports_vertical` is an opt-in shape.

Two claims were *not* made, and why. The pacing line for the vertical preset on the
workspace at hand reads `14 shots, mean 4.0s, median 4.0s, longest 4.0s, top shot
starts at 12.0s` — half the incumbent's 8.0 s, which B4b identified as the only
thing setting pace — but **the saturation pattern persisted**: mean = median =
longest = the new ceiling. Halving the ceiling halves the reel's shots; it does not
diversify them. And `beat_shortform` cannot be shown on any footage in this
repository's possession: its activity-mode config sees one continuous span on a
fixed broadcast camera (`chunks_considered=0 … segments=1`; the shipped
`generic_highlight` does the same here, so this is the content, not the preset), and
its eval row is 0.000 with one clip for exactly that reason. Its shape is
demonstrated on constructed segments, with 7 mutations — hook never moves, tail
re-sorted by score, `clip_order` accepting anything, ceiling 4→9, motion mode
dropped, hook order removed, a preset vanishing from the embedded list — each killed
by the named assertion (`hook_first started at 2, want … [2 14 26]`, `longest shot
is 9.000s; the preset promises a 4.0s ceiling`). The missing input is a multi-cut,
moving source, which is the same gap as the standing question about a second match.

One investigation closed as *not* a defect, recorded because the log line reads like
one: `xcut analyze` on a 105 s rendered reel reported `onsets=0`. Onsets are
computed per preset config at build time — the same asset yields `onsets=282` under
`sports_vertical` (rally mode) and none under an activity-mode preset that has no
use for them. Reading `onsets=0` from an analyze-stage line as "the audio has no
transients" would have been wrong; the file's own mean level is −27.9 dB.
Accepted at `cbd4ee7`: control-plane local gate 522 passed / 8 skipped with
`steps not run: none` (`local CI PASS at 18:07:21 for cbd4ee78`), win-devops
`OVERALL PASS` at that head (`exit=0 duration=1m51.686s`), Linux full gate 511/13
with `not run: nothing`, zero `DATA RACE` lines, and the new suites run explicitly
on the node (`order ran=4`, `presets+embed+pacing ran=15`, `skipped=0` in both) —
the preset files ship in the embed, so Linux validates them too.

**B6a (first slice of the UI phase): the reel's shape is in the web UI.** The
timeline panel's pacing chip reads a derived `pacing` object that
`GET /api/v1/projects/{id}/timeline` now computes with `timeline.Pacing` — the same
function behind the CLI line — so the two readers cannot disagree about one
document; the PUT path takes a bare document and refuses the envelope, so a client
that echoes the response back cannot smuggle the derived field into storage. The
chip describes the *saved* document (the strip above it holds unsaved trims), hides
when there are no shots, and says "no shot is scored in this document" rather than
inventing a best shot at 0.0 s. Wire keys are pinned in Go (`shots`,
`mean_seconds`, `median_seconds`, `longest_seconds`, `scored_shots`,
`hook_seconds`) and on the script side (`app.js` must name each field it reads), so
a rename at either end fails a gate instead of blanking the chip; five controls
prove it: dropping the envelope field, renaming a JSON tag, typo-ing the element id,
drifting one i18n key, and renaming a field in the script each failed exactly one
named test (`app.js reaches for 1 id(s) no markup defines:
tl-pacing`, `pacing is missing the "hook_seconds" key`). Browser evidence, performed
by hand and **not repeatable in CI**: on the owner's match project the chip rendered
`8 个镜头 · 平均 7.5 秒 · 中位 8.0 秒 · 最长 8.0 秒 · 评分最高的镜头从第 16.0 秒开始`
with `display: block`, matching `pacing: 8 shots, mean 7.5s, median 8.0s, longest
8.0s, top shot starts at 16.0s` from the same document; it rendered again after a
reload and re-selecting the project; and after a save that stripped every score it
switched to the unscored sentence while keeping the lengths. Two traps met on the
way, both mine: a `xcut timeline` "restore" run silently did nothing because
`serve` held the workspace lock and the failure line was grep'd away, and a copy in
an edit changed a neighbouring translated sentence that a Go test pins — the second
was caught by diffing, the first only by looking at the output instead of the exit
code. Accepted at `4873320`: control-plane local gate 525 passed / 8 skipped with
`steps not run: none`, win-devops `OVERALL PASS` at that head
(`exit=0 duration=1m48.421s`, `local CI PASS at 18:38:13 for 48733204`), Linux full
gate 514/13 with `not run: nothing`, zero `DATA RACE` lines, and the two-ended wire
pins plus the asset guards run explicitly on the node (`ui_pacing ran=5`,
`shape+pacing+order ran=17`, `skipped=0` in both).

**B6b (UI): the bed and the beat, where they belong — the request form.** The
pipeline panel now has a **music bed** path field and a **cut on the beat** selector
(`style's own` / `±0.12 s (product default)` / `off — keep the music, move nothing`),
and `timelineRequest()` sends `music` and `beat_snap` only when the user chose them,
so "absent", "0.12" and "−1 (off)" stay the three different requests the API already
distinguishes — verified in the page itself, by calling the shipping function:
`{style, duration:60, music:"…bed60.m4a", beat_snap:0.12}` → `beat_snap:-1` with the
off option → `{style, duration:60}` with both left alone. Below the timeline, a
second line states what the *saved document* chose, in the four cases that mean
different things: a bed whose beat the cuts followed (`背景音乐「click20.wav」120.00 BPM
· 1/8 个剪辑点落在它的节拍上`), a bed whose beat was measured but moved nothing, a bed
whose audio held no believable grid, and no bed with snapping driven by the source's
own pulse. The path is echoed as a file *name*, never as the caller's path. The four
keys the note reads (`md.music`, `md.music_bpm`, `c.metadata.beat`, and the two
request fields) are pinned in Go on both sides, the same way the pacing chip is.

### Beat grid: the bed-length defect, how it was found (now fixed — see B1b below)

Found by using B4a's own feature at product scale, through the UI. Two synthetic
click beds, one tempo, two lengths:

- `click20.wav` (20 s of exact 0.5 s clicks): `bpm=120.00 coverage=1 beats=39
  onsets=39` → accepted, and the reel snapped (1 of 8 ends moved).
- `click120.wav` (60 s of the *same* 0.5 s clicks): `the music bed carries no beat
  grid the estimator will believe … onsets=119` → **refused**. A perfect metronome the
  estimator calls unbelievable.

A scratch matrix over `EstimateBeatGrid` (run, then deleted — the numbers are the
record) places the boundary by onset count, not by span: a 0.5 s lattice is accepted
at 6, 8, 10, 20, 30, 40 and 60 onsets and refused at 119; 119 onsets at 0.4 s are
accepted; 119 at 0.25 s are refused. And a second failure mode is worse than refusal:
119 onsets at 1.0 s returned `period=0.2 bpm=300 coverage=1.000 beats=591` — the
"longest period that explains the onsets" rule collapsed onto the ladder's own start
value, which is a **confidently wrong grid**, not an honest refusal.

The mechanism the numbers point at (to be tested, not assumed, when fixing): coverage
is judged on the *unrefined* candidate period. The candidate ladder multiplies by 2%
from 0.2 s, so a true period usually sits between two rungs with ~1% error, and that
error accumulates per beat — around ±48 beats from the phase centre before onsets fall
outside `beatTolerance`. Short files stay inside it; long ones do not, so long grids
are refused, and the only long-span grids that survive are the ones whose period
happens to land exactly on a rung (0.4, 0.2) — which is also why the 1.0 s case fell
back to 0.2. The least-squares refinement that would fix the period runs *after* the
verdict (beats.go: `bestPhase` → coverage gate → `refineGrid`), so it never gets to
save a candidate the ladder mis-fitted. The fix is to judge the candidate by its
refined coverage; the regression must include the 119-onset lattice (refusal) and the
1.0 s lattice (mis-fit), because they are different symptoms of one cause.

That was the state of `--music` on a real 3-minute pop track before B1b: the bed is
analyzed over its own length, and 3 minutes is 360 beats.

**B1b — that issue is fixed, and the estimator is now a different shape.** The
candidate ladder is gone; one source remains, anchored on the ends of the evidence —
for every interval count `n`, the period is `(last − first)/n`, fitted to the onsets
by the existing least-squares pass, and judged by a cluster window over the sorted
residues. Two of those words are the load-bearing parts, and each is a test:

- The **verdict is taken after the fit.** A 2% ladder never sits on the true period,
  and a ~1% error is not rounding: it moves the phase out from under a fold by about
  the hundredth beat, so a perfect minute of metronome folded to 0.80 and was called
  disbelief. Measured over every whole BPM from 30 to 300 on a 60 s click lattice:
  **before, 121 believed / 142 refused / 8 wrong; after, 271 believed / 0 refused /
  0 wrong**, and every period exact (worst relative error 0.0000%).
- The **phase is anchored on a real onset, not on the least-squares mean.** That
  detail came from my own first attempt: with the fit's mean phase, a lattice whose
  clicks alternate either side of the true beat puts every click at *precisely* the
  tolerance distance, so a grid twice as slow as the music scored `coverage=1.000`
  and won the longest-wins rule. Measured then: 119 clicks at 0.5 s returned
  `period=1.00000000 phase=0.250000 cov=1.0000`. `TestBeatGridDoesNotScoreBetweenTheBeats`
  is that case, and it is why `bestPhase` still picks the phase.

Product-level re-measure, same three beds as the discovery above: `click120.wav` now
reports `bpm=120.00 coverage=1 beats=119` and `cuts on the beat: 1 of 8 clips`; a
three-minute click bed (359 onsets) reports `bpm=120.0 coverage=1 beats=359`; and
`bed60.m4a` — the match's own hall audio, 145 onsets — **is still refused**, which is
the answer that was already correct. `TestBeatGridStillRefusesALongIrregularTrain`
holds the same line at unit level: 79 irregular onsets across 60 s produce no grid.

Two more things the rewrite had to earn, both measured rather than assumed. The
first version of the new scan was **cubic** — 120 onsets cost 54 ms, 1 440 cost 2 m
7 s, and 3 600 did not finish inside a 600 s test timeout — which is rule 4 of
`AGENTS.md` biting, not a style preference: the cluster search was quadratic per
candidate. Sliding the window over sorted residues made it `O(n log n)`, and the fit
now reads at most `maxBeatFitOnsets` = 1 200 onsets and projects the grid over the
rest, so 20 000 onsets cost **127 ms** (44.7 s with the budget removed) and the
271-tempo sweep costs 0.63 s instead of 57 s. `TestBeatGridFitsAPrefixAndBeatsTheWholeBed`
says the cap decides the *fit*, never the beats: a 2 000-click bed still gets beats to
its last click. And `TestBeatGridLeastSquaresSharpensALongJitteredBed` exists because
the fit otherwise had no witness — with ±90 ms of spread the anchored span alone lands
at 0.499872 while the fit lands at 0.499977, so the threshold sits between those two
numbers. Seven mutations, each killed by a named assertion (the refusal, the midway
phase, the longest rule, the coverage floor, the tail clamp, the fit, the circle-wrap
in the window); the fit budget is the one part no test can observe — it changes cost,
not output — so it is carried by the timings above instead of a fake assertion.

The ladder's own removal was decided the same way: after the new scan landed, mutating
the ladder's longest-rule and its refinement **survived** the whole suite, and running
the suite and the sweep with the ladder disabled gave identical numbers (271/0/0, the
jitter cases the same to six decimals, the spurious-trailing-click case the same
1.003877) while halving the long-lattice test's time. Code that cannot be shown to
matter does not stay. Accepted at `4f305c7`: control-plane local gate 531 passed / 8
skipped with `steps not run: none` (`local CI PASS at 20:52:06 for 4f305c7a`),
win-devops `OVERALL PASS` at that head (`exit=0 duration=1m50.137s`), Linux full gate
520/13 with `not run: nothing`, zero `DATA RACE` lines, and the suites run explicitly
on the node (`TestBeatGrid ran=12`, the bed and snap suites `ran=9`, `skipped=0` in
both) — so the race detector saw the new scan too.

**B5a (captions, first slice): the style is resolved against the reel's frame.** A
`.ass` declares a reference box and libass scales everything by it, so a file that
says 1280×720 puts its text at the size and height of a horizontal frame even when
the reel is 9:16 — same characters, wrong picture. `subs.KaraokeStyle` gained the
canvas (`Width`/`Height`; unset keeps the shipped reference, so an old caller sees no
change), and every pixel metric is now derived from it: font, outline, shadow and side
margins by the short side (they are about glyph size), the bottom margin by the height
(it is an offset from the bottom edge of *this* frame). So 1080×1920 gets
`PlayResX: 1080 / PlayResY: 1920 / Fontsize 72 / MarginV 107` while 720p keeps 48/40
to the digit, and a 256×144 frame still gets a 1-pixel stroke rather than the 0 that
rounding would give — the outline is the only thing keeping white text readable over a
bright frame. The transcript stage reads the project's own timeline for that canvas, so
the file is styled for the reel it will be burned onto. Proven at both levels: a unit
test over the writer's `Style:` line and an end-to-end one (`TestSubtitlesFlow` puts a
vertical timeline in the project *before* transcribing and reads the served `.ass`'s
PlayRes back). Five mutations each killed exactly one assertion, including
`terr != nil` in the caller — "the wiring never looked at the canvas" — which the unit
tests cannot see and the chain test catches (it failed with the 1280×720 header in the
error text, not with a panic).

Two honest remainders from this slice, both in `docs/ROADMAP.md`: the geometry is
decided when the transcript runs, and the transcript payload is not stored, so
switching a project to a vertical style re-runs the transcription to restyle its
captions; and B5's other half — wrapping long lines to the frame and holding each one
long enough to read — was left for B5b. Accepted at `539c1f7`: control-plane local
gate 532 passed / 8 skipped with `steps not run: none`, win-devops `OVERALL PASS` at
that head (`exit=0 duration=1m47.356s`), Linux full gate 521/13 with
`not run: nothing`, zero `DATA RACE` lines, and the new suites run explicitly there
(`subs ran=10`, the subtitle chain `ran=2`, `skipped=0` in both).

**B5b (captions, second slice): a cue fits its frame and stays long enough to
read.** Words are grouped into lines that fit the frame's usable width
(`(PlayResX − 2·MarginL)/FontSize` units, a word's separator included), at most two
lines per cue (the `\N` line break between them), and a segment longer than that becomes
successive cues whose times tile the original span — no hole, no overlap. A cue
shorter than 1.2 s is held into the silence after it, stopping at the next cue's start,
and the hold extends only the *display*: `speechEnd` is what the karaoke fill runs to,
so a held caption never finishes its highlight after the singing did. On a 1080×1920
frame that is twelve units a line, so a 40-character lyric becomes four cues of two
lines of six characters, every sweep still 20 cs.

Measured on the generated file rather than on the code: `TestCuesWrapToTheFramesWidth`
(cue count, at most one line break per cue, at most twelve units a line, all forty
sweeps and forty characters present), `TestShortCueIsNotSplit` (the control — two
characters stay one cue, so the wrap cannot be accused of always firing),
`TestCueTimesAreMonotonicAndNeverOverlap`, `TestBriefCueIsHeldLongEnoughToRead`,
`TestHoldStopsAtTheNextCue` (a floor, not a target) and `TestHoldDoesNotStretchTheFill`.
Six mutations, each killed by a named assertion: wrap off, dwell zero, hold uncapped,
sweep crossing a line break, margins ignored in the width maths, dwell leaking into the
fill.

Two things fell out of doing it, both worth keeping. The full suite caught a regression
the new tests did not cover — the dwell had stretched the last word's fill
(`{\kf50}你 {\kf70}好` where it had been 50/50) — and `TestSubtitlesCommandEndToEnd`,
written for the old behaviour, is what said so. And **two of my first assertions could
not fail**: counting characters but not the separators the code budgets, so the
margins-ignored mutation survived (fourteen units measured as seven characters ≤ 12),
and a hold fixture with enough gap that removing the clamp changed nothing. Both were
rewritten until their mutations died, which is the only reason either is in the file.

Accepted at `ae4a6ca` (docs head `a96f21c`): the Linux full gate PASSed there —
`== done gate_rc=0 layout_rc=0 subschain_rc=0`, the layout suite `ran=7 skipped=0`,
the subtitle chain `ran=2 skipped=0`, `DATA_RACE_lines=0`, `not run: nothing`, 13
skipped overall — read from that node's own `linuxrun-a96f21c….out`. The control-plane
local and win-devops legs passed at the same head in the session that wrote it; those
two verdict lines were read then and are not re-grepped from a file now, so this
milestone's third channel is the weaker one and is recorded as such.

**B5c (captions, third slice): the plain transcript gets the same frame.** Which
artifact a burn-in drew had been decided by a sidecar detail: with per-syllable
timings the words got the designed `.ass`, without them they got an `.srt` and
libass's own defaults. Only the fill needs syllables, so `WriteCaptionASS` runs the
same `layoutTranscript` and drops the `{\kf}` tags; where the timed path lays out by
word, the plain one lays out by character (nothing told it where a word ends) and
divides the segment's span between its cues by the characters each carries. The
pipeline now writes whichever `.ass` a transcript can back, which also means a second
transcription *replaces* a karaoke file rather than only deleting it — the stale-file
retry stays for the case that still has nothing to write, a transcript whose segments
were all empty. `xcut subtitles --ass` answers an ordinary transcript with
`Style: Caption,` instead of "karaoke output needs them", and reports which of the
three it wrote.

The tiling test earned its place before the code shipped: the first draft divided the
span per cue instead of cumulatively, and the generated file showed
`Dialogue: 0,0:00:03.60,0:00:03.60,Caption,…` — two lines of text handed no time on
screen at all. Four mutations replayed afterwards, each stopped by a named assertion:
the plain transcript writes nothing (`the second pass left no .ass at all`), the write
refuses to clobber an existing file (`TestReTranscribeReplacesTheKaraokeFile` is the
only case that notices — a plain-first project cannot see it), the caption writer
ignores the caller's canvas (`caption .ass lacks "PlayResX: 1080"`), and the CLI
branch calls the karaoke writer (`subtitles --ass failed (1): transcript has no word
timings`). One case of the family was also *fixed* while being written: the first
version of the character count summed `{\N}` as if it were text, so a wrap test that
looked like it measured 44 characters was measuring 40 characters and two escapes.

Accepted at `e909e37`: the control-plane local gate ran `545 passed, 8 skipped` with
`gate (fast): PASS (steps not run: none; …)` and `LOCAL CI PASS`; win-devops reported
`OVERALL  PASS` at that head (`exit=0 duration=1m50.454s`) with its own
`local evidence (after_local_pass): local CI PASS at 22:28:17 for e909e37e`; the Linux
full gate closed with `== done gate_rc=0 layout_rc=0 subschain_rc=0`,
`not run: nothing`, 13 skipped, `DATA_RACE_lines=0`, and both new legs run explicitly
on that machine — the caption layout `ran=4 skipped=0`, the subtitle chain through the
CLI and the api `ran=5 skipped=0`.


**B6d (UI, fourth slice): one tap to a post-ready reel.** `POST
/api/v1/projects/{id}/export` and the ★ button in the timeline pane run the sequence
a user would click — reel, captions, render — as one job that builds the timeline if
the project has none, transcribes if captions were asked for and none exist, and then
*queues* the render. It waits for no other job, and that is the load-bearing decision:
the queue is `resource.max_concurrent_jobs` (default 2) slots deep, so a parent that
blocks on its own child lets two taps deadlock each other, and a render run inside the
tap would slip past `resource.max_render_workers`. The render is therefore a row of its
own. The response carries the plan — each stage `reuse`, `create` or `skip` with a
reason — because the stage a machine cannot perform (no sidecar to transcribe with) is
the one that must not turn into a silent absence; the timeline pane shows that line
under the buttons. `export` joined the exclusive job set (a second press is a 409, not
two reels), which needed migration v7 to widen the partial unique index, and a new
test compares the queue's map with the index's list because those are one rule written
in two languages.

Measured on the state the tap leaves rather than on the code: three pipeline cases on
ffmpeg-made media (a tap that reaches a file on disk, one export row beside one render
row; a tap with no sidecar on `PATH` that succeeds and says why; a tap over a
hand-written horizontal reel and a sentinel `.ass`, both left byte for byte), five api
cases on the plan's wire keys — one of them pinning the literal app.js posts to, so the
two halves of that agreement are compared — and two storage/queue guards, one of which
applies v7 to a database that predates it with an active job still in the table, and
shows the rebuilt index reaching that row. Six mutations, each killed by a named case;
the one worth reading is `one tap wrote 1 export and 0 render rows`, which is what a
render swallowed into the tap's own body would cost. The ordering also buys something:
because the reel is built before the transcript is styled, the captions a one-tap
produces declare 1080×1920 — the canvas of a document that did not exist when the
request arrived.

Not verified: no browser was opened for this. The readout's markup and keys are pinned
by the standing DOM/i18n guards and its input by the api's wire assertions, but nobody
watched ★ write the line on screen. At the time of that acceptance `xcut auto` had no
caption step either — closed the same night by `--subs=on`, which runs the transcript
after the reel is built (described with B6e below).

Accepted at `c794786`: the control-plane local gate ran `555 passed, 8 skipped` with
`gate (fast): PASS (steps not run: none; …)` and `LOCAL CI PASS`; win-devops reported
`OVERALL  PASS` (`exit=0 duration=1m56.265s`) beside its own `local evidence
(after_local_pass): local CI PASS at 23:42:29 for c794786d`; the Linux full gate closed
`== done gate_rc=0 layout_rc=0 subschain_rc=0` with `not run: nothing`, 13 skipped and
`DATA_RACE_lines=0`, and this milestone's suites were then run explicitly on that
machine — `export_rc=0 ran=11 skipped=0`, including the three real-media taps
(`TestExportTapReachesAFile 5.22s`, `TestExportReusesArtifactsItDidNotMake 2.26s`)
against the snapshot's own ffmpeg. The ledger commit `69a52d3`, which added the `.srt`
assertion beside the `.ass` one, passed the same three channels: local `555 passed,
8 skipped` / `steps not run: none`, win-devops `OVERALL  PASS`
(`exit=0 duration=1m35.539s`), and Linux `== done gate_rc=0 tap_rc=0 guards_rc=0` with
`not run: nothing`, `DATA_RACE_lines=0`, `tap … ran=3 skipped=0` and the guards
`ran=8 skipped=0`.

**B6c (UI, fifth slice): a camera-motion picker for one clip.** The inspector's clip
panel offers 运镜 per clip — still, punch in, drift, follow the region — and asks the
server what each means. The reason it is a round trip and not a line of JavaScript is
that `style.framingPlan` was already the only rule turning a mode name into geometry:
a second implementation in the page would mean two answers to "drift", reconciled by
nobody. So the rule moved out into `style.MotionFor`, the builder became its caller,
and `POST /api/v1/projects/{id}/motion/plan` is its second. The region is read off the
asset row rather than handed up by the page, for the same reason — it is the region the
builder would have aimed at. Nothing is stored and no write path was added: the reply
lands on the clip and travels through the existing Apply → PUT, so the revision check
and the pre-regeneration backup stay where they are, and `none` comes back with no
window and no `framing` claim, because a clip that says it is framed while showing the
whole frame is the lie the style tests already refuse.

Measured: fourteen cases run explicitly on the Linux node (`pass=14 fail=0 skip=0`) —
six new in `style` and five new at the api, plus the pre-existing framing and preset
cases that the refactor had to leave untouched (that is the half which says nothing
moved). Six mutations, each killed by a named case: drift stops alternating, roi without
a region invents a centre, the framing claim is never answered, the zoom bound is
dropped, the builder stops asking the shared rule, and the page writes the geometry
without its claim — the last one caught by a text guard rather than a browser.

Not verified: no browser was opened. The select's on-screen behaviour rests on
`node --check`, the id/i18n guards, and that text guard; and hand-picked motion still
does not survive regenerating the reel, which the pane says out loud.

Accepted at `d154b72`: local `566 passed, 8 skipped` with `steps not run: none`;
win-devops `OVERALL  PASS` (`exit=0 duration=1m46.792s`, evidence bound as `local CI
PASS at 01:21:35 for d154b726`); Linux full gate `gate (full): PASS … not run: nothing`,
13 skipped, `DATA_RACE_lines=0`, `FAIL_lines=0`, `== done gate_rc=0`. The hosted
workflows stayed silent for the whole push run — `check-runs` for this head is 0 and the
newest workflow run on the repository is still 2026-09-20, read after the delay a
push-time check would not have given.

**B7a (resources): measured, and one knob's name was wrong in the docs.** The rows are
in `docs/PERFORMANCE.md`; the shape of the finding is that `max_analysis_workers` bounds
assets in flight while `max_ffmpeg_processes` bounds the children, so raising the first
alone changed nothing (18.1 → 18.1 s), and raising the second to 4 doubled both the
children (2 → 4) and their memory (107 → 215 MB) while cutting the wall to 12.1 s. The
one tap costs what its stages cost; the xfade render is a single 566 MB child that the
process cap cannot reach, which is the number any future default for
`resource.ffmpeg_max_memory_mb` has to be set against. Idle RAM/CPU were re-measured
after the whole arc: 18.1 and 17.7 MB, 0.000 s CPU delta on both runs.

Accepted at `49f3476` on two channels, and the third is named as missing rather than
implied: local `555 passed, 8 skipped` / `steps not run: none`, win-devops `OVERALL
PASS` (`exit=0 duration=2m9.486s`), and no Linux leg — that commit changed documentation
and a struct comment, and the code those words describe was Linux-verified at `69a52d3`
and `d154b72`.

**B6e (CLI, the one-shot's caption step).** `xcut auto --subs=on` transcribes between
building the reel and rendering it; `--subs=some.ass` burns a file the caller already
has, which is the shape `xcut render --subs` takes. Without the flag the command is
byte-for-byte what it was. The step runs after the timeline on purpose — the caption
box is styled against the canvas the same run wrote — which is the same ordering
property the tap has, now on a machine with no browser open. It goes through a new sync
entry point, `TranscribeProject`, that records the same `subtitles` job row the HTTP
path does; it does not wait for another job, because a job blocking on a child inside a
two-slot queue is how two runs deadlock.

Measured: two cases on generated media with a fake sidecar — the flag's run leaves one
`subtitles.ass` declaring 1920×1080 (the canvas `generic_highlight` had just written,
not the writer's shipped reference) with no karaoke tags, and renders the reel; the
flagless run writes no caption file. The refusal case narrows `PATH` to the directory
the media tools actually live in, so ffmpeg stays runnable while no sidecar can be
found: taking `PATH` away wholesale made the run die at the import instead, which is a
different failure with a different cause and would have proved nothing. Two mutations:
the flag never reaching the switch kills both cases; a swallowed transcript error
kills the reporting case — **and it did not at first**, because the assertion read
`stderr` for the word "sidecar" and the job logger echoes the cause into the same
stream. Reading the command's own `xcut auto:` line is what gave the assertion its
teeth; the same lesson in prose is in the commit message.

**Declared cost, for the operator to keep or reject.** `pipeline.AssetMotionROI` was
unexported until B6c needed the same `storage`→`style` region mapping in an HTTP
handler. Calling the existing function beat writing a second one that could drift, but
widening a package's exported surface to serve a new caller is a trade, not a freebie,
so it is named here instead of left as a silent fact. Nothing else in the arc changed
visibility for testability's sake.

Accepted at `58c628b`: local `568 passed, 8 skipped` with `steps not run: none` and
`no leaks found`; win-devops `OVERALL  PASS` (`exit=0 duration=1m49.266s`, its own line
recording `local CI PASS at 01:52:28 for 58c628b6`); Linux `gate (full): PASS … not run:
nothing`, 13 skipped, `DATA_RACE_lines=0`, `FAIL_lines=0`, `== done gate_rc=0
oneshot_rc=0 chain_rc=0`, with the one-shot leg `ran=6 skipped=0` and the
caption-and-tap chain leg `ran=10 skipped=0` run explicitly on that machine.


**A walk through the product (02:16), and what it turned up.** The suite says the
features work; the walk exists to ask what a user would *see*. It drove the HTTP
surface the way the page does — the real 603 s match, a synthetic 120 BPM click bed
written as PCM so the grid is a fixture rather than an approximation, the vertical
style, a transcript with word timings including two hostile lines (`{}`, a backslash,
a newline) — from import through the tap to the rendered file, printing every
user-facing string on the way: the health payload, the plan, the job progressions, the
document metadata, the pacing object, four clip rows, the ffprobe of the output, and
the caption file itself.

It cost about four minutes and produced three defects no test covered, all fixed in
`0bf23f2` (see the CHANGELOG entry for the details and the four mutations): float noise
on the pacing wire (`7.50000000000001`), a plural note that lied twice in the one case
where the vertical style on sports footage actually produces it, and a missing guard
over i18n placeholders — which the act of hand-translating that note was about to
exploit. It also confirmed what already held: the escaping put the hostile characters in
as text rather than as live tags, the caption file declared the canvas of the reel the
tap had just built, and the tap's 41 s from import to file is dominated by analysis,
not orchestration.

The lesson is not that the walk is a test. It is that reading the product's own
sentences, in order, on real input, finds the class of thing that unit tests cannot:
a number nobody was supposed to read raw, and a sentence whose grammar was written for
a case the fixture never had. The script lives outside the repo
(`D:\tmp\xcub1\walk.py`) with its workspace, because it is a probe, not a gate.

Accepted at `0bf23f2` on all three channels: local `573 passed, 8 skipped` with
`steps not run: none`; win-devops `OVERALL  PASS` (`exit=0 duration=1m50.187s`,
`local CI PASS at 02:37:27 for 0bf23f21`); Linux `gate (full): PASS … not run: nothing`,
13 skipped, `DATA_RACE_lines=0`, `FAIL_lines=0`, and the two legs run explicitly —
`walk_rc=0 ran=13 skipped=0` (the wire case and the singular note among them) and
`guards_rc=0 ran=15 skipped=0`.

**The walk came back a second time, onto the surface a user edits by hand.** The first
walk drove the happy path; this one did the opposite — it PUT documents the builder would
never produce and read the refusals. A zoom of 3.0 on a vertical reel, an asset id invented
on the spot, an empty document, a document wrong in four places at once, a revision copied
from the server's own reply: every one is a request the page can be made to send, and each
answer is a sentence a person has to act on.

Three sentences failed there, and all three are fixed at `bd80006`: the count that stood in
for the list (`(1 problem(s))`, produced identically by three different documents), the
float a validator interpolates when it prints a number it never computed for display
(`starts 10.500100000000003`), and the 409 that told a client it had missed a change when it
had never read anything at all. The UI's part in that last one is the finding worth
keeping: the page carried its own fixed sentence for 409 — "the timeline changed elsewhere"
— so the server's diagnosis never reached the screen, and the more specific the API became,
the less of it a user would see. The banner quotes the server now and adds only what only
the page knows, which is the name of the button to press. Its script lives outside the
repo with the first one's (`D:\tmp\xcub1\walk2.py`), for the same reason: a probe, not a
gate.

**What the acceptance for that commit turned up was larger than the commit.** The local
gate returned 0 at 03:21 — `gate (fast): PASS (steps not run: none)` — over
`== go test: 0 passed, 0 skipped`. The test step's stderr redirect pointed at a fixed name
in `%TEMP%`, another project's gate had opened that same file at 03:20, the redirect raised
an `IOException`, and `go test` was never asked to run. Nothing in the verdict line could
see it: the step *had* run, it had simply run nothing, and `$LASTEXITCODE` still carried the
previous step's 0. It surfaced only because this time the output was read line by line
instead of judged by its last line. Fixed at `b17c427` (a name per process, plus the floor
that was missing — zero test events is a refusal) and at `86dbaa0` (the same floor under
`sh`, which had the hole without the collision). Reproduced in both directions rather than
argued about, in `out/gate-mutA.log` and `out/gate-mutB.log`.

The shape to remember is not the temp file. A gate's green is a claim about what it ran,
and the only thing that can be trusted to say so is a count it prints — which is why both
scripts now refuse to be green on an empty one.

Accepted at `86dbaa0` (which carries `bd80006` and `b17c427`) on all three channels: local
`579 passed, 8 skipped` with `steps not run: none`; win-devops `OVERALL  PASS`
(`exit=0 duration=1m33.927s`, `local CI PASS at 03:37:40 for 86dbaa03`); Linux
`gate (full): PASS … not run: nothing`, `568 passed, 13 skipped`, `DATA_RACE_lines=0`,
`FAIL_lines=0`, with the new cases run explicitly — `validators_rc=0 ran=9 skipped=0` and
`refusals_rc=0 ran=6 skipped=0` — and the `sh` guard controlled on that node:
`guard_rc=1 BITES (an empty run was refused)`, snapshot `restored byte-identical`.

**A defect as old as the rotated log, found by running `serve` for the first time.**
`startServeCore` opened `<workspace>/logs/serve.log`, handed the combined logger to `a.Log`,
and closed the file with a `defer` in its own prologue — so for the whole life of the server,
every line written through that logger went to a writer returning `os.ErrClosed`, and a
`slog` handler does not look at what its writer returns. What the operator was told to read
(`docs/OPERATIONS.md`) therefore stopped at the startup line: the request trace that
`api.writeErr` exists to write — its own comment says a failed request is "undiagnosable from
serve.log" without it — the job warnings, and the drain. It shipped that way in `c12e22b` and
survived every session since because **no test had ever called `startServeCore` or
`shutdownServe`**: the address policy had tests, the lifetime they protect had none.
Ownership of the file now passes to `runningServe`, whose `close` all three callers
(`serve`, `client`, `client --browser`) already defer; the bind-failure path closes it where
it fails.

Four cases drive the lifecycle (`internal/cli/serve_lifecycle_test.go`), each with the arm
that says "the file is the right one" beside the arm that says "and it kept recording": a
request the running server refused must appear in the file next to the startup line; the
three startup sweeps must reclaim what each of them *reports* — a job row left `running` by a
dead process, temp debris, staged-upload debris — while a landed upload survives and the log
directory is still there; the port must answer before the drain and refuse after it; and
`serve` must stay a writer command, because the sweeps' licence to delete is exactly the
workspace lock. The pre-fix run is the first red; five mutations were then replayed, each
killed by the assertion it targets — and one of them (the temp sweep turned into a dry run)
was caught *only* by the file-survival arm, because the receipt line printed anyway, which is
what that half of the case is for.

Accepted at `0769849b` on all three channels: local fast gate PASS (`583 passed, 8 skipped`,
`steps not run: none`), the four new cases green under `-race` locally, win-devops
`OVERALL PASS` (`exit=0 duration=1m35.726s`), and the Linux full leg PASS with
`572 passed, 13 skipped`, `DATA_RACE_lines=0`, `not run: nothing` (cargo fmt/clippy/test all
ran) and the new cases run explicitly — `ran=4 skipped=0`. The dispatch's own stdout was
lost to a redirect ordering mistake (`> f 2>&1 > /dev/null`), so the verdicts above are read
out of the files the runner writes inside the snapshot — which is the lesson this session
keeps re-learning: put the evidence where the script itself puts it.

**The one tap now reads the frame its captions were styled for.** `export` decided
"the project already has subtitles" from the existence of a file, so a project that
changed shape — the tap's own default reel is vertical, and a transcript made before a
reel existed is laid out against `subs`' shipped 1280×720 reference — burned a box
sized for a frame nobody would watch, with libass scaling every pixel field by the
script's `PlayResX/Y`. The claim was already in the file; what was missing was asking
it. `subs.ReadASSFrame` now does, and refuses to invent an answer (absent, unparsable,
or outside the section that owns the pair is *no claim*, which is not the same thing as
the default); `pipeline.subsState` compares that with the canvas of the document the
same tap is about to render onto, and one rule answers for the plan and for the body,
because the body may have only just built the reel whose canvas the plan could not have
known. Three answers where there was one: restyle through the sidecar, say that no
sidecar can, or keep the plain sentence — and the last of those is the arm that an
always-restyle rule would have to pass, checked by the file's bytes as well as its
words. Accepted at `75af02b`: local fast gate PASS (`603 passed, 8 skipped`,
`steps not run: none`), win-devops `OVERALL PASS` (`exit=0 duration=2m10.367s`), Linux
full gate PASS with `592 passed, 13 skipped`, `DATA_RACE_lines=0`, `FAIL_lines=0`,
`not run: nothing`, and this round's cases run explicitly on that node — `subs_rc=0
ran=13 skipped=0`, `api_rc=0 ran=4 skipped=0`.

Five mutations, one of which taught something. A body that never restyled was caught by
the artifact, not the sentence (the plan line alone would have passed); a plan that
ignored the state, a comparison that never fired, a no-claim file treated as a
mismatch, and a mismatch that ignored its own comparison were each killed by the case
named for them. Removing the section guard from the reader came back **green**: the
fixture built to catch it was already covered by the early exit, so it proved nothing
about that guard — the distinguishing shape (a file with no `Script Info` section at
all, the pair parked in a style block) is in the test now, and it fails when the guard
comes out. The dispatch that started this round's Linux leg also shipped the *previous*
round's runner script — two scratch directories, one of them stale — so the snapshot's
provenance was established directly (`internal/subs/subs.go` md5 on the node equals
`git show 75af02b:internal/subs/subs.go`) and this round's cases were run against it by
hand rather than taken on the runner's word.

**A round that changed no product code, because two security claims turned out to
be true — and unrun.** `docs/SECURITY.md` says `xcut config show` masks the bearer
token as `<set>` and that `xcut init` never bakes an environment-supplied token into
the 0644 config.json it writes. Both statements sit in the two lines that implement
them (`Redacted()`'s one call site, and the blank line in `cmdInit`), and no test had
ever invoked either command: `cmdConfig`, `cmdVersion` and `usage` were the last named
0 % entries in Next Priorities #1. Six cases now go through `Run` with `--config` and
`--workspace` pinned to a throwaway directory — the machine running the suite owns a
real `~/.xcut`, and an assertion that reads it silently tests the operator instead of
the code. A token from the file and one from `XCUT_AUTH_TOKEN` both come back as the
mask and never as themselves; the rest of the config survives the redaction; `init`
writes a file with no token in it; `config path` names the file that was passed in;
three bad argument forms are refused with the usage line and nothing on stdout; the
version banner is the package's own string with the stamped fields and the platform;
and the page an unknown command prints carries every registered command's name and
summary. Four mutations, each killed by the case named for it: dropping `.Redacted()`
from the marshal, dropping the blank line from `init`, printing the bare version
instead of the banner, and printing a usage page that skips the registry.

One of the three first-run reds was mine, not the product's: the assertion looked for
`"<set>"` in the raw output, and `json.MarshalIndent` escapes the angle brackets into
their `\u003c` form, so a working mask failed a passing test. Read a decoded field, not
an escaped byte string.

Accepted at `4dd32490` on all three channels: local fast gate PASS (`609 passed,
8 skipped`, `steps not run: none`); win-devops `OVERALL PASS` (`exit=0
duration=1m40.326s`); Linux full gate PASS with `598 passed, 13 skipped`,
`DATA_RACE_lines=0`, `FAIL_lines=0`, `not run: nothing`, and this round's cases run
explicitly on that node (`cli_rc=0 ran=6 skipped=0`) with last round's still running
(`regress_rc=0 ran=20 skipped=0`). The Linux dispatch itself went through the two
failure modes this ledger already knew: a hand-typed sha (`not a tree object`, caught
by `set -o pipefail` before anything launched, leaving a 4 KB husk that was then
deleted) and, on the retry, the runner's own stdout arriving with the verdicts in it —
the redirect ordering fixed, and the snapshot provenance checked by a marker line that
only this commit has.

**The worker protocol had never been spoken by a gate, in any direction.**
`internal/worker`'s three describe/error cases and the Go side of
`analysis.RustAudioAnalyzer` skip when the binary is absent, and the binary was absent
everywhere: `cargo check`, `cargo clippy --all-targets` and `cargo test` build it into
`target/debug/deps/`, while the lookup in those tests wants
`target/{debug,release}/xcut-worker-media`. Confirmed on the node after its full gate:
no such file there, and all three were among its 13 skips — for the whole life of a
boundary AGENTS.md names as core. Both gate scripts now build it before `go test` in
full mode (one toolchain probe each, hoisted into a function called twice rather than
duplicated), and each prints whether the binary landed, because the skip list below
that line is only readable against it.

Five cases sit on the Go side of it: the `auto` set must wrap the worker in the ffmpeg
net and must not share a cache key with plain ffmpeg analysis; `audio=ffmpeg` must
ignore a worker that is genuinely present — the existing mode test asserted that opt-out
with no binary configured, so it passed unchanged while a mutation made ffmpeg mode use
the worker, and that mutation is why the new case exists; an unusable worker must cost a
degradation, not an analysis; strict `audio=rust` must keep no net underneath it (the
"must not do more" arm, killed by the mutation that added one); and the worker's own
`audio_rms` answer must arrive as the track kind the pipeline reads, with samples that
vary. Five mutations total, each killed by a named assertion.

Measuring that last one is what turned up a stale claim: the worker refuses XCut's own
media shape (H.264 + AAC in an MP4) with `unsupported feature: aac: predictor data`, but
decodes the *same* encoder settings in a bare `.m4a` — so "symphonia cannot decode
ffmpeg-encoded AAC", recorded in two places, named the wrong culprit; and a PCM `.wav`
fails earlier because the crate has no wav demuxer. Both lines now say what was observed,
and `TestAutoModeCoversTheWorkerCodecGap` provokes the MP4 case with its premise
assertified, so the fallback cannot start passing on nothing.

Accepted at `983596f` on all three channels. Local fast gate: `617 passed, 5 skipped` —
the arithmetic of this change in one line, 609+8 = 617 and 8−3 = 5, three cases that used
to skip now running. win-devops: `OVERALL PASS` (`exit=0 duration=1m39.746s`). Linux full
gate: PASS with `606 passed, 10 skipped` (was 598/13), `DATA_RACE_lines=0`,
`FAIL_lines=0`, `not run: nothing`, its own new step printing `worker binary present —
internal/worker's protocol tests will run` and no `skip worker/` line left in the log —
then the explicit runs: `analysis_rc=0 ran=5 skipped=0`, `worker_rc=0 ran=21 skipped=0`
(the package's first zero-skip pass anywhere), `regress_rc=0 ran=20 skipped=0`.

**A coverage sweep, then the two things it was pointing at.** The max-merge sweep
(22 test binaries on the node, 82.4 %) named 28 functions at 0.0 %. Three of them have
no caller at all and two of those carry a comment naming one that does not exist:
`workspace.RenameAtomic` (its sibling's doc block says the timeline document keeps
plain `RenameAtomic`; the timeline publishes through `pipeline.WriteAtomic`, which
calls `RetryableRename`), `analysis.coverageAt` (a second, unread definition of the
beat grid's coverage rule, orphaned when B1b moved the computation into `bestPhase`),
and `analysis.Key` ("used by CLI logging" — no CLI line references it). All three are
gone and the comment now names what is actually called. Two more were checked instead
of assumed: `writeIdleWriter.Flush` is an interface-conformance shim — `ServeContent`
does not flush, verified against the stdlib's own `fs.go`, and the first version of
that case asserted a flush through the funnel and went red for exactly that reason —
so what is pinned is the shim's two arms; and `cmdClient` on `!windows`, the desktop
client a Linux or macOS user actually gets, had never been compiled by a test.

**The client case is also the round's lesson about which leg sees what.** It cannot
compile on the dev box; `GOOS=linux go vet` proves only that it type-checks; the
node's first run of it was without `-race`. Three defects surfaced in three separate
Linux runs, all mine, none of them reachable from this laptop: the URL was sliced from
the first `http://` in the shared buffer, which is the serve pipeline's own line, not
the client's; it bound the configured default port, so any neighbour holding 8619
would have failed it for unrelated reasons; and the signal channel field was cleared
after firing from the writer's goroutine while the test read it unlocked — a real data
race, caught by the detector the local and Windows legs do not run. Each was fixed
where it could be seen, and the last one re-verified with `-race -count=5` on the node
before the gate was re-run.

**Packaging is checked again, locally, on the host it ships from.** The artifact smoke
was a GitHub Actions step and that runner is out of quota, so `build-release` had been
stamping three platform binaries and never starting one. `scripts/smoke-release.sh`
(now called by both twins, one check list in one file) runs the host artifact: the
version and commit it was stamped with, the usage page, a nonzero refusal for an
unknown command, `init` laying out the workspace it was pointed at, and the shipped
`config show` still not printing a bearer token. Measured on both hosts: a Windows run
built all three targets plus the musl worker and printed five passes; and the Linux leg
carried the same step for the first time (`release_rc=0 checks=5`), which is the host
this side could not verify.

Accepted at `fabe46d` on all three channels: local fast gate PASS (`618 passed,
5 skipped`, `steps not run: none`); win-devops `OVERALL PASS` (`exit=0
duration=1m39.453s`); Linux full gate PASS with `608 passed, 10 skipped`,
`DATA_RACE_lines=0`, `FAIL_lines=0`, `not run: nothing`, and every new case run
explicitly on that node — `client=0 ran=1`, `flush=0 ran=1`, `worker=0 ran=21`,
`analysis=0 ran=5`, `captions_rc=0,0 ran=20` (round two's cases, still green),
`release_rc=0 checks=5`. The two intermediate shas are part of the record rather than
erased from it: `4f032be2`'s Linux leg was red on my own test's URL slice, and
`9b30373`'s was red with `DATA_RACE_lines=1` — the gate's numbers, not mine, which is
why they are quoted here instead of a green headline.

**The rendering rule that the session posture leans on now has a gate.** SECURITY.md
listed the token, the loopback rule, the path guard and the pinned MIME types, and said
nothing about the one surface where a bug reads the workspace through an authenticated
session: a script injected into the embedded page. PROJECT_STATE had been carrying
"the textContent-only rendering rule is what holds that line" as a *description* — and
nothing checked it, while `app.js` held twelve `innerHTML` assignments that happened to
all be empty-string clears. Two guards now scan the shipped scripts through one
classifier (markup sinks: `innerHTML`/`outerHTML` assignment, `insertAdjacentHTML`,
`document.write`; dynamic code: `eval`, `new Function`, the string timer forms),
accepting only the clear. Both halves of the discipline this project keeps relearning
are in the file: a floor that requires the scan to find the dozen clears it expects, so
"no violations" cannot come from a regex that stopped matching; and a refusal table run
through the same classifier rather than against the regexes directly — which is how the
first version of the control disagreed with the guard about what a clear is (it fed the
patterns whole lines with their `;`, while the guard sees folded statements).
Verified the only way that counts: injecting `probe.innerHTML = "<b>" +
currentProject.name;` and `setTimeout("pollTimer = null", 10)` into the asset turned
both guards red naming `app.js:8` and `app.js:9`, and `git diff` on the asset is empty
afterwards. SECURITY.md gained the subsection the rule deserved and never had,
including what the guard does *not* cover.

Accepted at `f2a4eb2`: local fast gate PASS (`621 passed, 5 skipped`,
`steps not run: none`) and win-devops `OVERALL PASS` (`exit=0 duration=1m32.389s`).
The Linux full leg was **not** re-run for this sha: no product code changed since
`fabe46d`, whose leg was green end to end — the delta is one test file and two docs.

**The per-milestone legs now look at data races, and say when they cannot.** `-race`
had lived only in the full battery on the Linux node, so no leg run per milestone had
ever watched concurrency: this session's own race (a test helper clearing a channel
field the test goroutine read) passed `GOOS=linux go vet`, passed the node's non-race
verification of that same file, and was reported only by the full gate, 20 minutes and
one dispatch after the change. Fast mode in both twins now runs `-race` over
cli/job/worker/pipeline — `internal/cli` because it holds both races this project has
recorded (`680d707`'s shared writer, and that helper). Measured here: 62 s and 98 s on
two runs of the subset, 3m16s for the whole fast gate against roughly 2m30s before; the
full leg still runs `-race ./...`, so this is latency removed, not coverage moved. The
other half of the fix is older and quieter: neither script had ever recorded the race
skip in its own `not run:` list, in fast or full mode — the verdict line that exists to
state what a green run did not check had no entry for the longest thing it skips. That
was verified rather than assumed: with the cgo probe forced false the verdict reads
`not run: race-subset`, and with it true, `steps not run: none` plus the wall time.

Accepted at `111e2ba`: local fast gate PASS (`621 passed, 5 skipped`, race subset run,
`steps not run: none`); win-devops `OVERALL PASS` (`exit=0 duration=1m32.141s`) which
**skipped the new step and said so** — read out of the node's own job log
(`20260923-133058-4bf8f0`): `== go test -race: SKIPPED (no cgo/C toolchain; the full
leg covers it)` and `gate (fast): PASS (steps not run: race-subset; tests skipped: 11`.
That node has no C toolchain, so the -race subset runs on this laptop and on the Linux
node and nowhere between; the accounting line is the reason that is visible in the
verdict instead of inferred from a duration that did not move.

**One laptop's download was carrying a layout check.** `TestRealZipLayoutExtracts`
opened `../../.gotmp/ffmpeg-9.0.1-essentials_build.zip` if that file happened to exist
and skipped otherwise — the CI node's own log says it skipped, and a pin bump would
have left it skipping the layout of a version nobody downloads. `TestPinnedArchiveLayoutExtracts`
now derives its archive root from `FFmpegPin.URL`, the same constant that decides what
gets fetched, so the fixture moves with the pin; it asserts the returned paths, the
bytes that landed, and the install directory's full listing.
`TestDuplicateToolEntriesKeepTheFirstOne` pins which of two `ffmpeg.exe` entries wins.
The genuine-artifact case stays, globbing now and naming what it found rather than
passing a stale cache off as the pinned one. Accepted at `f49da9a` — local fast gate
PASS (`623 passed, 5 skipped`, race subset 59 s) and win-devops `OVERALL PASS`
(`exit=0 duration=1m43.877s`, `steps not run: race-subset` as before on that node),
whose job log is the evidence that matters here: the two new cases are **not** in its
skip list, so the layout assumption is now tested on a machine that never had the
110 MB file. Two mutations were needed to believe the test, and the first came back
green: adding `ffplay.exe` to the selection map cannot be observed, because the same
duplicate guard skips it once its slot is filled — which is why the assertion is about
the directory listing and not about a name being absent.

**The config layering silently discarded a user's global settings.** AGENTS.md and
`config.go`'s header both promise defaults < `<workspace>/config.json` < env < flags,
and the file layer never honoured it: `config.Load` prefilled with `Default()` before
decoding, so an unmentioned field arrived at `MergeLayer` already carrying its default —
and `MergeLayer` reads a zero as "not said" (its own `TestMergeLayerKeepsBaseWhenLayerZero`
says so). Writing one knob into the workspace file therefore reset everything the
bootstrap config had set. Measured through the shipped binary, then re-measured after
the fix, with `~/.xcut/config.json` at `log.level=debug`, `max_cache_gb=2`,
`max_temp_gb=3` and a workspace file mentioning only `frame_sample_fps`:
`info/10/20/3` (three values invented) → `debug/2/3/3`. `Load` is sparse now, by
contract and with the reason in its comment; defaults are applied at the point where
precedence is built, and the two `internal/config` Load tests assert that shape instead
of the old accident.

Three things this round turned up about how to test a layering: an explicit `--config`
*deliberately* stops at the bootstrap file, so the first version of the new case ran
green against the broken code without ever touching the layering — it now asserts the
resolved path is the workspace file before it asserts anything about values; flipping
the merge direction is caught by two named assertions; and the env layer passing under
that flip is not a contradiction, it guards a different edge.

Accepted at `f2b819c` on all three channels: local fast gate PASS (`627 passed,
5 skipped`, race subset 60 s, `steps not run: none`); win-devops `OVERALL PASS`
(`exit=0 duration=1m53.118s`); Linux full gate PASS with `617 passed, 10 skipped`,
`DATA_RACE_lines=0`, `FAIL_lines=0`, `not run: nothing`, and the new cases run
explicitly on that node — `precedence=0 ran=3`, `loadcontract=0 ran=2`, plus the
earlier rounds still running: `client=0 ran=1`, `flush=0 ran=1`, `worker=0 ran=21`,
`analysis=0 ran=5`, `release_rc=0 checks=5`.

**The two bounds that were built and left behind a default are now shipped** (owner
decision, recorded as D16): `resource.ffmpeg_max_memory_mb` defaults to 1536 MB —
2.7× the 566 MB xfade child measured here, with `0` still the explicit uncapped
answer and a negative value repaired to the shipped cap rather than to no cap — and
`resource.proxy_enabled` defaults to true, which is what makes `max_proxy_gb` a
ceiling over something real. The second decision forced the first's type: a bool
cannot tell "the file said false" from "the file said nothing", and `MergeLayer`'s
one-way rule — harmless while the default was off — became a knob you could opt into
and never out of. `ProxyEnabled` is a `*bool` now (carried when non-nil, normalised
by `Resolve`, read through `Config.ProxyOn()`), which only means anything because
`config.Load` went sparse earlier in this session.

Verified on the shipped path, not just in a unit: `xcut auto` over a 1280×720 clip in
a clean workspace wrote `cache/proxy/<fp>.w640.f2.mp4` (576 KB for 20 s) and analysed
from it — 4.2 s wall for import → analyse → timeline → a 5.6 MB reel.

The battery found a hole in what I had just written: flipping `ProxyOn()`'s nil arm to
"off" **passed the entire suite**, because `Resolve` fills the pointer and every test
reaches a resolved config — so the branch I had justified in a comment ("a hand-built
Config must not flip behavior") had no test reaching it. `TestProxyOnDefaultsInAnUnresolvedConfig`
now builds exactly that config, and the same mutation is caught by two named assertions.
The merge-completeness gate also had to learn the pointer kind rather than skip it, and
two comments that promised "Config holds no reference types" were corrected: `Redacted()`
is still safe, but the reason is now the real one.

Accepted at `6b6f73e`: local fast gate PASS (`629 passed, 5 skipped`, race subset 121 s —
the widest it has measured, proxies now built under the detector); win-devops `OVERALL
PASS` (`exit=0 duration=1m55.491s`); Linux full gate PASS with `619 passed, 10 skipped`,
`DATA_RACE_lines=0`, `FAIL_lines=0`, `not run: nothing`, and the config cases run
explicitly — `defaults=0 ran=10 skipped=0`, `precedence=0 ran=3`, plus the standing
regressions (`worker ran=21`, `analysis ran=5`, `captions ran=20`, `client`, `flush`,
`release_rc=0 checks=5`).

**ARM64 stopped being an unverified claim.** Decision (a) was taken, so `449e1af` pinned
a stock arm64 FFmpeg, and the first run of that pin disagreed with the run before it:
`ran=553 failed=48` where the manual run had been all-`ok`. Two harness defects, both
mine, and each one a lesson worth keeping:

1. `fetch-arm64-ffmpeg.sh` joined its argument onto the repo root without asking
   whether the argument was already absolute, so `…/src` + `/root/…/tools` produced a
   394 MB FFmpeg *inside* the checkout it was verifying. The doubled path was visible
   in the run's own log (`bin=/root/xcut-arm64/src//root/xcut-arm64/tools/…`) and the
   old expression reproduces it exactly — that is the mutation's teeth, plus two arms
   (absolute argument used as given, relative one resolved against the repo root) and a
   runner assertion that refuses a fetched path under `$SRC`.
2. The runner exported `XCUT_FFMPEG`/`XCUT_FFPROBE` and left `PATH` alone. Those
   variables are read by `config.Env` — the product's startup layer — while the suite
   resolves both binaries *by name* (`testmedia.HasFFmpeg`, `media.requireFFmpeg`), so
   the injection never reached the code under test and 48 tests answered with the
   vendor build's own corruption message. The failure message was the evidence: same
   suite, same box, same pinned binaries, `PATH` instead of env → `failed=0`,
   `vendor_corruption_lines=0`. OPERATIONS.md had inherited my mistake and told a
   reader to verify with those variables; it says `PATH` now, and the runner prints
   `TOOL_CHECK=OK (vendor 4.2.2 is shadowed)` from `command -v` before it tests
   anything, because "the binary exists" is not "the tests used it".

Accepted at `2544cc1`: local fast gate PASS (`629 passed, 5 skipped`, race subset 96 s,
`steps not run: none`), and the ARM64 leg on Kylin V10 SP1 aarch64 — `fetch_rc=0`,
`TOOL_CHECK=OK`, `ran=553 failed=0 skipped=17`, 19 packages `ok`, `stray_in_src=0`.
The two deep legs (win-devops, Linux full) were not re-run for this commit: it changes
one POSIX shell script and four prose files, and the platform that script targets is
the one that ran it.

**v0.1.9-alpha was cut, and the cut is where the packaging discipline paid for
itself.** Decision (b) was taken, so the release went out the local path —
`build-release.ps1` → `make-installer.ps1` → `make-setup.ps1` → `gh release create`.
Three things worth recording:

- The host artifact was **executed by the build** (`smoke-release.sh`, 5 checks: the
  version stamp, `-h`, an unknown command refusing, `init` laying out a workspace, the
  token masked). That step exists because of this session's earlier work; for the first
  time the binary that shipped is a binary something ran, and the stamp it printed —
  `xcut v0.1.9-alpha (48d0fe8, 2026-09-23T08:14:39Z, go1.26.6 windows/amd64)` — is the
  ldflags contract checked rather than assumed.
- The two cross-built Linux binaries were run **on the machines they target**: same
  5 checks on Kylin V10 SP1 aarch64 and on linux-ci x86_64 (`go1.26.6 linux/arm64` and
  `…/amd64`). The arm64 one then did the product's actual job against the pinned
  FFmpeg: a 20.02 s four-color fixture through `xcut auto` → import, analyse, timeline
  (4 clips, canvas 1920×1080@30) and render in 5 s wall, producing a 392,449-byte file
  that ffprobe reads back as 20.021 s with a video and an audio stream. A `SHA256SUMS`
  asset joined the release (six lines, `sha256sum -c` all OK); it had never been there.
- `gh release create` with seven assets did **not** finish inside 580 s on this link,
  and what it left behind was not "nothing" — it was a *draft release holding four of
  the seven files*. The command's exit status was 124 (the timeout's, not gh's), so the
  only verdict that means anything is the asset list read back: `gh release view
  --json assets`. Recovery was `gh release upload --clobber` for the three stragglers
  and then clearing the draft. Recorded because "the create failed" and "the create
  failed leaving a half-finished public artefact" are different incidents, and only the
  second one is what actually happens.
- **`gh` reads the tag through a cached endpoint, and it lied.** Right after
  publishing, `gh release view --json isDraft,assets` reported `assets=0`, and
  `gh release download` said "no assets to download" — while
  `GET /repos/…/releases/394440623` and `/releases/394440623/assets` both listed all
  seven. Believing the first reading nearly put the release back into draft ("a public
  release with no files!" is what the stale cache said). What settled it was the thing a
  user actually does: `curl` the browser download URL. `SHA256SUMS-v0.1.9-alpha.txt`
  came back 200 / 607 bytes / md5-identical to the local file, and
  `xcut-v0.1.9-alpha-linux-arm64` came back 200 / 11,731,106 bytes with a SHA256 that
  `sha256sum -c` accepted from that downloaded sums file — so published bytes equal
  built bytes, verified through the public path rather than through a client's cache.

Accepted at `48d0fe8`: local fast gate PASS (race subset wall 76 s, `steps not run:
none`, 5 skips named), and the tag push started no Actions run — checked against
`gh run list`, whose newest Release entry is still `v0.1.8-alpha`. The release commit
then went through the deep legs it skipped on the way: **Linux full gate PASS at
`48d0fe8`** (`619 passed, 10 skipped`, `DATA_RACE_lines=0`, `FAIL_lines=0`,
`not run: nothing`, `worker ran=21`, `analysis ran=5`, `captions ran=20`, and the
release smoke run on that platform — `release_rc=0 checks=5`), and **win-devops
`OVERALL PASS`** at the following docs commit (`exit=0 duration=1m33.343s`,
`local evidence (after_local_pass): local CI PASS at 16:58:18 for 8d26c725`).
So the shipped Windows and Linux binaries come from a tree that passed the race
detector, the Rust worker build and the artifact smoke on two platforms.

**The ARM64 leg stopped living in a scratch directory.** `scripts/verify-arm64.sh` is
the durable form of the runner that produced this session's ARM64 numbers, with the two
mistakes that cost the first round made unreachable rather than merely documented: the
toolchain goes on `PATH` (the suite resolves `ffmpeg`/`ffprobe` by name, so
`XCUT_*` would have it run on the vendor build), and the fetch script's exit status is
taken *before* its output is piped (`| tail -1` reports tail's 0). It refuses a
non-aarch64 host instead of downloading a binary that cannot exec, and the race attempt
is a report, not a pass. Run from a fresh snapshot and a from-scratch download:
`TOOL_CHECK=OK`, `suite_rc=0 ran=553 failed=0 skipped=17 packages_ok=19`,
`vendor_corruption_lines=0`, `race_rc=1 tsan_refused_lines=3 data_race_lines=0` — the
same counts `2544cc1` measured by hand, which is what makes the script a
reproduction rather than a restatement.

The gate now parses the shell it ships in: `check.sh` and `check.ps1` both run
`sh -n` over `scripts/*.sh` (ten files), name offenders, refuse to count a directory
that yielded fewer than five scripts as a pass, and record `sh-n` under
`steps not run:` on a host without a shell. Teeth were measured before committing — the
ten parse clean, and one deliberately broken `if [ x = y` turns exactly one red.

Accepted at `cb6a550`: local fast gate PASS with `== sh -n / 10 scripts parse` and
`steps not run: none`; **Linux full gate PASS** on the node (the POSIX twin's own step
printed `== sh -n` / `10 scripts parse` at line 3 of its gate log; `619 passed,
10 skipped`, `DATA_RACE_lines=0`, `FAIL_lines=0`, `not run: nothing`,
`release_rc=0 checks=5`); and the ARM64 leg above.

`--tools DIR` then made the leg re-runnable without moving 121 MB again (the flag takes
where the pin lives instead of always fetching into the checkout), and the third
reproduction on the Kylin box — a fresh snapshot, the cache directory handed to it — read
the same `suite_rc=0 ran=553 skipped=17 failed=0 packages_ok=19`, `TOOL_CHECK=OK` naming
the cache path, and the same reported race refusal. Accepted at `d6865b8`: local fast
PASS, Linux full PASS on the node (`== sh -n / 10 scripts parse`, `619 passed,
10 skipped`, `DATA_RACE_lines=0`, `FAIL_lines=0`, `not run: nothing`,
`release_rc=0 checks=5`, `worker ran=21`, `analysis ran=5`, `captions ran=20`).

**The memory cap stopped being a one-platform knob (D18).** `ffmpeg_max_memory_mb` had
an answer on Windows and a polite sentence everywhere else, on a project whose server and
ARM64 target are both Linux. Each FFmpeg child now starts in its own transient systemd
scope (`MemoryMax`, `MemorySwapMax=0`) through one `wrapChild` shared by `Run`,
`RunCombined`, `StreamStdout` and `Version`, probed once so a host that will not start a
scope runs unwrapped children instead of failing renders with somebody else's error
message. The measurements that shaped it, in order: the wrapper keeps stdout pure and
propagates exit codes (measured by hand before writing code — a wrapper that wrote one
line there would have rebuilt D13 inside our own toolchain); a 512 MB allocation under a
128 MB cap dies with SIGKILL on cgroup v2; and on Kylin V10 SP1 — hybrid cgroup —
`systemd-run` exits 0, the same allocation **survives**, and `systemctl show` says why
(`MemoryMax=infinity`, `ControlGroup=` empty: the scope was never attached). That last one
is why the posture string and the doctor row claim less than the mechanism can sound like
they claim, and why the remaining work is named rather than implied: ask `systemctl`, not
just `systemd-run`. Measured on the node (`8251b98` + this working tree): `ran=5 skipped=0
failed=0`, then the mutation — wrap disabled — `ran=0 failed=2` with
`a 512 MB allocation survived a 128 MB cap (stdout "536870912")`.

Accepted at `170b52f`: local fast gate PASS (`steps not run: none`), and the Linux full
gate on the node went `619 → 624 passed` with the same `10 skipped`,
`DATA_RACE_lines=0`, `FAIL_lines=0`, `not run: nothing`, `release_rc=0 checks=5` —
which is also the first time the whole suite ran on a Linux host with the wrapper
installed, so no existing test discovered it had been depending on an unwrapped child.

**Doctor stopped inferring from an exit code.** The D18 row said "the scope started",
which is true and which an operator cannot act on, so `SandboxEnforcement` now starts a
named probe scope around a two-second sleep, asks `systemctl show -p MemoryMax --value`
about the live unit and prints the manager's own answer: the byte count reads OK,
`infinity` or silence reads WARN. Two things fell out of building it. The deciding half
had to leave the Linux file — the first version of the enforcement test could not be
mutated where it runs, because on the node the manager *does* answer with a number, so a
mutant that treated `infinity` as applied survived there (an unreachable control passing
as a check). As `memoryMaxAnswer`, untagged, its eight table cases plus a four-value
negative arm run on every leg: mutating the `infinity` arm now goes red twice on the
laptop in one command. And the ARM64 leg went **red** on the first run carrying the
allocation case — `a 512 MB allocation survived a 128 MB cap (stdout "536870912")` on
Kylin — which is the platform fact arriving as a failing test rather than as prose; the
case now names the `/proc/mounts` hierarchy it needs and skips by naming it, because a
permanently red leg is where the next real failure hides.

Accepted at `68892d6` (guard + prose only after `bd6c117`): local fast gate PASS
(`639 passed, 5 skipped`, race subset 72 s, `steps not run: none`); win-devops
`OVERALL PASS` at `bd6c117` (`exit=0 duration=1m54.246s`); Linux full gate PASS at
`bd6c117` (`636 passed, 10 skipped`, `DATA_RACE_lines=0`, `FAIL_lines=0`), whose verdict
is unchanged by construction at `68892d6` because the guard passes on that host — it was
not re-run there, and that is the accounting, not an oversight. The ARM64 leg on the
in-repo script (`verify-arm64.sh --tools`, cache hit, no download) is green again:
`TOOL_CHECK=OK`, `suite_rc=0 ran=560 failed=0 skipped=19`, 19 packages `ok`,
`vendor_corruption_lines=0`, race reported as refused — `ran` up from 553 and `skipped`
up from 17, which is exactly the two cases moving from asserting to naming the hierarchy.

**The coverage sweep earned its keep again.** `cover-sweep.sh` on the node listed 26
functions at 0.0% after max-merge, and this time the triage was the point: 25 of them are
platform stubs (`attachJob`, `icon_other`, `existing_instance_other`), Windows-only code
the Windows legs do execute (`setup.*`, `isWindowsTransientIO`), the worker path that
skips when no Rust binary was built (the sweep does not build it; the full gate does —
`worker ran=21`), the entry point, and error-string helpers. The one that was a live path
with no test was `media.firstLine` at 0.0% — reached only when systemd *refuses*, i.e. the
degraded arms of D18 had never been exercised anywhere. Three stub-manager cases now cover
them (a refused scope, no `systemctl` on PATH, a `systemctl` that errors), and writing them
found a real defect: the query-failure detail was assembled from exec's own error string,
so the operator-facing text read `systemd could not be asked about the probe unit: exit
status 1` with none of the manager's words. The assertion that caught it wanted
`Host is down` in the string; the product now captures the query's stderr and quotes it,
the way the scope-refusal path has all along.

Accepted at `237c051`: local fast gate PASS (race subset 59 s, `steps not run: none`),
**Linux full gate PASS** (`639 passed, 10 skipped` — 636 plus the three stub cases,
`DATA_RACE_lines=0`, `FAIL_lines=0`, `not run: nothing`, `release_rc=0 checks=5`), and a
targeted node run of the four enforcement/refusal cases green individually
(`ran=4 skipped=0 failed=0`, the previously failing one at 0.41 s). win-devops was not
re-run: the change is a Linux-tagged file and its stub tests, and the Windows build and
suite are covered by the local fast gate at this sha.

**Rule 6 became a gate, and its first flight failed on a host fact.** `scripts/idle-check.sh`
starts a given binary on an ephemeral port with a throwaway workspace, waits for health, then
measures a window in which **no request is made** — reading VmRSS and utime+stime from `/proc`.
On this tree: `rss=18MB cpu=0ms of 5000ms (0%)`, agreeing with the hand-measured numbers
session #8 recorded (11.9 / 12.3 / 17.2 MB) that nothing had re-read since. Teeth, measured
before committing: a copy of the tree with the banned shape injected (a 50 ms ticker globbing
and walking the workspace beside the startup log) reads **6%** of the same window and fails,
naming the rule. The memory axis is the weaker discriminator (20 MB against 18 MB for that
mutant), so both numbers are printed rather than asserted equal.

Then win-devops went red at `5d6e40a` in 19 s and its log said why: that host's `bash` is the
**WSL launcher**, answering `WSL_E_LOCAL_SYSTEM_NOT_SUPPORTED` and exiting 1. The gate had
been reading "this host has no shell that will run a command" as "the idle check failed",
blaming the repository for a machine fact; `build-release.ps1` carried the same name-trusting
lookup, where a genuinely missing Git for Windows would have surfaced as a smoke failure that
never happened. `Resolve-PortableShell` (dot-sourced by both, `scripts/posh-shells.ps1`) asks
each candidate to echo a token, Git's own path first, and probes under a locally-downgraded
`ErrorActionPreference` because `Stop` would promote the refusal's stderr into a terminating
error before the refusal could be read. Contract measured both ways: a stub exiting 1 without
the token is rejected, the real Git bash is accepted, and the resolver returns Git's path.

From the same blind spot — a file type no step parses — `ps parse` runs the PowerShell parser
over `scripts/*.ps1` (6 files, floor 5, and `check.ps1` parses itself); teeth shown by
planting a broken file: `files=7 bad=[zz-mutant.ps1:1]`. And the last shell invocation in the
tree, `exec.Command("cmd", "/c", "ping … > NUL")` in the job-object test — the one place
contradicting AGENTS.md rule 2 while documenting it — is a direct `ping -n 30` now, passing
the same assertion.

Accepted at `6c0949b`: local fast gate PASS (`sh -n` 11 scripts, `ps parse` 6, idle check
`18MB / 0%`, `steps not run: none`), and win-devops PASS again (`exit=0 duration=1m58.265s`)
after the FAIL at `5d6e40a`. Whether that host's idle step ran or recorded
`idle-check(no-shell)` is **not observable from here**: the jobs-log endpoint returned 822
characters without the step and the node's ssh refuses this key. Recorded as a gap in the
evidence rather than guessed at — what was tested is the resolver's contract, not that
host's choice.

**Two "never" rules got static guards, and one of them was already broken.**
`internal/architecture` (no production code, only scans) enforces AGENTS.md rule 2 — no
shell interpreter as an exec target, no `&&`/`||` inside an exec call's string literals —
and rule 5's checkable half: the analyzer layer must not import `internal/render` nor
compose a filter graph (it may stream media to *decode*, which is what `FrameDiff` does and
what the rule allows). Both rules had already decayed in exactly the plausible way: the
job-object test spawned `cmd /c ping … > NUL` — the one file that contradicted rule 2 while
documenting it — and the crossfade fixture was built inline in an analysis test, which is
why that command line moved to `internal/testmedia`, the package whose job is composing them.

The classifiers were refined by their own first runs rather than trusted: `; ` fired on
`Run(); err != nil` in `client_windows.go`, which is Go's statement separator and not a
command string — so shapes are matched inside literals only, and redirections were dropped
(a redirect needs a shell, which the name rule already refuses; and render's filter graphs
use `|` and `;` legitimately). Then the guard fired on its own fixture tables, which are now
excluded *by name*, with the must-fire table named as the reason that cannot quietly become
an escape hatch. Each scan prints what it read (`246 Go files, 30 exec call sites`;
`41 files in analysis/event/style`) because "no findings" and "read nothing" are otherwise
indistinguishable — and one counter bug in this very session was caught the same way: my
first table-width check counted `\|` as a column and accused a healthy row of being broken.

Accepted at `e5b601b`: local fast gate PASS, win-devops `OVERALL PASS`
(`exit=0 duration=1m54.745s`), Linux full gate PASS (`643 → 647 passed`, the four new
cases, `DATA_RACE_lines=0`, `FAIL_lines=0`, `not run: nothing`). AGENTS.md now names the
guard next to rules 2 and 5, and rule 6 names the idle step that measures it — a rule
whose enforcement lives somewhere a reader has to discover is a rule that gets re-litigated.

**The caption mismatch got its third answer, and it was a product call made in the open.**
`ExportStaleSubtitles` said "burned as they stand" for every unfixable mismatch, including
the case where a plain `.srt` of the same transcript sits beside the styled `.ass`: the
`.ass` carries a frame (`PlayResX/Y`, what libass scales every pixel field by) and the
`.srt` carries none, so the plain file is laid out against the reel it lands on. Burning a
caption sized for 1280×720 onto a 1080×1920 reel is a defect; burning a plainer caption is
a degradation — so the tap now prefers the `.srt`, names the choice in its plan line, and
writes the same sentence into the log with the file it used. `subsState.plainFallback` is
the single rule the plan and the body share (that sharing is the whole reason the type
exists), and ROADMAP B5 records the decision plus the one call site to flip if the styled
look is wanted over the fit. The other half of B5's open question stays open, unchanged: a
true restyle needs the transcript payload stored, because re-wrapping inside the `.ass`
means reading the format back and a header-only rescale would keep a wrap computed for the
old width. **[Closed the next cycle, as written here: the payload is now stored, so nothing
has to be read back out of the `.ass` — see the paragraph before `## Version / HEAD`.]**

Measured on the new case: green as written (`3.11s`), and red in exactly the two places it
should be — with the body's call site stubbed out, the log assertions fail ("the body never
put \"burning the plain transcript\" on the record"); with the rule itself made dead, the
plan assertion fails quoting both sentences. Building it also caught a fixture lie worth
recording: the existing no-sidecar idiom clears `PATH`, which hides ffmpeg as well, so the
render failed with "cannot probe source for clip" and the test was about to assert against
a world that could not render at all. Narrowing `PATH` to the FFmpeg directory instead
keeps the render runnable while nothing that transcribes resolves.

Accepted at `752aaef`: local fast gate PASS (race subset 92 s — the new case runs under
`-race` in `internal/pipeline` — `steps not run: none`), win-devops `OVERALL PASS`
(`exit=0 duration=2m2s`), Linux full gate PASS (`647 → 648 passed`, `DATA_RACE_lines=0`,
`FAIL_lines=0`, `not run: nothing`, `release_rc=0 checks=5`).

The live gap this section named is closed: `worker.errResponseTooLarge.Error()` no longer
sits at 0.0%, because the refusal keeps its cause and the oversized-response case drives it
(`d1bb82d`). What the sweep still owns is the list of platform stubs and Windows-only paths,
itemised in Next Priorities 1.

**The named gap became a fix, and the re-sweep closed the loop.** `errResponseTooLarge`
was detected by the code that refused the response and then thrown away: the resource-limit
`xcerr` was built with a nil cause, so a caller could not distinguish "too big" from any
other resource limit without string-matching, and the chain that `xcerr` prints into logs
lost the reason too. `readErr` is the cause now. Two cases come with it: a Go-stub flood
that needs no python (the existing sidecar case skips without it) asserting the code, the
byte count, the surviving typed cause, and — the part a bare `err != nil` cannot say — that
the worker was actually killed, checked by dialing the port its stub published and demanding
"refused", because a timeout is also an error and says nothing about the process. The
existing `TestCallBoundedRejectsOversized` asserted only that *something* failed; it names
the code and the message now. Teeth: reinstating the nil cause fails the new case with "the
refusal lost its cause" while the strengthened sidecar case stays green — they check
different things, which is the point of keeping both.

Accepted at `d1bb82d`: local fast gate PASS (race subset 85 s, `steps not run: none`),
Linux full gate PASS (`640 passed, 10 skipped`, `DATA_RACE_lines=0`, `FAIL_lines=0`,
`not run: nothing`, `release_rc=0 checks=5`), and the coverage sweep re-run on the same
tree: **26 → 24** zero-coverage functions, with `errResponseTooLarge.Error()` and
`media.firstLine` gone from the list — the two entries this session's last two milestones
were aimed at. win-devops was not re-run: the change is in `internal/worker` and its tests,
and the Windows build plus suite ran in the local gate at this sha.

**A cancelled FFmpeg call was not over.** AGENTS.md rule 4 says nothing may be
unbounded, and the deadline bounded only the process: a tool that hands its stdout to a
helper and exits leaves the descriptor open, and exec's default is to wait for the pipe to
drain. Measured before any fix, on the node, with a 1 s budget and a grandchild sleeping a
minute: `media.Run` came back after **60.01 s — with success**, so a cancelled probe looked
like a completed one, and `StreamStdout` (the ffprobe JSON path) waited the same 60.01 s.
Four capture sites now set `cmd.WaitDelay = pipeDrainGrace` (1 s); the two driven by
tests return in 1.01 s and 2.00 s with the cancellation intact, `RunCombined` and `Version`
carry the line by construction and say so. The same assignment added to
`worker.callBounded` was **reverted with its test**: removing WaitDelay left that test
returning at 1.007 s, because the worker path reads through `StdoutPipe` and exec closes
those descriptors when the context ends — an assignment that cannot be made to fail is not
a guard, and a test that cannot fail is not evidence.

Accepted at `66f15c2`: local fast gate PASS (`steps not run: none`), Linux full gate PASS
(`643 passed, 10 skipped` — the +3 over `d1bb82d` is the two cases plus the re-exec'd
holder itself, which returns at once when the env var is unset, `DATA_RACE_lines=0`,
`FAIL_lines=0`, `not run: nothing`, `release_rc=0 checks=5`), and the targeted node runs:
red before the fix (`60.01s`, one of the two reporting *success*), green after
(`1.01s` / `2.00s`), red again with the four assignments removed. win-devops not re-run:
the file is `//go:build unix`, and the Windows build and suite ran in the local gate.

The step's other arm was proven by accident of hardware, which is worth recording: the
win-devops job log (`20260923-173821-bea374`, `623 passed, 11 skipped`) reads
`== sh -n` / `no sh on PATH — the step did not run`, and the verdict line carries
`steps not run: sh-n, race-subset`. That is the evidence the skip accounting is wired to
the thing it claims — the reason `$NotRun +=` sits at script scope in `check.ps1` rather
than inside an `Invoke-Step` scriptblock, where it would append to a local copy and the
line would go on printing `steps not run: none` on a host that skipped two steps.

**One gate red, and what it was actually measuring.** The fast gate at `c63e884` came back
FAILED on a docs-only commit: `TestCallBoundedRejectsOversized` reported
`message = "resource_limit: worker call timed out: context deadline exceeded"` after
14.84 s. Run alone it passes in 0.28 s, so the 10 s it was given had been spent starting a
python interpreter while the rest of `go test` ran in parallel — the case was asserting
which budget was hit, and the budget it hit was the machine's. It is **deleted**, not
retuned: `TestOversizedResponseIsRefusedAndTheWorkerKilled` already checks that same
`callBounded` refusal against a worker stub with a superset of the assertions (typed cause,
enforced byte count, and a *refused connection* on the port the stub published, which is the
only observation that distinguishes "killed" from "took too long"), and the `.py`
interpreter resolution the python route exercised is on the path every sidecar case takes.
The survivor's own window was closed in the same pass: its flood stub now closes stdout
after writing the oversized answer and then stays alive, so the reader decides on bytes
instead of waiting on a pipe whose drain depends on spawn cost, and the call carries the
product's default 10-minute deadline rather than a hand-picked 30 s. Measured: 5.90 s →
3.68 s green, `an oversized response reported analyzer_failure` in 5.36 s with the size
check weakened, and `a 128 KiB response was accepted under a 4 KiB budget` in 5.58 s with
both guards weakened — no mutation reaches the deadline, which is what the stub change buys.
Open in this class: the remaining sidecar cases still start real python inside a 30 s-or-more
deadline, so a saturated host can still make them slow.

**B5e: the transcript stays on disk, so a shape change stops costing a transcription.** The
sentence above closed with a decision that was still open — a mismatched `.ass` could be
fixed only by re-running Whisper through a sidecar, because the words were not kept
anywhere. They are now: transcription writes the payload it was given to
`projects/<id>/transcript.json`, a name chosen so that `ResolveSubtitlesPath` (which walks
`ass` then `srt`) can never mistake it for a caption to burn, and read back through
`subs.Parse` — the same validation the sidecar's answer passed, which is what lets a stored
file that cannot be laid out read as "there is none" rather than failing a tap that has a
plain `.srt` to fall back on. The tap gained a fourth answer, planned and executed by the
one rule `subsState` exists to hold in one place: `Action: restyle` with
`ExportRelaidSubtitles`, and it is taken **before** the sidecar arm, because the words a
restyle uses are byte-identical to the words a re-transcription would rediscover.
`writeStyledSubtitles` is the layout rule both callers share (transcribe, restyle),
including the stale-`.ass` removal that keeps an emptied transcript from shadowing its own
`.srt`; `restyleSubtitles` is unexported because the tap is its only caller. The web readout
names the new verb (`relaid` / 按新画幅重排) rather than falling through to the raw word.

Measured on the two new cases, both green as written (`3.27s`, `3.47s`) and red where they
must be: hide the payload and the plan drops to the plain-transcript answer with the
ill-fitted file untouched; keep the promise in the plan and withhold it in the body and the
assertion quotes `captions after the restyle declare 1920x1080 … want the reel's
1080x1920`. Both also assert the files the restyle did not use — transcript and `.srt` —
are the bytes they were, so "re-laid out" cannot quietly mean "re-transcribed". What is
still open here: the stored payload is only as good as the transcription that wrote it, so
projects captioned before this change restyle only after one more run; and a mismatch with
no captions at all still goes to the sidecar, which is the one case where re-transcribing
is the only way to get words at all.

Accepted at `4e039e1`: local fast gate PASS (`steps not run: none`, tests skipped 5), the
Linux full leg on `linux-ci` against a 1.0 MB `git archive` snapshot of that sha — `== gate
(full): PASS` with `649 passed, 10 skipped`, `not run: nothing`, and the race, cross-compile,
cargo fmt/clippy/test and gosec steps all present in the step list (the +1 over `752aaef`'s
648 is the two caption cases minus the deleted stopwatch case) — and the `win-devops` node's
own report row for XCut: `PASS`, `2m26.317s`, the same duration the control plane printed,
with local evidence claimed as `after_local_pass: local CI PASS at 15:50:35 for 4e039e19`.
The previous commit (`b4e1bf8`, the test-stability cycle) was accepted on the local fast gate
only — it deletes a test and changes a test stub, and no Windows- or Linux-only code is
reached by either, so its two deeper legs were not re-run rather than passing unmentioned.

**The installer that puts a binary on PATH had never been measured.** The same sweep that
cleared the caption work's remainder (`4e039e1`, 17 functions at 0.0% after max-merge — down
from 24) left five in `internal/setup`, and reading them found two defects no test could
have caught because the code they sit in was never run: `fetchPinned` fetched
`FFmpegPin.URL` while `Status.Source` was filled from the resolved pin, so `Pin.URL` — the
field D17's digest discipline and the UI's own "where did this come from" both rest on —
was decorative; and `verifyFFprobe` took a zero exit status for identity, which publishes a
file that runs happily but is not ffprobe as an installed tool. Now the fetch uses the URL
of the pin in force (and a nil `Fetch` means that fetcher, not a missing one), and the
verify step must hear the tool call itself ffprobe — recorded as D19, including the part it
does not cover: a banner is a string, so identity here is a defect-catcher and provenance
stays with the SHA-256 and byte-count gate ahead of it. Both are exercised by running this
test binary as the tool under verification (env var picks the answer), which is the
repository's existing idiom for "a real executable that answers on command" without a
shell, a compiler call or a committed fixture; the fetch cases serve from a local
`httptest` source, so no test in this package can reach the network.
`internal/setup` reads **85.6%** statements with the five gone, and three mutations were
caught by the cases that replaced them: identity check disabled → `a file that runs and
says it is not ffprobe was accepted`; the 99% progress clamp removed → `reads
99.98017942447866, want the clamped 99`; a fetcher bound to another URL → both fetch cases,
in 0.02 s against a refused local port rather than an external host. One 0.0% left in the
package by a single-binary profile is `NewFFmpegInstaller`, which is reached through
`cli/serve.go` and covered under the sweep's max-merge — not a gap, and the sweep is the
denominator that says so.

**The sibling binary was the same defect, one file over.** Having just read the verify step,
the asymmetry was visible without another sweep: `extractTools` installs `ffmpeg.exe` beside
`ffprobe.exe`, every render execs the first, and the check asked only the second — so the
one thing the previous cycle's fix was about (a file that runs and is not the tool) stayed
uncaught on the side that actually carries the user's arguments. `Verifier` takes both paths
now and `verifyTools` asks each to name itself; the wrap that used to say "installed ffprobe
did not verify" says FFmpeg and lets the cause name the tool, because half the time the old
sentence would have been wrong.

Tested in the direction that isolates it: a missing `ffmpeg.exe` next to a `ffprobe.exe` that
answers perfectly must refuse and must name ffmpeg — and removing the ffmpeg entry from the
loop is caught by exactly that case (`an ffmpeg that does not exist was accepted because
ffprobe answered`), while the three cases that only exercise the ffprobe slot stay green. The
pair's other direction (the ffprobe slot answering as ffmpeg) and the installer handing both
extracted paths to the verifier are covered beside it. `internal/setup`'s `Verifier` seam is
now two-argument, which is the only reason the four existing injections changed.

**The mismatch becomes something a client can see.** The restyle the tap does at export was
invisible everywhere else: the panel that lists caption files knew only that an `.ass`
existed, so a project whose captions were sized for another frame looked exactly like one
whose captions fit. `GET /api/v1/projects/{id}/subtitles` now answers with the four facts
(`styled_frame`, `reel_frame`, `mismatch`, `transcript`) and `POST …/subtitles/restyle` does
the repair without a sidecar and without a render, returning the same body so the caller
sees the frame it asked for. `pipeline.CaptionState` and `RestyleSubtitles` were exported for
it — a second reader re-deriving the frames would be the disagreement `subsState` was built
to prevent, and the endpoint is a real caller, not a test seam. `storedTranscript` changed
shape in the same pass: it used to fold "no file", "unreadable" and "fails validation" into
one `nil`, which is the right answer for a tap that has a `.srt` to fall back on and the wrong
one for a client asking directly; it now returns the error and `HasStoredTranscript` is the
tap's question. The new case `TestExportDoesNotPromiseARestyleItCannotPerform` pins the
difference (a truncated payload plans the plain-transcript answer, not a restyle) and goes
red when existence is mistaken for usability.

Writing the status assertion before the handler was finished caught a second defect: the
state type returned early when a project had no caption file, so a project with a 1080×1920
reel and nothing transcribed yet reported `reel_frame: ""` — correct for the tap, which has
nothing to compare, and a withheld fact for a status endpoint. The reel's canvas is read
unconditionally now and `mismatch` still says false. That one was not a mutation catch; the
fixture's own claim failed first, which is the cheaper way to find it.

**A transcript on disk is a caption source.** The state "transcribed once, caption files
since removed, no sidecar configured" answered `subtitles=skip` although the words were
sitting in `transcript.json` — the tap had learned to *repair* captions from them but not to
*make* them. It now does, planned as `create` with its own sentence (`written out from the
transcript this project already keeps, no sidecar needed`) and executed by the same
`writeStyledSubtitles` the repair uses, sized for the reel's canvas. The precedence is
deliberate and asymmetric with the mismatch arm: with a sidecar configured the plan still
promises a transcription, because a stored payload cannot say whether the project's asset is
still the one that was spoken over — a mismatch asks only about the frame, where the words
are already agreed. Two cases hold the line: disabling the body arm leaves `no styled
captions were written` while the plan goes on promising them (the plan/body split is what
the tap has been wrong about before), and the sidecar case asserts the new arm did not take
it over. What the rebuild does not do is bring the `.srt` back — the styled file is what
burn prefers, and the case says so rather than leaving it to be assumed. Still open in this
arc: a project captioned before the transcript was stored has no payload to rebuild from, so
it keeps needing a sidecar exactly as it did.

## Version / HEAD

- Version: 0.1.0-dev (release artifacts stamped via ldflags); **v0.1.9-alpha tagged
  at `48d0fe8` (2026-09-23, annotated)** and published with seven assets — the three
  platform binaries, the Windows zip, the setup exe, the static musl worker and a
  `SHA256SUMS-v0.1.9-alpha.txt`. Both Linux binaries were executed on the machines
  they target before the release went out (see the session paragraph below).
  `.github/workflows/release.yml` no longer listens for tags: it never completed a
  run, and the release path is local.
- HEAD: session #20 (2026-09-22, Phase 5 through B5c) — the auto-edit arc landed
  on all three channels: `卡点` music carried from CLI to API to render, two new
  styles plus `clip_order`, a pacing readout measured rather than eyeballed (CLI,
  API wire keys, UI chip), the bed and the beat-snap selector in the browser, a
  beat grid that fits a three-minute bed instead of refusing it, and captions
  sized, wrapped and held for the reel they land on — with or without word
  timings. Each milestone's verdict lines are in the sections above; the bullet
  below is the previous head, kept as history.
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
- Per-child memory cap (D18): six Linux cases in `internal/media` — no cap means no
  wrap; a missing `systemd-run` degrades to unwrapped children with the refusal named in
  the posture; a wrapped child's stdout is *byte-compared* (the wrapper must not write
  there) and its exit status propagates; the argv shape around the `--` is pinned;
  `TestCapKillsAChildThatOverrunsIt` measures the claim itself (512 MB under a 128 MB
  cap, killed by the cgroup), and `TestSandboxEnforcementAsksTheManager` reads the limit
  back off a live unit. The two allocation cases sit behind a `/proc/mounts` check for
  `cgroup2` at `/sys/fs/cgroup` and skip naming the hierarchy, because on Kylin's hybrid
  layout systemd attaches nothing — the cap case ran there first and **failed**, which is
  how the platform fact got into the ledger. Teeth: a mutant that stops wrapping fails
  with `a 512 MB allocation survived a 128 MB cap`; a mutant that reads `infinity` as
  applied fails twice, locally, on any platform, through the untagged table below.
  Three more cases drive the degraded arms with a stub `systemd-run`/`systemctl` on PATH
  (a refused scope, no query binary on PATH, a query that errors), asserting the words
  an operator would read rather than merely a non-nil error — one of them found that
  the query-failure detail carried none of the manager’s output at all.
- What systemd *attached* is a separate measurement (`memoryMaxAnswer`, D18): the byte
  count we asked for is applied, `infinity` and an empty answer are not, a clamped number
  is named back. It is a pure function in an untagged file on purpose — the decision is
  not platform code, so the eight table cases and the four-value negative arm run on
every leg (the fast gate included)
  rather than only where a session bus exists. Mutating the `infinity` arm to "assume the
  exit code meant it" goes red twice, locally, in one command.
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
- The Rust worker's decoder does not take XCut's own media shape, and the reason is
  narrower than this line used to say. Measured 2026-09-23 against the built worker:
  an H.264+AAC MP4 (what `testmedia`, an import from a phone, or a screen recorder
  produces) fails in symphonia as `unsupported feature: aac: predictor data`, while
  the *same* encoder settings in a bare `.m4a` decode fine (`ok: true`, −21 dBFS
  windows) — so it is not "ffmpeg-encoded AAC", it is the AAC stream as the isomp4
  reader hands it over next to a video track. A second, unrelated class showed up the
  same day: a PCM `.wav` fails earlier, at the probe (`end of stream`), because the
  crate's feature list is `isomp4, aac, mp3, flac, pcm, adpcm` and has no wav
  demuxer. `workers.audio: auto`'s ffmpeg fallback covers both, and
  `TestAutoModeCoversTheWorkerCodecGap` now provokes the first one on purpose — with
  its premise asserted, so the case cannot start passing on nothing if the decode
  ever works upstream.
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
  length is; **the arithmetic for the 60 s default as it shipped then**: 473 s of
  rally time in the match bounds recall at 60/473 = 0.127, and the committed state
  measures 0.112, i.e. 88% of what that budget allows — which is why the budget,
  not the scoring, is what session #20 raised: at 120 s the same arithmetic allows
  0.254 and the reel measures 0.243 (96% of it).
  Decomposing the reel (inside a rally / adjacent to
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
  P 0.886 → 0.813) while the 60 s default of that day is bit-identical. Precision
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
  Consequence for verification: **closed 2026-09-23, by measurement rather than by
  argument.** `scripts/fetch-arm64-ffmpeg.sh` pins a stock arm64 build (FFmpeg
  9.0.2 `linuxarm64-gpl` from the immutable tag `autobuild-2026-09-20-13-11`), checks
  size and SHA256 before extracting, checks `xfade` is present after, and refuses a
  mismatch instead of running something else. The whole suite then passes on that same
  Kylin box against the pin: `ran=553 failed=0 skipped=17`, 19 packages `ok`,
  `vendor_corruption_lines=0`, with a `TOOL_CHECK` step asserting that PATH resolves
  the pinned binaries — because that assertion is exactly what the first run lacked
  (see session #18). What the pin does not buy: `-race` still cannot start there
  (`ThreadSanitizer: unsupported VMA range — Found 39, Supported 48`, a kernel limit,
  not a product failure), so **ARM64 is verified without the race detector**, and race
  coverage stays on the Linux x86 full leg plus the fast gate's `-race` subset. The
  digest is not publisher-signed (GitHub publishes none for that asset); the script
  states where the hash came from.
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
- **Armed is not enforced on a hybrid-cgroup host** (measured 2026-09-23, D18). On
  Kylin V10 SP1 (systemd 245; `/sys/fs/cgroup` is a tmpfs of v1 controllers plus
  `unified` at `/sys/fs/cgroup/unified`) `systemd-run --scope -p MemoryMax=100M` exits 0
  and the child allocates 400 MB and **lives**, because the manager never attached the
  scope: `systemctl show <unit> -p MemoryMax --value` reads `infinity` and
  `ControlGroup` is empty. On the cgroup v2 hosts the same property binds — 512 MB under
  a 128 MB cap dies with SIGKILL and the wrapper propagates 137. What the product claims
  is therefore deliberately weaker than what a user would like: `doctor` says the scope
  *started* (the half it can see), OPERATIONS.md carries the two commands that measure
  the other half. `xcut doctor` now asks `systemctl` directly — it starts a named probe
  scope, reads `MemoryMax` back off the live unit and prints `infinity` / an empty answer
  as a WARN — so the Kylin-shaped case is visible to whoever runs the binary, not only to
  the test suite. What remains open is the container/seccomp rung of ROADMAP Phase 4.

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
   correction: 46, of which the ones still worth work were the CLI command
   (`cmdVersion`, `cmdConfig`, `usage`) and serve-lifecycle
   (`startServeCore`, `shutdownServe`, `newServeLogger`) paths, and the
   environment-conditional ones (`analysis.RustAudioAnalyzer.*` need the built
   Rust worker; `setup.*` runs only on Windows). Platform stubs
   (`*_other.go`) and interface shims (`Error`, `String`, `Name`) are not gaps.
   **The serve-lifecycle half and the CLI half are both now closed**
   (`0769849b`: four cases driving the real start/sweep/drain path — where the
   closed-at-startup `serve.log` was found; `4dd32490`: `config show`/`config path`,
   `init`, `version` and the usage page run through `Run`, which is where the two
   token-handling claims in docs/SECURITY.md got their checks). What is left of this
   item is the environment-conditional pair, and the Rust half of it was a *gate* gap
   rather than a writing gap: `analysis.RustAudioAnalyzer.*` and `internal/worker`'s
   three media-worker cases need the built binary, and no leg had one — `cargo check`,
   `clippy --all-targets` and `cargo test` all leave it in `target/debug/deps/`, while
   the tests look for `target/{debug,release}/xcut-worker-media`. Closed at `983596f`
   (both scripts build it before `go test`; the local fast gate went 609/8 → 617/5 as
   those three started running). Followed up at `fabe46d`: the sweep was re-read
   function by function, three orphaned helpers were deleted (two of them with a
   comment naming a caller that does not exist), and the two live leftovers — the
   streaming shim and the non-Windows `xcut client` — now have cases that run on the
   leg that can compile them. Two further entries left the list at `237c051`/`d1bb82d`:
   `media.firstLine`, reached only when systemd refuses, and `errResponseTooLarge.Error()`,
   which could not run because the refusal dropped the cause that would print it. The sweep
   now reads 24 entries, and what is left of this item is `setup.*`, which is

2. Real-footage evaluation — **one match is done, and that is the limit of what
   can be concluded.** 43 rallies were derived from the burned-in scoreboard and
   the provisional constants swept against them (docs/EVAL.md carries the
   negatives). What remains is not more tuning on this footage: every
   audio-driven idea dies on the same wall (shared six-court hall), and the
   ceiling arithmetic says the budget is what limits coverage — the 60 s default
   reached 88% of its bound, and the 120 s default that replaced it reaches 96% of
   its own, so the remaining error is placement inside rallies, not length.
   The next useful measurement needs a *different* input — ideally a
   single-court recording, so "our strokes" and "the hall's strokes" stop being
   the same signal. Owner-supplied footage, or the vision sidecar.

3. Two decisions that were the owner's, not mine — **both taken 2026-09-23**.
   **(a) arm64 in CI** — decided: pin a stock arm64 build the way the Windows one-click
   pins Gyan.dev. Implemented as `scripts/fetch-arm64-ffmpeg.sh` and measured green on
   the Kylin box (Known Issues, ARM64: `ran=553 failed=0 skipped=17`). What is still
   *not* done: the leg is a command, not a gate — `sh scripts/verify-arm64.sh` is the
   durable form (reproducing those counts from a fresh snapshot), but no scheduler runs
   it, and 4 of that box's 17 skips are the Rust worker cases (no cargo on the Kylin
   host). Wiring it into the control plane is now one dispatch, not a decision.
   **(b) release cadence** — decided: cut v0.1.9-alpha now rather than bundling it with
   the next batch. Done: `v0.1.9-alpha` tagged at `48d0fe8`, seven assets published,
   both Linux artifacts executed on their target platforms, digests verified through the
   public download path.

4. Subtitles with a real Whisper: install faster-whisper locally and run
   `xcut subtitles` on real singing content (the plumbing is tested; the
   model load is deliberately not night work).

5. Re-enable push/PR CI if the GitHub account's billing situation ever changes — with
   one correction so nobody "restores" something that was removed on purpose: `ci.yml`
   is `workflow_dispatch`-only, and **`release.yml`'s `push: tags: ["v*"]` trigger was
   deleted at `48d0fe8`**, not merely starved by the quota. It had never completed a run
   (every attempt from v0.1.1 to v0.1.8-alpha died in its own cargo step) and it cannot
   build the two Windows assets a release carries. The job is still there, hand-runnable
   against a named tag; the release path is local (`build-release.ps1`/`.sh` →
   `make-installer.ps1` → `make-setup.ps1` → `gh release create`). Do not re-add the tag
   trigger as part of "restoring CI".

6. Phase 4 leftovers: tray/auto-update and model registry — need
   maintainer decisions; desktop packaging itself (zip, icons) shipped
   in session #8 and the true installer in session #13.

7. TLS for the API, or an explicit statement that the tunnel recipe in
   `docs/OPERATIONS.md` is the answer: D12's bearer token crosses the wire in
   plaintext, which is why remote binds are documented as trusted-network or
   tunnel-only today. Owner-level call, and the only survivor of the old
   remote-access thread.

