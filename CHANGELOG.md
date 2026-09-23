# Changelog

All notable changes. Format loosely follows Keep a Changelog. The released
sections are tagged; anything above the newest one is unreleased.

## [Unreleased] — after v0.1.9-alpha

### Improved
- **The ARM64 verification is a script in the repository now.** `sh
  scripts/verify-arm64.sh` fetches the pinned stock FFmpeg, puts it on `PATH`, proves
  the suite is resolving *it* (`TOOL_CHECK` asks `command -v` and reads the version
  string back), runs `go test -v ./...` and reports the kernel's refusal of
  ThreadSanitizer as a line rather than as a green step. The two ways the hand-run got
  it wrong are structurally excluded: injecting the toolchain through
  `XCUT_FFMPEG`/`XCUT_FFPROBE` (which the suite never reads — it resolves ffmpeg and
  ffprobe by name, so that run reports 48 vendor-build failures and says nothing about
  the pin), and reading a fetch script's status through a pipe (`| tail -1` reports
  tail's 0, which is how a pin stops being a pin). Verified on a fresh Kylin V10 SP1
  aarch64 snapshot from a from-scratch download: `suite_rc=0 ran=553 failed=0
  skipped=17 packages_ok=19`, `vendor_corruption_lines=0`, `race_rc=1
  tsan_refused_lines=3 data_race_lines=0`.
- **The gate parses the shell the release path is written in.** `gofmt`, `go vet` and
  `go build` cannot see a typo in `scripts/*.sh`, and the release path — build, smoke,
  FFmpeg pins, the gate's own POSIX twin — is shell. Both twins now walk the directory
  with `sh -n`, name every offender, and carry a floor: fewer than five scripts found is
  an error, because a step that read nothing must not report a pass. A host with no
  `sh` records `sh-n` under `steps not run:` instead of pretending. Measured teeth: the
  ten shipped scripts parse (0 red), and one deliberately broken `if [ x = y` turns
  exactly one red.

## [v0.1.9-alpha] — tagged at 48d0fe8 on 2026-09-23, 190 commits / 172 files since v0.1.8-alpha. Session #20 (Phase 5: beat grid, captions, one tap)

### Added
- **The one-shot learned to caption (`xcut auto --subs=on`, `--subs=<file>`).** The
  command that goes from a file to a reel stopped one step short of the thing a
  platform takes: it rendered without captions, and closing that meant running
  `xcut subtitles` between two halves of the same command. `--subs=on` transcribes
  this run's first input **after** the timeline is built, so the caption box is styled
  against the canvas the same run declared; `--subs=some.ass` burns a file the caller
  already has, which is what `xcut render --subs` takes. A run without the flag is
  unchanged, and a run whose transcript fails says so on its own line rather than
  rendering an uncaptioned reel and calling it done.
  Verified: two cases on generated media with a fake sidecar — the flag's run leaves
  one `subtitles.ass` in the project, declaring 1920×1080 for `generic_highlight`'s
  canvas with no karaoke tags (nothing timed a syllable), and rendering the reel; the
  flagless run writes no caption file at all; a machine with no sidecar on the narrowed
  `PATH` fails with the command's own report naming the missing piece. Two mutations:
  the flag never reaching the switch (both cases die), and a swallowed transcript
  error (the reporting case dies — which it did not at first, because the job logger
  echoed the cause into the same stream the assertion was reading).
- **A camera-motion picker for one clip, not one for the whole style (ROADMAP
  Phase 5, B6c).** The inspector's clip panel now offers 运镜 per clip — still,
  punch in, drift, follow the region — and asks the server what each of those
  means rather than computing it in the page. `style.MotionFor` is the single rule,
  and `framingPlan` is now a thin caller of it, so the geometry a hand pick produces
  is the geometry the builder would have produced for the same mode, zoom and
  region; the endpoint reads the region off the asset row for the same reason.
  Nothing new is stored: the reply carries `motion` plus the `framing` claim, the page
  puts both on the clip, and the ordinary Apply → PUT round trip keeps its revision
  check and its pre-regeneration backup. `none` answers with no window and no claim,
  because a clip that says it is framed while showing the whole frame is the same lie
  the style tests already refuse.
  Verified: eleven new cases — six in `style` (each mode's numbers, the drift
  alternation by ordinal, a region hanging off the frame edge still centring inside
  it, the refusals naming what to do, and the builder and the picker agreeing by
  construction across every mode × ordinal × has-region combination) and five at the
  api (the wire keys and the default window, the aim taken from the region row, the
  refusal without one, `none` claiming nothing, another project's asset being a 404),
  plus a guard that the page writes geometry and claim together. Six mutations
  replayed, each killed by a named assertion: drift stops alternating, roi without a
  region invents a centre, the framing claim is never answered, the zoom bound is
  dropped, the builder stops asking the shared rule, and the page writes the geometry
  without its claim. Not verified: no browser was opened — the select's on-screen
  behaviour rests on `node --check` and that text guard.
- **One tap to a post-ready reel — `POST /api/v1/projects/{id}/export` and the
  ★ button in the timeline pane (ROADMAP Phase 5, B6d).** The sequence a user
  otherwise clicks through (generate the reel, transcribe, burn, render) became
  one job that builds the timeline if the project has none, transcribes if
  captions were asked for and none exist, and then **queues** the render. It
  waits for no other job on purpose: a parent that blocks holds one of two queue
  slots, so two taps would deadlock each other, and a render run inside the tap
  would escape `resource.max_render_workers`. The response carries the plan —
  each stage as `reuse`, `create` or `skip` with a reason — because the stage a
  machine cannot do (no sidecar to transcribe with) is exactly the one that must
  not be a silent absence. Two side effects worth naming: `export` joined the
  exclusive job set (migration v7 rebuilds the partial unique index, so a second
  press is a clean 409 rather than two reels), and building the reel before
  transcribing means the captions are styled against a canvas that did not exist
  when the request arrived. Default style for the tap is `beat_shortform`.
  Verified: eight new cases — three through the real pipeline on generated media
  (a file on disk, the `.ass` declaring 1080×1920 for the reel the same tap
  built, one export row and one render row), one with no sidecar on `PATH`
  (the tap succeeds and says why there are no captions), one that hand-writes a
  horizontal reel and a sentinel `.ass` to prove a reused stage leaves both byte
  for byte — four at the api on the wire keys, and one that fails if
  `exclusiveTypes` and the storage index ever disagree. Five mutations replayed,
  each killed by a named case: the body always rebuilds the reel, the body
  always transcribes, the skip stops naming itself, the render runs inside the
  tap (`one tap wrote 1 export and 0 render rows`), and `export` leaves the
  exclusive set.
- **A transcript without word timings gets the styled caption file too (ROADMAP
  Phase 5, B5c).** Which artifact a burn-in drew turned on a sidecar detail
  nobody chose: the same words with per-syllable timings got the designed
  `.ass` frame, without them they got an `.srt` and whatever libass defaults
  to. Only the karaoke fill needs syllables — the frame, the wrap and the
  dwell need the line and nothing more — so both writers now run the same
  layout and the plain one just leaves the sweeps out. `xcut subtitles
  --ass` answers an ordinary transcript with `Style: Caption,` instead of
  "karaoke output needs them", and a project's second transcription
  *replaces* the karaoke file the first left behind rather than only
  deleting it, so a karaoke artifact can no longer shadow a newer, plainer
  transcript. Verified: three api cases (a plain transcript's `.ass` names
  the project's canvas; a re-transcription leaves no `\k` behind; the
  karaoke pass is unchanged), one CLI case pinning the exact cue line
  `Dialogue: 0,0:00:00.50,0:00:01.70,Caption,,0,0,0,,你好` — the dwell, not
  the speech length — and `TestPlainCuesTileTheSegmentsTime`, which paid for
  itself before the code was finished: the span was first shared per cue
  instead of cumulatively, and the file it caught showed
  `0:00:03.60,0:00:03.60` — two lines of text given no time on screen.
  Four mutations replayed, each stopped by a named
  assertion: the plain transcript writes nothing (`the second pass left no
  .ass at all`, and the plain-transcript case with it), the write refuses to
  clobber an existing file (only `TestReTranscribeReplacesTheKaraokeFile`
  notices), the caption writer ignores the caller's canvas (`caption .ass
  lacks "PlayResX: 1080"`), and the CLI branch calls the karaoke writer
  (`subtitles --ass failed (1): xcut subtitles: transcript has no word
  timings`).
- **`analysis.EstimateBeatGrid` — the `卡点` foundation (ROADMAP Phase 5, B1).**
  A beat grid inferred from the onset track: the onsets are folded modulo each
  candidate period (30–300 BPM scanned geometrically), the single phase explaining
  the most of them is kept, and among the periods that explain ≥90% of the onsets
  the *longest* wins — a drummer hitting every beat and one hitting every other
  beat are the same evidence, and the estimator does not get to invent the
  half-beat it never heard. Two least-squares passes over the integer beat index
  then pull the period off the 2% candidate ladder, because a period that is
  slightly long drifts off the real beats over a long file: the grid would fit the
  first clicks and miss the last. Beats stop at the last onset plus half a period,
  not at the requested horizon, so a reel is never cut to beats a faded-out tail
  never played.
  It is a pure derivation rather than a fourth `FeatureTrack`: from the cached
  onset track it costs microseconds, so a cache entry would be one more thing to
  invalidate for no measurable gain. Nothing consumes it yet — B2 does.
  Verified: five estimator cases plus one through the real chain
  (`testmedia.GenerateRally` clicks at 0.5 s → the shipped `AudioOnsetAnalyzer` →
  the estimator), reporting `period=0.4999 bpm=120.0 coverage=1.00 beats=24` —
  0.02% off the constructed grid. Replaying four mutations, each one is stopped
  by a named assertion: the 4-onset floor (`[1 1.5] produced a grid … want a
  refusal`), shortest-instead-of-longest (`period = 0.2500, want 0.5000`), no
  refinement (`beats = 12, want one per second across 13 s`), and trusting the
  horizon (`grid extrapolates past the last onset: last beat 12.000, last onset
  11.500`).
- **`diversity.min_per_window` — the other half of the spread rule.** The ceiling
  caps a window; it never asks an empty one to be filled, so a reel may put two
  clips in each of two windows and leave the other three untouched. The floor
  takes each window's first clip before any window takes a second, and the fill
  pass that follows is the ordinary greedy loop unchanged (unset knob = today's
  ranking, byte for byte — two pre-existing window tests failed the moment that
  was not true). `TestPhaseFloorTakesAnEmptyWindowBeforeDoublingUp` carries both
  halves: a positive control that asserts the floorless reel *does* cluster, and
  the floored reel covering every window at the same clip count. Disabled by
  mutation (`if false && !withinFloor(...)`) it fails naming the empty window.

### Changed
- **The badminton preset's default reel is 120 s, not 60 s** (owner decision:
  defaults may be raised). Measured on the owner's match, both with and without
  the new floor, four points on the ladder: F1 0.225 → 0.389 → 0.455 → 0.562 and
  longest missed run 10 → 4 → 2 → 2 rallies across 60/120/180/240 s, with
  precision flat (0.97–0.99) the whole way and 16/16 clips ending on a measured
  point at the new default. What the match's own rally arithmetic allows is
  120/473 = 0.254 recall; the reel delivers 0.243, against 88% of its bound at
  60 s. Render cost at exactly this length was already measured (17.8 s wall for
  105.7 s of output, 0.17x). Generic presets stay at 45 s: nothing annotated in
  generic content has been measured.
- **`docs/EVAL.md` loses a wrong attribution.** The ten-rally missed run was
  blamed on the missing floor. Adding the floor moved it by zero and the 60 s reel
  by one rally (7/43 → 8/43 at unchanged precision), so the ceiling had already
  spread those picks and the gap sits inside windows that do have a clip — the
  budget is what binds, which is why the default moved and not the quota.

- **`卡点`: clip ends may follow the beat grid (`--beat-snap`).** With
  `beat_snap_tolerance` set (preset) or asked for per run (`--beat-snap 0.12`,
  `--beat-snap off`, `"beat_snap"` on the API's timeline body), a clip end that
  nothing else has fixed moves to the nearest beat of the source's own grid. Three
  things bound it: it may not pass the material the detector attributed to that
  event, may not lengthen the clip past `max_clip_duration`, and may not touch an
  end a scoreboard mark already owns — the measured point is worth more than the
  pulse, and the tolerance is capped at 0.5 s and below `min_clip_duration` so a
  snap can never empty a clip. Each moved end records `beat` in its metadata at
  four decimals (a refined 0.5 s grid really sits at 7.5028; two decimals would
  disagree with the geometry it documents), and eval prints `beats N/M` only when
  the grid actually moved something.
  Proven through the real path, not a fixture handed to the selector: a 120 BPM
  click rally → the shipped `AudioOnsetAnalyzer` → the analysis cache → selection,
  asked for twice with the rule off and on, with a control that the ends *start*
  off the lattice. Measured on the owner's match, it is inert — and the reason is
  now known instead of assumed: with marks, all 16 ends of the 120 s reel are
  point-pinned (precedence working); without them, the hall's 1493 onsets carry no
  grid worth believing, which is B1's refusal rule doing its job. Same F1 at off,
  0.12 and 0.25 s. The consequence is written into `docs/EVAL.md` and moves B4:
  卡点 needs a music bed, because a location recording has no pulse to cut to.

- **Camera motion (`运镜`): a per-clip framing plan, and the renderer crops to
  it.** A clip may carry `{"motion": {"zoom": 0.8, "from": [x,y], "to": [x,y]}}` —
  a window of the source sized to the canvas's aspect, magnified to fill it, whose
  center slides over the clip's own time. Absent means the whole frame, which is
  what every existing document carries, so an old timeline renders unchanged.
  Styles ask for it as `camera_motion: {"mode": "punch_in" | "drift" | "roi",
  "zoom": …}`; `drift` alternates the pan direction by clip position so a reel is
  not one metronome, and `roi` centers the window on the region the project was
  analyzed with. A 9:16 canvas over a 16:9 source is the vertical reframe from the
  same arithmetic.
  Proved at three levels, because the filter string alone proves nothing about the
  picture: the expression text (window width carries the canvas aspect, a drifting
  axis carries `t`, both axes are clamped at both edges, a still plan carries no
  time term); the command line, read back from the child's argv through `Render()`
  against the package's stand-in FFmpeg — including that a clip with no plan grows
  no `crop` stage; and the pixels, on a fixture whose only content is the top-left
  quadrant: YAVG 94.17 → 19.24 as the window drifts to the far corner, against
  39.25 → 39.36 with no plan. Four policy mutations and four renderer mutations,
  each killed by the assertion named.
  Cost measured on the match's own default reel: **8.6 s against 8.1 s** of render
  wall for 60.02 s out, and **16.57 MB against 14.86 MB** (+11.5%) at a fixed CRF —
  the size, not the time, is what a plan buys. No shipped preset enables motion:
  cropping a broadcast can cut the scoreboard out of the shot, and there is no
  measured aesthetic claim to trade against that yet. What is *not* claimed: a plan
  centered on the ROI does not guarantee the whole region stays in frame — the
  selector does not know the source's pixel aspect, only the renderer does.
- **卡点音乐: a run can name a music bed, and the reel cuts to it and carries it.**
  `--music track.mp3` (CLI) or `"music"` on the timeline API analyzes that file with
  the same onset analyzer and cache as any asset, estimates its beat grid, and lets
  that grid — not the location audio's — decide where cuts land; naming a bed
  implies the product's ±0.12 s snap, and `--beat-snap off` opts out while keeping
  the music. The render loops the track and mixes it under the clips at
  `audio.music_gain` / `audio.source_gain` (defaults 0.9 / 0.35, both bounded to
  (0,1]), copying the video stream so a bed costs no second encode. What the reel
  chose is recorded in its own document (`music`, `music_gain`, `source_gain`,
  `music_bpm`), so `xcut render` needs no repetition — and a document whose track
  has moved fails the render instead of quietly returning a musicless cut.
  Measured against synthetic truth: footage clicking at 0.4 s under a bed at 0.5 s,
  whose single free end moved to the bed's lattice (1/1 moved, 0 on the footage's),
  `music_bpm` 120.04; and the mix is audible *in the output file* — 9 transients
  only the bed's grid explains, against 0 in the same cut rendered without one, at
  an unchanged duration. Cost: 11.3 s against 10.2 s of render wall per minute of
  output, +3.3% bytes. A bed with audio but no believable grid is not an error: the
  music plays, the cuts stay put.
- **A pacing readout: the reel's shape is now measured, not eyeballed.**
  `xcut timeline` and `xcut auto` print `pacing: N shots, mean …s, median …s,
  longest …s, top shot starts at …s`, computed by `timeline.Pacing` from the
  document that was just built. Every metric the harness has is set-based — one
  15 s stretch and five 3 s cuts score identically — so until this line existed,
  "new style, same F1" was indistinguishable from "new style, different edit".
  The hook number is the shot's **output** position rather than its source time
  (a reel that reports when a rally was filmed answers a different question), an
  unparsable score is not read as 0, and a hand-edited document gets its lengths
  and no claim about a top shot. Verified: 7 tests over real documents — an
  equal-length control, a half-speed clip so source span ≠ played span, an
  out-of-order score tie, the unscored and empty cases — and 7 mutations, each
  killed by the assertion it targets (`median = 6, want 3.5`, `HookSeconds = 900,
  want 200`, `ScoredShots = 3, want 1`). What it says about the shipped preset on
  the owner's match: `14 shots, mean 8.0s, median 8.0s, longest 8.0s, top shot
  starts at 24.0s`. Every shot sits exactly on `max_clip_duration`, so the
  preset's ceiling is what sets its pace — not its scoring — and the best moment
  arrives a quarter of the reel in. Those are B4c's targets, written down before
  anyone picks a number for them.
- **Two new styles, and the ordering knob one of them needed.** `clip_order` says
  how the shots a style chose are arranged: unset/`chronological` plays them in
  match order (what every preset written before this means today), `hook_first`
  leads with the reel's own top-scored shot and leaves the rest in match order. A
  name that is neither is refused when the preset loads — a typo would otherwise
  keep the old order while the style file promised a hook, and the pacing line
  would be reporting a rule nobody wrote. Two presets use what B1–B4a built:
  `sports_vertical` (9:16 canvas, a 4.0 s ceiling, `roi` framing that produces **no
  plan at all** on a project with no analyzed region rather than guessing a centre
  crop over broadcast footage) and `beat_shortform` (1080×1920, 1.0–2.8 s shots,
  ±0.12 s snap, hook first). Both are embedded, so the API style list and the UI
  picker carry them without a hand-kept registry.
  Measured against the incumbent at the *same* 60 s budget on the owner's match:
  15 clips vs 8, recall 0.100 vs 0.108, precision 0.785 vs 0.850, and the longest
  run of annotated rallies the reel never touches **4 instead of 10**. Its
  `ranges_hit` is lower (4 vs 6) and that is the yardstick rather than the edit: a
  hit needs IoU ≥ 0.3, and a shot inside a rally scores `len(shot)/len(rally)` —
  4 s inside 15 s is 0.267, under the bar, where 8 s clears it. Per the rule written
  before this shipped, **the incumbent stays the default**: the new preset does not
  beat it on the annotated case and is an opt-in shape. The pacing readout says why:
  `mean 4.0s, median 4.0s, longest 4.0s` against the incumbent's 8.0/8.0/8.0 —
  halving the ceiling halves the shots, and the pile-up *at* the ceiling persisted.
  Not demonstrated: `beat_shortform` on real footage. Activity-mode segmentation
  finds one continuous span on a fixed broadcast camera (`chunks_considered=0`,
  1 segment — the shipped `generic_highlight` does the same on this match), so its
  eval row is one clip and F1 0.000. Its pacing and hook are carried by constructed
  segments, replayed against 7 mutations each killed by the assertion it targets
  (`hook_first started at 2, want … [2 14 26]`, `longest shot is 9.000s; the preset
  promises a 4.0s ceiling`, `clip_order "hooks_first" accepted; a typo would
  silently keep the old order`). The missing input is a multi-cut, moving source.
- **Captions are now styled for the reel they land on.** A `.ass` file declares a
  reference frame (`PlayResX/Y`) and libass scales the whole script against it, so a
  style written for a 1280×720 box puts its text at the size and height of a
  horizontal frame on a 9:16 reel — the same file, a different picture.
  `subs.KaraokeStyle` gained the canvas (`Width`/`Height`, unset meaning the shipped
  reference, so nothing changes for a caller that says nothing), and the writer now
  resolves every pixel metric from it: font, outline, shadow and side margins by the
  frame's short side (they are about glyph size), the bottom margin by its height (it
  is an offset from the bottom edge of *this* frame). 1080×1920 therefore gets
  `PlayResX: 1080 / PlayResY: 1920 / Fontsize 72 / MarginV 107` where 720p keeps
  48/40 exactly as before, and a frame small enough to round the outline to zero still
  gets a stroke — an outline is what keeps white text readable over a bright frame.
  The transcript stage reads the project's own timeline for that canvas, so the
  generated file matches the reel it will be burned onto. Verified end to end
  (`TestSubtitlesFlow` puts a vertical timeline in the project before transcribing and
  reads the PlayRes back out of the served `.ass`) and at the unit level; five
  mutations each killed one assertion, including `terr != nil`, which is "the wiring
  never looked at the canvas" and fails the chain test rather than the unit one.
- **A caption now fits the frame and stays long enough to read** (B5b, the other half
  of the slice above). Words are grouped into lines that fit the frame's usable width —
  `(PlayResX − 2·MarginL) / FontSize`, with the separator between words counted, because
  that is what the layout budgets — at most two lines per cue, and a longer segment is
  split into successive cues whose times tile the original span: no silence hole between
  them, no overlap. A cue under 1.2 s is held into the silence that follows it, stopping
  at the next cue's start, and the hold extends the *display* only: the karaoke fill runs
  to `speechEnd`, because a caption that stays on screen must not stretch the highlight
  past the singing. On a 1080×1920 reel that is twelve units a line, so a 40-character
  lyric becomes four cues of two lines of six characters, every word's sweep still 20cs.
  Six assertions on the generated file (cue count, line breaks, units per line, all
  characters and sweeps present, monotonic non-overlapping times, hold that stops at the
  next cue, fill that does not stretch) plus a control that a two-character line is not
  split at all; six mutations, each killed by exactly one of them. Two lessons in the
  same commit: the full suite caught a regression the new tests could not see (the dwell
  had lengthened the last `\kf` sweep — `\kf50` / `\kf70` where it had been 50/50),
  and two of the first assertions could not fail, so their mutations survived until they
  were rewritten to measure what the code actually budgets (characters *and* separators)
  and to leave the fixture no gap the missing clamp could hide in.
  Known remainder, stated because it will bite: the geometry is decided when the
  transcript runs, so switching a project to a vertical style re-runs the transcript to
  restyle its captions (the transcript payload is not stored, so the burn cannot
  re-render the style at its own time).

### Improved
- **The release build now runs the binary before shipping it.** The packaging job used
  to do that, on a runner that no longer takes the work, so `scripts/build-release.ps1`
  and its `sh` twin had been stamping three platform binaries, copying a worker, and
  never starting one of them again — the shape of gap where a `-X` path that stopped
  matching a variable ships a release that says `0.1.0-dev` forever and nobody notices
  until a user quotes it back. `scripts/smoke-release.sh` is one file both twins call
  (the same reason `check.sh` and `check.ps1` share a step list rather than each
  keeping a copy): the artifact runs and reports the version and commit it was stamped
  with, its usage page renders, an unknown command exits nonzero, `init` lays out the
  workspace it was pointed at, and the shipped `config show` still refuses to print a
  bearer token. Only the host artifact can be executed on the build machine, so that is
  the one that is executed, and a platform list that misses the host now refuses to
  ship rather than quietly producing three untested files. Measured: a full build of
  windows-amd64 / linux-amd64 / linux-arm64 plus the musl worker printed five passes
  against the stamped binary; the same script aimed at an unstamped `go build` exits 1
  naming what it saw, and under the script's own `set -eu` the next statement is not
  reached.
- **A coverage sweep, read instead of guessed.** The attribution-correct sweep (22 test
  binaries, max-merge) named 28 functions no leg had ever executed; three of them have
  no caller at all and two of those carried a comment pointing at one that does not
  exist — `workspace.RenameAtomic` (whose sibling's doc block said the timeline
  publishes through it; it goes through `RetryableRename`), `analysis.coverageAt` (a
  second, unread definition of the beat grid's coverage rule, orphaned by the B1b
  rewrite), and `analysis.Key` ("used by CLI logging", unreferenced by any CLI line).
  All three are gone; the comment now names the function that is actually called. Two
  more were checked rather than assumed: `writeIdleWriter.Flush` is an
  interface-conformance shim — `ServeContent` does not flush, the stdlib source was
  read to be sure — and the non-Windows `xcut client` is a real command nobody had
  compiled a test for. It now runs on the Linux leg, where it caught two defects in
  the case itself before any product defect: it read the wrong URL out of the shared
  output buffer, and it bound the default port rather than asking the kernel for one.
- **A refused save now says which refusal it is.** The second walk through the product
  spent its time on the manual-editing surface, and it turned up three sentences that
  could not be acted on. A document rejected for a zoom of 3.0 was answered with
  `timeline validation failed (1 problem(s))` — the same words an unknown asset and an
  empty document produce, so the reader guesses and re-sends. A validator that prints a
  float prints `starts 10.500100000000003`, seventeen characters standing for a moment
  nobody can point at. And both kinds of revision conflict said "timeline changed since
  you loaded it", which is no use at all to a client that never loaded anything.
  `Validate` now names up to three problems (`track "v1" clip[1] "c2": motion.zoom 3 out
  of (0,1]`) and counts the rest (`(and 1 more)`) rather than hiding them; every time in
  those sentences goes through the same rounding the pacing readout uses, because a
  millisecond is all a readout can claim and the fastest canvas frame is 4.17 ms, so what
  gets dropped is the residue, not the number. The 409 split names both sides of a
  mismatch (stale: "you sent revision 1, the saved document is at revision 2") and, for a
  document carrying no revision, says there was nothing to check it against. The UI's
  banner stopped asserting "the timeline changed elsewhere" and quotes the server
  instead, keeping only what only the page can know: that the button to press is Reset.
  Verified: six new cases, two of them written red first (the raw-float assertion, and
  the requirement that the two 409 wordings differ). Four mutations, each killed and each
  restored byte-identical: the seconds formula back to `%g` (the readability case dies
  naming the float), the blind/stale condition made unreachable (the blind case inherits
  the stale sentence), the 409 key literal stripped of `{msg}` (the DOM gate and the i18n
  gate fire independently), and the UI reverted to its fixed sentence. The DOM check
  reads the 409 arm's own key literal rather than `{msg}` anywhere in the block, because
  the other arm interpolates it and would have covered for a missing one.
- **The web UI now shows what a reel is made of, not only what it contains.** The
  timeline panel carries a pacing chip — `{shots} shots · mean …s · median …s ·
  longest …s · best shot starts at …s` — beside the existing footage note, and the
  numbers come from the server: `GET /api/v1/projects/{id}/timeline` gained a
  derived `pacing` object computed by `timeline.Pacing`, the same function the CLI
  line uses. One measurement, two readers, so the chip and `xcut timeline` cannot
  tell different stories about one document. The chip describes the **saved**
  document, not the unsaved trims in the strip above it, and a document with no
  scores in it gets its lengths and the sentence "no shot is scored in this
  document" instead of an invented best shot at 0.0 s.
  Verified at three levels, because a CSS rule that renders on nothing has fooled
  this project before: the wire (a Go test asserts the raw JSON keys — `shots`,
  `mean_seconds`, `hook_seconds`, `scored_shots` — and the values against a document
  it builds itself), the asset guards this package already runs (the new element id
  and the two new i18n keys go through `static_dom_test.go` and `static_i18n_test.go`
  in every gate), and the browser (the
  chip read back as `display: block` with `8 个镜头 · 平均 7.5 秒 · 中位 8.0 秒 ·
  最长 8.0 秒 · 评分最高的镜头从第 16.0 秒开始` — the same numbers `xcut timeline`
  printed for that document, and again after a reload and re-selecting the project).
  The browser level was performed once, by hand, and CI cannot repeat it — stated
  here as what was seen, not as something enforced.
  Five controls prove the guards bite: dropping the envelope field, renaming a JSON
  tag, typo-ing the element id, drifting one i18n key, and renaming a field on the
  *script* side (`app.js never reads "p.mean_seconds"`) each failed exactly one named
  test — the last one is what makes the two ends of the wire a gate rather than an
  observation. The tree was restored byte-for-byte afterwards.
- **The web UI can now ask for music and for the beat, and it says what the document
  actually did.** The pipeline panel gained a **music bed** path field and a three-way
  **cut on the beat** selector — `style's own` sends no field at all, `±0.12 s` sends
  the product default, `off` sends `-1` — so the three states the API already
  distinguishes stay distinguishable from the browser (`timelineRequest()` called in
  the page returned `{style, duration:60, music:"…bed60.m4a", beat_snap:0.12}`, then
  `beat_snap:-1`, then `{style, duration:60}`). Under the timeline, a second line
  reports the saved document's choice in the four cases that mean different things: a
  bed the cuts followed (`music bed "click20.wav" at 120.00 BPM · 1 of 8 cuts landed
  on its beat`), a bed whose beat was measured while nothing needed to move, a bed
  whose audio held no grid, and no bed at all with snapping from the source's own
  pulse. File *names*, never the caller's path. The keys both sides speak are pinned in
  Go (`md.music`, `md.music_bpm`, `c.metadata.beat`, `req.music`, `req.beat_snap`).
- **Known issue found by using `--music` at product scale (ROADMAP B1b).** 20 seconds
  of exact 0.5 s clicks is accepted as a 120 BPM grid (`coverage=1 beats=39`); sixty
  seconds of the *same* clicks is refused ("no beat grid the estimator will believe,
  onsets=119"), and 119 onsets spaced 1.0 s apart returns `period=0.2 bpm=300
  coverage=1.000` — a confidently wrong grid, which is worse than the refusal. A
  scratch matrix (run, then deleted) puts the boundary at the number of beats rather
  than the span: accepted at 6/8/10/20/30/40/60 onsets, refused at 119, and accepted at
  119 only where the period lands on a rung of the 2% candidate ladder (0.4 s). The
  numbers point at coverage being judged on the *unrefined* candidate period, so the
  ladder's ~1% error accumulates per beat while the least-squares refinement that would
  correct it runs after the verdict. Until it is fixed, a three-minute pop bed should
  be expected to play without snapping — which is exactly the promise B4a made and did
  not measure at that length.

### Fixed
- **A workspace `config.json` no longer resets settings it never mentioned.** The
  documented precedence is defaults < `<workspace>/config.json` < env < flags, and the
  second file layer did not behave that way: `config.Load` prefilled the struct with
  `Default()` before decoding, so a workspace file that named one knob arrived at the
  merge carrying *every* default — while `MergeLayer`'s rule, pinned by its own
  `TestMergeLayerKeepsBaseWhenLayerZero`, reads a zero as "not said". Nothing could
  tell a choice from an absence, so writing anything into the workspace file reset
  whatever the operator had set in the bootstrap config. Measured through the shipped
  binary with `~/.xcut/config.json` holding `log.level=debug`,
  `resource.max_cache_gb=2`, `resource.max_temp_gb=3` and a workspace file mentioning
  only `resource.frame_sample_fps`: the reset (`info` / 10 / 20) before, all four
  values honoured after. `Load` is sparse by contract and documented as such; the
  defaults are applied where the precedence belongs. Four cases drive it through the
  real `loadConfig` with a home directory pointed at a fixture — an explicit
  `--config` deliberately skips the layering, which is how the first version of the
  test passed while proving nothing — and the case now fails loudly if the layering
  branch never runs. Flipping the merge direction is caught by two named assertions.
- **The gate could not say it had skipped the race detector, and now it says it
  skipped a subset of it.** `-race` lives only in the full battery, which runs on the
  Linux node, so neither per-milestone leg — this laptop's gate nor win-devops's — ever
  looked at a data race. The session paid for that directly: a test helper cleared a
  channel field the test goroutine was reading, `GOOS=linux go vet` was silent (the
  file compiles), the node's non-race verification of it was silent, and the full gate
  reported `DATA RACE` twenty minutes and one dispatch later. Fast mode now runs
  `-race` over the four packages where that class of defect actually lives —
  `internal/cli`, which holds both races this project has ever recorded (the analyze
  fan-out's shared writer at `680d707`, and the test helper above), plus the job queue,
  the subprocess client and the pipeline's fan-out. Cost, measured twice here: 62–98 s
  for the subset, 3m16s for the whole fast gate against about 2m30s before. The full
  leg keeps running `-race ./...`.
  Both twins got the accounting fix too, which was the other half of the hole: neither
  script had ever added the race skip to its `not run:` list, in fast *or* full mode —
  the step whose entire job is to state what a green run did not check had no line for
  its own longest skip. Verified by forcing the probe false and reading the verdict:
  `not run: race-subset`, and the normal path reports `steps not run: none` with the
  wall time printed.
- **The one tap reused captions from another frame and called it done.** "The
  project already has subtitles" was decided by whether a file existed. For a project
  that kept its shape that is the truth; for one that changed it — the tap's own
  default reel is vertical, and a transcript made before the reel existed is laid out
  against this package's shipped 1280×720 reference — it burned a caption box sized
  and placed for a frame nobody was going to watch, because libass scales every pixel
  field by the script's `PlayResX/Y`. The file already carries the claim, so
  `subs.ReadASSFrame` asks the file (and refuses to invent an answer: a pair that is
  absent, unparsable, or sitting outside the section that owns it is *no claim*, which
  is not the same as the default). `pipeline.subsState` compares that with the canvas
  of the timeline document the same tap is about to render onto, and one rule answers
  for the plan and the body — they run at different moments, and a tap that has to
  build the reel first only learns the canvas after that build. Three answers now
  where there was one: a sidecar can lay the same words out again, so the step says
  `create`, names both frames, and the test reads the artifact back to check it
  declares the reel's; nothing can, so the step says `reuse`, names both frames, and
  the render writes the two numbers into the log it now keeps writing; the frames
  agree, so the sentence stays the plain one and the file stays the same bytes — the
  arm that an always-restyle rule would have to pass. Four cases, and the mutations
  that say so: a body that never restyles was caught by the artifact rather than the
  sentence; a plan that ignored the state, a comparison that never fired, a
  no-claim-file treated as a mismatch, and a mismatch that ignored its own
  comparison were each caught by the case named for it. One mutation ran green at
  first — removing the section guard changed nothing for the fixture designed to
  catch it, because the early exit already covered that shape; the case that
  distinguishes them (no Script Info section at all, the pair parked in a style block)
  is in the file now.
- **`serve.log` stopped recording the moment the server started.** `startServeCore`
  opened the rotated file logger and closed it on return — `defer closeLog()`, sitting in
  the one function that hands that logger to everything running afterwards. So the file
  `docs/OPERATIONS.md` points an operator at, and the request-failure trace `api.writeErr`
  exists to write, covered exactly one line: the startup one. Every failure, warning and
  drain note from the rest of the server's life went to a closed handle, silently, because
  a `slog` handler ignores a writer's error. The ownership now passes to `runningServe`,
  whose `close` the three callers (`serve`, `client`, and `client --browser`) already
  defer; the bind-failure path closes it where it fails. Found by running the paths at
  all: `startServeCore` and `shutdownServe` had never been called by a test, which is how
  a defect introduced with the logger itself stayed invisible through every session since.
  Four cases now drive the real lifecycle — a request refused while the server is up must
  reach the file, with the startup line as the control that the file is the right one; the
  three startup sweeps must reclaim what each of them reports (a job row left running, temp
  debris, staged-upload debris) and leave a landed upload alone; the port must answer
  before the drain and refuse after it; and `serve` must remain a writer command, since the
  sweeps' licence to delete is the workspace lock. Five mutations, each killed by the
  assertion it targets: the temp sweep as a dry run (only the file-survival arm saw it), the
  orphan sweep given its age gate back, the staging sweep removed, the drain turned into an
  early return, `serve` dropped from the writer list.
- **The local gate printed PASS over a run in which `go test` never started.** At 03:21
  the fast gate returned 0 with `== gate (fast): PASS (steps not run: none)` and, four
  lines above it, `== go test: 0 passed, 0 skipped`. The step's stderr redirect pointed at
  a fixed name in `%TEMP%`; another project's local gate had that same file open, the
  redirect raised an `IOException`, the child never launched — and the exit code could not
  catch it, because a redirect that fails before the child starts leaves `$LASTEXITCODE`
  holding whatever the previous step set. The file is now named for the process that owns
  it, and both gates (the PowerShell one and its `sh` twin, which has no such collision but
  had the same missing floor) refuse a step that observed zero test events, printing how
  much output it did get.
  Verified by reproducing the incident rather than reasoning about it: collision restored
  with the guard disabled returns rc=0 with `0 passed, 0 skipped` under a PASS; the same
  collision with the guard returns rc=1 naming the empty run; the `sh` twin's guard is
  exercised on the Linux node against the throwaway snapshot, where the test command is
  replaced by `true` and the run must be refused — a control that runs after the real gate
  and restores the file with `cmp` before judging, so it cannot poison the verdict.
  `scripts/check.ps1` came back byte-identical after each experiment, and the runtime name
  was confirmed per-process (`xcut-gate-test-8700.err`).
- **Three things a walk through the real product found, at 02:16.** The walk drove the
  HTTP surface the way the page does — real 603 s match, a synthetic 120 BPM click bed,
  the vertical style, a transcript with word timings and two hostile lines (`{}`, a
  backslash, a newline) — and reported what a user would have seen:
  - **`pacing` came out over HTTP as `7.50000000000001` and `6.0200000000039`.** The
    numbers are sums over float durations, and the browser hides it with `toFixed(1)`
    while any other consumer sees the whole tail. `Pacing()` now rounds to
    milliseconds — a millisecond is the finest claim a shot list can make. The case
    that proves it needed the *third* of a second to be written first: the fixture with
    human durations (7.1/7.3/8.1) let the mutation that deleted the rounding walk
    through, because those numbers divide cleanly enough to look like the bug was gone.
  - **"this footage offered 1 candidate rallies and the cut took 1 of them."** The
    vertical style on sports footage finds one candidate, which is exactly the case
    where the sentence matters most (the reel is 2.8 s of the 30 s asked for), and the
    plural template said something false twice over — the grammar, and "has worked
    through every candidate it found" about a search that never happened. CLI and
    client now have a singular sentence: *"this footage offered one candidate rally
    and the cut took it … the selector found nothing else to cut."*
  - **`TestI18nPlaceholdersMatch`** compares each source string's `{placeholders}` with
    its zh value's, both directions. It was written immediately after hand-translating
    the singular note left `{clips}` in the Chinese string that no longer declares one
    — which would have printed `{clips}` on screen, silently, in the only language the
    guard was missing for.
  Left alone on purpose, and named so it is not mistaken for unseen: the terse data
  lines (`timeline: 1 clips`, `pacing: 1 shots`) read as labels rather than prose, and
  Chinese has no plural to get wrong there. The walk itself is kept out of the repo at
  `D:\tmp\xcub1\walk.py`; its output for the record says the tap took 41 s import to
  file, the reel it wrote was 2.833 s / 657 KB from one candidate, and the caption file
  it produced declared `PlayResX 1080 / PlayResY 1920` with the hostile characters
  escaped (`{花括号}`, `/`, `\N`) rather than live in the stream.
- **`docs/USAGE.md` was behind the binary, and now a test says so.** Adding `--subs`
  to `xcut auto` left the page stating the old syntax in the same commit that landed
  the flag — the third such drift a grep turned up on the spot: `xcut timeline`'s own
  `usage:` line never grew `--beat-snap` or `--music` when those arrived, and
  `xcut roi` was in the binary and not on the page at all. `TestUsageDocsMirrorTheBinary`
  reads the command registry (usage line and summary for every registered command) and
  requires each to appear in the doc, whitespace-collapsed so markdown's line wrapping
  is not part of the contract. It went from five mismatches to zero as the drifts were
  fixed — the witness is the before/after on real text, not a synthetic mutation. The
  check is a floor: it says the page contains what the binary promises, not that the
  page says nothing false.
- **`EstimateBeatGrid` refused music it should have believed, and sometimes named a
  different tempo with total confidence** (ROADMAP B1b). The estimator walked a 2%
  ladder of candidate periods and judged each one by folding the onsets to a single
  phase — which is fine for a short file and wrong for a long one, because the rung's
  ~1% period error is a phase error that grows one beat at a time: by the hundredth
  beat the clicks are out from under the fold, and a *perfect* minute of metronome
  scored 0.80 coverage and was called rhythmless. Measured over every whole BPM from 30
  to 300 on a 60 s click lattice: **121 believed, 142 refused, 8 answered at the wrong
  tempo** — and of those 8, 119 clicks spaced 1.0 s apart came back as
  `period=0.2 bpm=300 coverage=1.000`, the longest-wins rule collapsing onto the
  ladder's own start value, which is worse than a refusal because it snaps cuts to
  beats nobody played.
  The fix removes the ladder. A period is now proposed for every count of intervals
  between the first and last onset (an anchored span has no error to accumulate),
  sharpened by the least-squares fit, and judged by the largest cluster of residues —
  with the phase anchored on a real onset rather than taken from the fit. That last
  part is not tidiness: the fit's mean phase sits exactly halfway between the clicks of
  an alternating lattice, where every click is at *precisely* the tolerance distance, so
  a grid twice as slow as the music scores full coverage and wins. That trap was hit on
  the first attempt at the fix, measured (`period=1.0 phase=0.25 cov=1.0000`), and is now
  `TestBeatGridDoesNotScoreBetweenTheBeats`.
  Measured after: **271 believed, 0 refused, 0 wrong**, every period exact; the six B1
  cases and the real-audio case unchanged. Through the product: the 60 s click bed that
  was refused now reports `bpm=120.00 coverage=1 beats=119` with
  `cuts on the beat: 1 of 8 clips`, a three-minute bed reports the same grid over 359
  onsets, and the match's own hall audio (145 onsets) is **still refused** — the answer
  that was already right, now provably not the same failure.
  Cost was part of the work: the first version of the new scan was cubic (120 onsets
  54 ms, 1 440 onsets 2 m 7 s, 3 600 did not finish in 600 s), so the cluster search
  became a window sliding over sorted residues and the fit reads at most 1 200 onsets
  and projects the grid across the rest — 20 000 onsets now cost **127 ms** (44.7 s with
  the budget removed, and the beats still reach the last click). Seven mutations, each
  killed by the assertion it targets (the refusal, the midway phase, the longest rule,
  the coverage floor, the tail clamp, the fit, the window's wrap around the circle).
  Two things the rewrite gave up, said plainly: the ladder's own longest-rule and
  refinement survived the suite once the anchored scan existed, and running everything
  with the ladder disabled gave identical numbers — so it was deleted rather than kept
  as a second opinion; and the 1 200-onset budget is a cost bound with no test that can
  observe it, so it is carried by the timings above instead of by an assertion.

### Security
- **The UI's rendering rule is now a gate, not a habit.** docs/SECURITY.md documented
  the token, the loopback trust rule, the path guard and the pinned MIME types, and
  never named the surface that makes one rule load-bearing: a script injected into the
  embedded page reads the workspace *through an authenticated session* (D14), which
  nothing else in the document would catch. `docs/PROJECT_STATE.md` said the
  textContent-only rendering rule "is what holds that line"; no check enforced it, and
  the twelve `innerHTML` sites in `app.js` happened to be empty-string clears — which
  is a convention, and a convention is what a confident one-line change ends.
  `TestUINeverWritesMarkupFromAString` and `TestUINeverBuildsCodeFromStrings` now scan
  the shipped scripts for markup sinks (`innerHTML`/`outerHTML` assignment,
  `insertAdjacentHTML`, `document.write`) and for code built from strings (`eval`,
  `new Function`, the string forms of `setTimeout`/`setInterval`), accepting only the
  clear form the code uses. Both carry the two guards this project keeps needing: a
  **floor** (the scan must find the dozen clears it expects, so "no violations" can
  never mean "the regex matched nothing") and a **refusal table** run through the same
  classifier. Verified against reality: injecting `probe.innerHTML = "<b>" +
  currentProject.name;` and `setTimeout("pollTimer = null", 10)` into `app.js` turned
  both guards red naming `app.js:8` and `app.js:9`; the asset is byte-identical
  afterwards.
### Measured
- **B7b — what the analysis caches actually cost, and whether the proxy ceiling holds.**
  Four rows in `docs/PERFORMANCE.md`, on the 603 s broadcast and a 300 s 1080p synthetic,
  on this machine (i7-10875H, 16 threads, FFmpeg 9.0.2, xcut `0aed2e6`): the sample cache
  at **115,752 B** for 10 minutes of real footage and **36,387 B** for 5 minutes of a tone
  (~11.5 KB per source-minute, and an entry's size tracks its samples, not the pixels);
  the proxy at **2.5 MiB per source-minute** at the default 640 px / 2 fps geometry, which
  makes the default 2 GiB budget worth about **13 hours** of footage like this; a geometry
  change costing a **second full copy** (19,167,798 B at 480 px, 72% of the 640 one, plus
  116,267 B of new cache entry and a 28.8 s re-encode), because the geometry is in the
  filename on purpose; and the ceiling itself, where `xcut cleanup --dry-run` was asserted
  to plan without touching a byte and `xcut cleanup` then drained all three proxies in
  208 ms — every file went because the newest one was itself bigger than the 11.9 MB
  budget. The asset left without a proxy re-analyzed clean and paid **15.3 s** against the
  283 ms cached path, landing back under the ceiling.
  Two facts this put on the table rather than in a claim: `xcut analyze` prints no
  per-analyzer breakdown, so the byte split by track is not observable from the CLI; and
  `proxy_enabled` defaults to **false**, so in the shipped posture the 2 GiB proxy budget
  governs an empty directory — a standing control that is currently dormant, now written
  down as an owner decision instead of left reading as coverage.

## [v0.1.9-alpha] (cont.) — 2026-09-21 → 09-22, sessions #17–#19 (secret-scan honesty,
tailnet recipe, reel cost, rally slicing, the Windows 500)

### Security
- **The render download endpoint's response header is now pinned against hostile
  project names.** `GET /api/v1/projects/{id}/render` writes
  `Content-Disposition` from the project name, and names are validated for length
  only — a stored CR, LF or quote goes straight in. No test had ever reached that
  handler (or `GET /api/v1/styles`). The new test drives the real endpoint and
  requires the header to match one narrow shape; replaying the mutations shows it
  bites: allowing `"` through the sanitizer produces
  `filename="…_"quoted"___"` (a parameter boundary), allowing 13/10 produces a
  header with a literal CRLF in it. Same sweep deleted three functions with no
  callers at all (`event.scoreRally`, `event.intervalMax`,
  `analysis.Result.FindTrack`).
- **Every HIGH-severity static-analysis finding triaged, and the bar raised to
  match.** `gosec` on this tree reports 90 findings and had none at
  severity HIGH × confidence HIGH — the only cell the gate looked at, which made
  the step unable to fail. The 14 findings that *are* HIGH severity were worked
  through: `brandicon.ICO` accepted any image and wrote the one-byte width/height
  fields with `& 0xff`, so a 512 px icon (measured: no error, bytes `0,0`)
  produced a container claiming 256 px around a 512 px payload — now refused,
  with the entry list capped at 256 so the offsets cannot reach 2^32;
  `Brand(1)` fed a division-by-zero NaN into an integer conversion whose result
  the language leaves to the platform (right on amd64 only because `int(NaN)` is
  −2^63 there, divisible by 256) — now a defined gradient start;
  `workspace.CleanupPartials` deleted files by a path resolved from a walk, so a
  parent swapped for a symlink between the walk and the unlink could reach outside
  `projects/` — deletion now goes through `os.OpenRoot`, which refuses symlinked
  parents; and `workspace.DiskFree` would have turned a negative `Bsize` into an
  astronomically large free-space figure, opening every disk guard that reads it —
  now refused. The rest (s16le sample decode, fingerprint hash input, the
  little-endian byte splits, the colour lerp) are correct by construction and carry
  `#nosec G115` with the reason written out. Both gates now filter on severity
  alone, proven by a differential: an unbounded `uint8(v)` exits 0 under the old
  flags and 1 naming G115 under the new ones.
- **The Linux gate gained the static-analysis step the Windows gate had**
  (`gosec -severity high`, same suppression convention), and a clean run now
  announces itself — the first version printed its clean line on the
  policy-skip path as well, so a scan that never ran claimed there was nothing to
  find; the exit-126 leg caught that in its own log. The convention is now
  enforced by the scanner instead of by review: both gates pass
  `-nosec-require-justification -nosec-require-rules`, so `// #nosec` with no rule
  id or no `-- reason` fails (planted both defects, each named; and the check fires
  even below the severity filter). Both gates also say *which* scanner ran —
  `gosec --version` prints "dev" whatever tag it was built from, so the log now
  carries the binary's sha256 prefix and path, and the install hints name
  `@v2.29.0` / `@v1.8.0` rather than `@latest`. First run proved the need: the node
  holds two gosec binaries and the log settled which one scanned.
- **A vulnerability finding could not fail the gate.** `check.sh` read the scan
  status as `if ! govulncheck ./...; then rc=$?` — and `$?` after a negated
  command is the *negation*, always 0 — so every non-zero scan landed in
  `exit 0`: green, with the script stopping before its own verdict line. The
  `govulncheck(policy-blocked)` branch was unreachable the same way. Proven end
  to end on the Linux node: in a copy of the snapshot, a package calling
  `html.Render` from `golang.org/x/net v0.12.0` makes the real scanner exit 3
  naming the call site, and the gate now returns `gate_rc=3` with
  `== govulncheck: FAILED (rc=3)`; the identical shape before the fix returned 0
  and printed no verdict line. The step also has something to run for the first
  time: `not run: govulncheck` had been in every Linux verdict line while the
  node held `~/go/bin/govulncheck` (a non-login ssh PATH carries no GOPATH/bin),
  and a pinned `.tools/bin` copy now wins over whatever the machine installed —
  the precedence was inverted, and a stub leg that came back green proved it.
- **A gate that skipped tools now says so.** The verdict line lists the steps
  that did not run (`steps not run: integration-tests(no-ffmpeg)` /
  `not run: govulncheck`), and `check.sh` — which never scanned for secrets at
  all while printing `gate: PASS` — now carries the scan status in the same
  line. Verified both ways: a no-FFmpeg snapshot reports the skip, the normal
  full run reports `none`.
- **Gate hardening (from the control plane's secret-scan blind-spot audit).**
  `scripts/check.ps1` now *fails* when gitleaks cannot be found instead of
  warning and continuing to `gate: PASS`; both gates print the scan scope as a
  path count and refuse a zero-file scope; a failing working-tree scan names the
  offending file (JSON report on stdout) rather than only "leaks found: 1".
  Measured on a `.git`-less snapshot — before: the scan ran but reported no
  scope (284 paths present, count not shown); after: `gitleaks (working tree,
  233 paths under .)`, and a planted PEM key is named as
  `docs/planted.pem:private-key:1`. `scripts/check.sh` (the Linux leg) used to
  print `gate: PASS` having never scanned for secrets; its verdict line now
  carries the scan status, verified on the node as
  `PASS (secret scan: NOT RUN (gitleaks absent …))`.
- **The `.gotmp/` secret exemption made honest.** The allowlist is load-bearing
  (without it `gitleaks dir` reads 304 MB instead of 13 MB and reports 157
  shape-matches — gitleaks does *not* honour `.gitignore`), but the same config
  excuses history too, so a force-added file under `.gotmp/` would be excused
  from the commit gate. Both gates now refuse any tracked `.gotmp/` path
  (verified: a force-added file is named and the gate exits 1; the `check.sh`
  logic verified on the Linux node), and the scope comment states what the
  reported number actually measures.

### Added
- **`scripts/cover-sweep.sh`, a coverage sweep that attributes honestly.** One
  profile per test binary, merged by taking the maximum hit count per block:
  letting several packages share a single `-coverprofile` reports the *last*
  writer's count for each block, so code covered only through another package's
  tests reads 0.0% — that artifact put `pipeline.AnalyzeProjectAsync` on a gap
  list while it measures 100% through its own consumer (`internal/api` posting
  `/analyze`). The script names the toolchain it had (ffmpeg/python present or
  not — a skipped test is an unexecuted one), aborts on an empty or unparseable
  merge rather than reporting "no gaps", and fails when any package's tests fail.
- **The eval harness now reports the longest missed run** — how many annotated
  rallies in a row the reel did not touch at all. P/R/F1 and range hits are
  indifferent to *where* picks fall, so three clips on the first three rallies of
  a six-rally match score identically to three spread over it; the two new `Score`
  tests are built on such pairs. On the owner's match the 60 s default leaves **10
  consecutive rallies** unrepresented (120 s: 4, 240 s: 2) — the preset's phase
  rule caps clips per window and never floors one, so a window can get nothing.
  Whether that is a defect or "take the best eight wherever they are" is the
  owner's call and is recorded as an open question, not tuned silently. The
  metric replaced a seconds-based version that misled: its 164 s figure was
  mostly between-point dead time, of which only 39 s was rally content.
- **A long reel now gets finer rally slices, a short one does not.** The dense
  span the analyzer finds is cut at `rally_chunk` (default 30 s), so
  candidates = footage ÷ slice — on the owner's 603 s match that pinned the pool
  at 21 candidates whatever the style asked for, and a 240 s reel stopped at
  168 s while the note blamed the footage. The pipeline now narrows the slice for
  an asset when the ask needs more candidates than the current slice can supply
  (`event.AdaptRallyChunk`: never widened, floor at **two** clip lengths).
  Measured on the owner's match, unmarked manifest: 60 s and 120 s unchanged to
  the decimal (F1 0.199 / 0.330, 8 / 15 clips); 240 s goes 21 clips / F1 0.426 →
  **27 / 0.511** with 18 → 23 of 43 labelled ranges hit; 300 s → **0.523**. The
  configured path (the `score_roi` manifest, 44 scoreboard marks) was measured
  afterwards because the change could have broken point-end trimming and did
  not: 240 s goes 21 clips / F1 0.463 → **27 / 0.562** with precision
  0.977 → 0.987 and 26 of 27 clips still landing on a scored point, while the
  60 s row is bit-identical to before. The
  8 s floor was tried first and rejected by the suite: it still split the
  synthetic fixture's 10 s rallies and took rally recall to 0.455. Relaxing the
  event *floor* instead (`min_duration`, `min_hits`) was measured and rejected
  too — it changed nothing (`dropped_min_duration=0`), so the note no longer
  recommends that remedy. Details: `docs/EVAL.md`.
- **The client now says out loud when the footage, not the setting, capped the
  reel.** The web UI already showed per-clip `ends at point`; it stayed silent
  about the whole reel. The generated document carries
  `target_duration` next to `candidate_events` / `candidate_limit`, and the
  timeline panel prints the same sentence the CLI prints when the selector ran
  out of rallies while seconds were still allotted. Verified on the owner's
  match through the real path (reload the page, open the project, click twice):
  a 240 s ask over 30 candidate rallies renders *"这段素材提供 30 个候选回合，成片取了
  其中 27 段——要 240 秒只做到 216.0 秒"*, and the 60 s reel, which the budget cut
  rather than the footage, keeps the line hidden.
- **`xcut auto --score-crop x,y,w,h`** brings point boundaries to the one-shot
  flow: the region is written to the assets this run imported before analyze, so
  import → measure → cut → render is one command, and a malformed region is
  refused before any media work starts.
- **A reel that came out shorter than asked now says why.** Sweeping
  `--duration` over the owner's match found that 180, 240 and 300 seconds all
  produce the same 21 clips / 147.2 s — the footage stops answering long
  before the budget runs out. `xcut timeline` distinguishes the two cases
  (`candidate_events` / `candidate_limit` in the document's metadata) and prints
  which one applied: *"this footage offered 18 candidate rallies and the reel
  holds 18 — 126.4s of the 300s asked for"*. A budget-limited reel says nothing.
- **A scoreboard scan reports its own spacing**, not just its count:
  `44 boundaries, 0.3s..595.7s, median gap 13.5s, tightest 5.0s`, in
  `xcut boundaries` output and in the analyze job log. A crop that follows a
  clock or a pulsing logo returns a plausible *count* and an implausible
  distribution — the count alone cannot tell the two apart.
- **Each clip says which point boundary ended it.** A shaped clip carries
  `point_end` in its metadata and the inspector shows it as "ends at point", so
  the scoreboard rule is checkable one clip at a time rather than only through an
  aggregate score; a clip that was *not* shaped carries no such key (both
  directions are tested, and the absence is the honest case). On the owner's
  match all 14 clips of the 120 s reel now end on a measured boundary.
- **No terminal needed.** The web UI's region picker now draws either the court
  or the scoreboard (`GET/PUT/DELETE
  /api/v1/projects/{id}/assets/{assetID}/score`), and `analyze` measures whatever
  region it finds on the asset row — so drawing over the score digits and running
  the normal pipeline is the whole flow. What a region does *not* do is pretend
  to be a measurement: the response reports `marks: 0` until an analyze has run,
  and marks measured against a moved region read `stale` (changing the region
  drops the old boundaries rather than silently reusing them).
- **The gate now says which optional legs actually ran, and why a test failed.**
  `go test` reports `N passed, M skipped`, lists every skipped test by name in
  the verdict line, and replays the output (file, line, message) of each failing
  test before declaring the step red. Both directions were checked on one host:
  with python on PATH the sidecar tests are absent from the skip list; with every
  python-bearing PATH entry removed the list names
  `worker/TestScoreChanges*` and `cli/TestBoundariesScanListClear` — so a green
  gate on a python-less node can no longer read as "the cross-language contract
  was exercised". The failure replay was checked by injecting an assertion
  failure and reading it back out of the gate's own output; the numbers in the
  verdict are not quoted here on purpose, because they move with every test
  added. `scripts/check.sh` (the Linux leg) carries the same accounting.
- **Clips can stop where the point stopped.** The core's own signals were
  measured and cannot find rally ends (docs/EVAL.md), so the one source that
  can — a burned-in scoreboard — is now readable: a `score_changes` op in the
  reference sidecar, `xcut boundaries <project> --crop x,y,w,h` to scan and
  store the marks per asset, and `score_roi` in an eval manifest to A/B them.
  Optional by construction: with no marks stored, selection is what it was.
- **`xcut eval` records how many marks a case ran with** (`score_marks`) *and*
  which clips a boundary actually ended (per-clip `point_end`), so a "scored
  with the scoreboard" result cannot silently mean "scanned nothing" — nor
  "scanned everything, used nothing". Measured on the match's 60 s reel: 8 of 8
  selected clips carry a `point_end` equal to their own end.
- **`resource.ffmpeg_max_memory_mb` — a per-ffmpeg memory cap** (phase-4
  "sandbox options for FFmpeg", second rung). The kill-on-close job object
  every child already joins now also enforces `JOB_OBJECT_LIMIT_PROCESS_MEMORY`
  when the knob is set, so a runaway encoder dies of allocation failure
  instead of eating the machine, and the render fails with ffmpeg's own
  error. Opt-in and 0 (uncapped) by default: a tight cap fails real
  high-resolution renders, not just runaway ones, so the default keeps every
  workload that worked before working. Windows-effective; other platforms
  ignore it (the context-kill path stays the cleanup mechanism there).
  `xcut doctor` reports the sandbox posture so an operator can see whether
  runaway encoders are bounded on the machine they are about to trust.

### Fixed
- **`xcut eval` no longer reports zero scoreboard marks for a case that measured
  some.** The count was written to the asset row and then dropped when the case's
  reel failed (`evalRunCase` returned `nil, 0, err`), so the results document
  showed `score_marks: 0` beside the error — the exact ambiguity the field was
  added to remove. The manifest path now returns what it measured, and
  `internal/cli/eval_score_test.go` pins both halves (the refusal without a
  sidecar names the case and `workers.ai_bin`; the scan itself reports the
  fixture's two corner changes). `cli`'s duplicate of the scan-and-store write is
  gone: `pipeline.ScoreScan` is now the only implementation the product and the
  harness share, so an evaluation cannot measure a stand-in.

- **A timeline read could answer 500 while Windows was mid-swap, and the test
  that caught it blamed the wrong verb.** Two related fixes:
  - `timeline.LoadFile` now waits out a transient open failure. While another
    process is replacing a document's name, `os.Stat`/`os.ReadFile` answer
    `ERROR_ACCESS_DENIED`, and that became `cannot access timeline file` → 500 on
    a plain `GET /timeline`. `workspace.RetryTransient` waits only for the
    sharing class of error (2 s ceiling), so a genuinely missing file stays a
    fast NotFound — pinned both ways by `TestRetryTransientWaitsOnlyForSharing-
    Conflicts` (widening the predicate to `ERROR_FILE_NOT_FOUND` costs 8 attempts
    and 2.54 s, and is red).
  - The publish side, for the mirror case (a reader holding the destination):
    `workspace.RetryableRename` allowed ~420 ms of escalating sleeps, which a
    Defender scan or four concurrent savers can outlast; the budget is now an
    explicit 2 s, which is risk-free because nothing is deleted and the source
    keeps its bytes. `TestRetryableRenameWaitsOutABriefHolder` reproduces the old
    behaviour verbatim (holder releases at 900 ms →
    `Access is denied. (waited 627ms)`), and its pair proves the give-up stays
    bounded with the source intact. The media path's delete-then-rename is
    deliberately still not used for the document: it would trade revision
    integrity for the same convenience.
  - `api.TestTimelineRegenAndPutRevisionUniqueness` stored only the status when
    its `GET` failed, so the report read `unexpected PUT status 500:` with an
    empty detail — and the search went looking for a write bug. It now records
    which verb failed and its response body either way.
  This is the cause of the single unreproduced 500 in `docs/PROJECT_STATE.md`
  (job `20260921-173929-cfff4f`), now also reproduced locally in a gate run.
- **`xcut auto` stayed silent when the reel came out shorter than asked.** The
  one-shot printed the same `timeline: N clips, X s total` line as `xcut
  timeline` but not the note under it — so the path where an ambitious
  `--duration` is most likely to be answered short, and the one a first run
  takes, was the one path that explained nothing. Found by running the documented
  recipe on the owner's match: 240 s asked, 23 clips / 158.6 s delivered, silence.
- **An unreadable worker answer now says which worker was run.** "worker
  response unparseable: unexpected end of JSON input" was the whole report when
  `workers.ai_bin` names an interpreter instead of the sidecar script — a dead
  end that cost a full media run to diagnose. Both worker read paths now name the
  binary, and the README's one-shot recipe states the region's unit
  (`--score-crop 0.43,0.78,0.14,0.10`): pixels are the natural guess for a screen
  region and the refusal said only "out of range".
- **A data race in the analyze fan-out (the Linux leg's long-standing open
  sighting).** `analyzeBody` runs one goroutine per asset and called the
  per-asset callback *on that goroutine*, while `xcut analyze`'s callback prints
  a multi-line block per asset to a single shared writer. Two assets finishing
  together wrote the same buffer at once — reported as `race detected during
  execution of test` in `cli.TestE2EAutoScopesToRunInputs` at `1f7c899` (never
  reproduced) and again at `680d707`, where the node gave both stacks. The
  fan-out's comment said callbacks were marshalled back to the coordinating
  goroutine; they were not. The pipeline now serialises the callback where it
  creates the concurrency, so every caller may write to one stream.
  `TestAnalyzeProjectParallelMultiAsset` asserts the callback is never entered
  twice at once and yields inside the region to make that observable: deleting
  the mutex fails locally, while the same mutation stayed invisible through 8
  local `-race` runs of the e2e that first caught it — the trigger needs load,
  which is why the guard counts overlap instead of hoping for the detector.
- **The worker test that timed out on a loaded machine now observes instead.**
  `TestCallReturnsBeforeWorkerExits` compared a clean spawn against a hung one
  and failed at 5.0 s with the bound; it failed for real in a gate run at
  `2697ac3` (clean 10.96 s, hung 16.73 s). The spawn re-executes the test
  binary, so its cost is this package's own suite up to the stub — 5 s idle,
  22 s under parallel load — and subtracting the two legs does not cancel the
  part that scales, because one ends in a natural exit and the other in a kill
  and a reap. The replacement asserts what is observable: the answered call
  returns the answer, and the hung worker's own loopback listener is gone by
  the time it does — with an in-run control that a live listener *is*
  dialable on this host, so "cannot dial" cannot mean "cannot dial anything".
  Mutation-checked (`parseErr != nil || true` at the answered-then-killed
  branch turns it red with the error text). Costs: nothing in the suite notices
  the grace window growing any more; that is a number for docs/PERFORMANCE.md,
  and the test also got 7× cheaper (27.7 s → 3.9 s).
- **An existing project could not be opened.** The header's project button was
  revealed only by selecting a project, and the list it opens was hidden by a
  `hidden` attribute while the script toggled a class no stylesheet rule read —
  so after a reload, or after deleting the project that was open, the only
  action left in the bar was *create a new project*, and the media, analysis,
  timeline and renders of every project already on disk were unreachable
  (while the empty state invited "select or create a project"). Found by
  loading the UI against a workspace that already had a project in it: the
  button was not in the accessibility tree and the list computed to
  `display: none` after the click. The button now appears whenever there is at
  least one project, and the menu's visibility is owned by one mechanism (a
  class the stylesheet answers).
- **The timeline refuses boundaries it cannot vouch for.** Marks are consumed
  only when they were measured against the region the asset carries *now*. The
  storage layer already dropped them on a region change, so nothing could go
  wrong today — which is exactly why the consumer now checks: the guarantee held
  only as long as every future writer behaved. A stale row is logged with both
  regions rather than used or ignored silently.
- **The region panel cannot show the other region's state.** Switching the
  picker's target fired two overlapping requests, and whichever resolved last
  wrote the status line — so the scoreboard view could end up reading "full
  frame (no ROI)", the court's answer. Each refresh now binds to the target it
  started for. Found by driving the real UI in a browser session against the
  owner's match (the scoreboard line then reads "44 point boundaries measured at
  0.43, 0.78, 0.14, 0.10"). The panel heading and button also stopped claiming
  "Court ROI" while drawing the scoreboard.
- **Two tests that could only report a number now report a reason.** The
  concurrent-PUT revision test threw away every response body, so the one time
  it failed (a 500 on a CI node, unreproducible in 40 local runs and two green
  local gates) it said only
  "unexpected PUT status 500"; it now prints the body, and a second test proves
  that a rejected PUT does explain itself. The hanging-worker test asserted
  "returns in seconds" with a hand-picked 15 s bound, while the measured cost of
  *its own harness* re-executing the test binary as the worker is ~5.0 s idle and
  7.7–22.7 s with the suite running other packages in parallel — the grace path it
  guards adds ~0.07 s over that baseline, so the fixed bound was a load detector
  and did go red on a busy machine. It now times a clean-exit stub and a hung stub
  back to back and bounds the **difference** at 5 s: spawn cost cancels, measured
  differences were 0.85 s and 1.55 s under load, and inflating the grace 20×
  separates them to 40.3 s — a clean, named failure.
- **A scoreboard scan and its region can no longer be written apart.** Storing
  marks now stamps the region they were measured from in the same statement.
  The eval harness had been writing one without the other, and the consumer
  guard added the same day (ignore marks whose region is not the stored one) did
  its job and dropped them — so `score_roi` cases ran with the boundaries
  switched off and still printed a green gate. The new `boundaries n/m` field is
  what surfaced it: the eval line read `boundaries 0/8` at the old precision.
  Re-measured on the match after the fix: P 0.998, 8/8 clips boundary-shaped.
- **A reel length below the style's minimum clip fails with the real reason.**
  `--duration` under the style's `min_clip_duration` could not hold a single
  candidate, so the run burned a full analysis pass and died with the generic
  "style constraints rejected all events"; the check now fires where both
  numbers are at hand ("reel length 1s is shorter than style "ktv_mv" minimum
  clip (2s)") and covers every entry point through the pipeline.
- **`phase=done` used to be published before the scratch archive was removed.**
  The installer's cleanup sat in a `defer` on the enclosing function, so a
  client polling status could observe a finished install with
  `scratch/ffmpeg-pinned.zip` still on disk. Found by running the gate on a
  machine without FFmpeg (the timing window opened there), not by changing the
  test: the archive is now removed before success is published, and the deferred
  cleanup still covers the error paths. 20 consecutive runs in the environment
  that exposed it.

### Measured
- **Tailscale remote access is now a measured recipe, not an option**: the
  published arm64 binary on a tailnet peer, driven from this machine — token
  gate 401/401/200, session 201, cookie reads pass, a cookie-only **write** is
  refused while the echoed header is accepted, UI shell unauthenticated. First
  time D14's asymmetry was observed over a real network path rather than
  loopback or httptest. Node cleaned afterwards (listener gone, token file
  removed).
- **Cost of a longer reel** (docs/PERFORMANCE.md): 2.4x the duration costs 8%
  more core memory (20.2 → 21.9 MB) and a flat ffmpeg peak (~323 MB, set by the
  encoder canvas), wall time roughly linear. Over-asking is material-bound, not
  budget-bound: a 4 h target on a 10-minute match returns the same 21 clips /
  168 s as 240 s, so the new knob opens no unbounded growth axis.



## [v0.1.9-alpha] (cont.) — 2026-09-20 night session #16 (sign-in visual pass, budget fix)

### Fixed
- **The sign-in page could lock its own address out.** The remote UI's
  background health poll runs without a token, and every credentialless 401
  was charged against the per-peer brute-force budget: leaving the sign-in
  dialog open for five minutes (measured, in a real browser over a real LAN
  bind) spent all 20 rejections, and then the *correct* token got 429 too.
  The budget now charges only requests that presented a credential — a wrong
  bearer token, a wrong scheme, a stale session cookie or echo header. A
  credentialless request cannot authenticate and learns nothing per attempt,
  so it keeps its plain 401 forever without spending anything; the guessing
  budget keeps punishing exactly what it punished before (verified at the
  binary level: 25 credentialless polls → 401, correct token → 200; 20 wrong
  tokens → 429).

### Measured

- **The sign-in panel's visual pass is done** (the last item of the
  remote-access roadmap entry that a 0×0 harness viewport could not close):
  a real browser at 1280×800 and 390×844 over a non-loopback bind with a
  48-char token — sign-in modal centered and unclipped in English and
  中文, the wrong-token error state ("That token is not valid here." / 该令牌
  在这台服务上无效。) renders inside the dialog with no overflow, sign-in
  succeeds to the full three-pane UI, and the narrow viewport wraps without
  horizontal scroll. The browser drive itself found the budget defect above —
  which is what the pass was for.
## [v0.1.9-alpha] (cont.) — 2026-09-20 night session #15 (reel length, ops runbook)

### Added
- **Reel length is a per-run choice, not a preset edit**: `--duration` on
  `xcut timeline`, `xcut auto` and `xcut eval`, a `duration` field on the
  timeline API, and a "reel length (s)" number field beside the style picker in
  the web UI. Empty/absent keeps the style's own `target_duration`; the preset
  file is never rewritten by an override. Accepted range 1–14400 s, enforced in
  `pipeline` so CLI, UI and scripts cannot disagree about the bound.
- **`docs/OPERATIONS.md`** — the remote-serving runbook: what the server binds
  and why, how to make a token, the SSH-tunnel recipe driven end to end (with
  the consequence stated: a forward makes the peer look loopback, so the tunnel
  hands authentication to SSH), rotation and revocation, and a 401/403/429
  troubleshooting table.
- `internal/api/static_dom_test.go`: every `$("id")` / `querySelector("#id")` in
  the UI must resolve to markup that exists, `for=`/`aria-labelledby=` targets
  must exist, and the sign-in panel's five elements are pinned in both
  directions. Falsified by renaming one id.

### Fixed
- **A render failure that named nothing.** Kylin V10 SP1's FFmpeg 4.2.2 is built
  without `xfade`, so a valid timeline failed with "ffmpeg failed". The message
  now names the missing filter and the two ways out (`generic_highlight`, or a
  full build), built from the real captured stderr and guarded against
  over-matching by a test.
- **A longer reel used to be silently truncated.** The diversity phase quota
  (`phases × max_per_window`) was an absolute ceiling on clip count regardless
  of budget: a 240 s request on the measured match returned exactly the same
  10 clips / 80 s as a 120 s request. The quota now buys more *windows* instead
  of a looser per-window discipline, keeping the spread rule the quota exists
  for. Measured (docs/EVAL.md): 240 s goes from 10 clips / R 0.140 / F1 0.239 to
  **21 clips / R 0.289 / F1 0.426**, covering 18 of 43 rallies rather than 8,
  while the shipped 60 s default stays **bit-identical** (P 0.886, R 0.112).
- The remote sign-in field had only a placeholder, which is not an accessible
  name; it now carries a translated `aria-label` through a new `data-i18n-aria`
  channel (the i18n drift gate covers the attribute in both directions).

### Measured
- **The ARM64 suite ran on real Kylin hardware for the first time.** 15+ tests
  fail there, and every one traces to the two distro-FFmpeg limitations above
  rather than to product logic — so arm64 stays "compile-verified +
  artifact-smoke-tested", not suite-green, until the node has a stock build.
  Closing that gap needs an owner decision (pin a checksummed arm64 FFmpeg for
  CI, or accept the current bar); an unpinned binary was not downloaded onto
  the machine.
- `max_clip_duration` is at its optimum: 11 s and 14 s both lose precision *and*
  distinct rallies (a longer window cannot fit a 10.5 s median rally, so it
  spills and the budget holds fewer clips). Recorded as a negative result.
- Decomposing the committed 60 s reel: 53.2 s inside an annotated rally, 6.8 s
  adjacent to its own, **0.0 s wrongly picked** — the remaining error is
  boundary coarseness, not selection.

## [0.1.8-alpha] — 2026-09-20 session #14 (API authentication — D12/D13/D14)

### Security
- **Bearer-token authentication gates a remote bind.** The API had refused
  every non-loopback address because there was nothing to authenticate a peer
  with (D8); that precondition now exists. `/api/v1/*` requires
  `Authorization: Bearer <token>` from any peer whose socket address is not
  loopback; loopback peers stay trusted so the desktop client, `xcut client`
  and the double-clicked exe need no setup and no token.
- **The invariant is enforced twice, on purpose**: `config.Resolve` rejects
  `listen_remote: true` without a token of at least 24 characters, and
  `serveAddr` re-checks the pair at the last moment before `net.Listen` — a
  socket opening on the network must not depend on one call site having run.
- Token comes from `server.auth_token` or `XCUT_AUTH_TOKEN`, never a CLI flag
  (argv lands in process listings and shell history). `xcut config show`
  reports it as `<set>`; the starter config written by `xcut init` blanks it,
  so an environment secret cannot silently land in a 0644 file.
- Comparison is constant-time (`crypto/subtle`); a rejection says only that
  authentication failed — no hint of how close the attempt was — and serve.log
  records method/path/peer/status, never the configured or supplied token.
- **Brute force is budgeted**: 20 rejections per peer per 5 minutes, then 429;
  the tracker itself is capped at 4096 peers and prunes expired windows rather
  than growing (AGENTS.md rule 4 applies to new growth axes).
- Trust is decided from `r.RemoteAddr` alone. `Host` and forwarding headers
  are attacker-chosen and are never consulted.
- Documented limits, stated where they can be acted on (SECURITY.md):
  cleartext transport (trusted network or tunnel), a static shared token with
  no rotation surface, and a warning that a same-machine TLS reverse proxy
  would bypass the gate by presenting every peer as loopback.

### Added
- **The web UI works from another machine** (D14). Browsers cannot attach a
  header to `<video src>`, thumbnails or download links, so signing in
  (`POST /api/v1/session`, proven by the bearer token) mints a 256-bit session
  id returned as an `HttpOnly; SameSite=Strict` cookie. Reads may ride the
  cookie; **every other method must echo the id in `X-Cut-Session`**, which a
  cross-site page cannot produce from an HttpOnly cookie — CSRF is structurally
  impossible here rather than token-guarded. The access token is never stored
  by the page (only the session id, per-tab), a reload stays signed in, and
  Sign out or `DELETE /api/v1/session` revokes at once. Sessions are in-memory
  with a 12 h TTL, capped at 256 — past the cap a login is refused rather than
  the table growing. UI: a localized sign-in panel, a Sign out affordance that
  appears only when a session exists, and one shared prompt so parallel 401s
  cannot stack modals.
- `xcut doctor` reports the API posture (remote + token required / token set
  but loopback / no token, remote refused) without printing the token, and the
  serve startup line states which posture it started in.
- Error model: `unauthorized` → 401 and `forbidden` → 403.

### Improved — media pool performance
- **Thumbnails survive a reload.** Capturing one meant pulling real media
  bytes through a hidden `<video>` and spinning up a decoder — per asset, per
  project view. D14 made that cross a network. Captured frames are now kept in
  `localStorage` keyed by asset id and invalidated by the content fingerprint,
  and a cached one is painted as an `<img>` without touching the media at all.
  Measured in a real browser on a 3-asset project: **3 media requests per view
  → 0 after reload**, and corrupting one stored fingerprint re-fetched exactly
  that one asset. Storage growth is capped (400 entries) and a quota error
  drops the thumbnail set rather than breaking the panel — they are decoration.
- One timeline repaint per batch instead of one per arriving thumbnail: with N
  assets each landing at its own moment, the unguarded redraw cost up to N
  full timeline renders per refresh.

### Added
- **`xcut eval --baseline results.json`** — A/B an algorithm change against a
  previous run in one command, instead of diffing two result files by hand. It
  prints per-case and macro deltas (P/R/F1, ranges, dup) and refuses to lie:
  a case that errored on either side is `NOT COMPARABLE`, cases new to or gone
  from the manifest are labelled, and a different `--iou` prints
  `WARN hit_iou differs` because those numbers compare two yardsticks, not two
  algorithms. A manifest passed as a baseline is rejected (it shares
  `{"version":1,"cases":[…]}` shape with a results document, so the check is on
  `generated_at`/`hit_iou`, not shape), and `--baseline` cannot point at the
  file `--out` is about to overwrite.
- `event_config.rally_chunk` — the dense-span piece length (default 30 s,
  validated with a 4 s floor so a preset cannot flood the candidate set). It is
  the one place where a reel's coverage could in principle be widened, so it is
  now tunable per source; measured on the real match, **the existing default is
  the best value found** (20/14/10/8/6 s give P 0.810/0.799/0.823/0.766/0.706
  against 0.886), so this is a tuning surface for other footage, not an
  improvement here.

### Improved — badminton highlight quality, measured on real footage
- Ground truth derived automatically instead of by scrubbing: a burned-in
  scoreboard only changes when a point ends, so a scene-difference detector
  aimed at the scoreboard crop yields the rally boundaries. Cross-checked
  against the final score (22:19 = 41 points; the recipe produced 43 candidates
  for 41). Recipe and its three gotchas are in docs/EVAL.md.
- **A highlight now covers the match instead of its first two thirds.** The
  reel's budget was filling entirely from the highest-scoring early chunks;
  `diversity.max_per_window` + `phases` cap how many clips any one time region
  may contribute, so the closing phase — match point included — is present
  (confirmed by inspecting frames, not only by the metric). Phases derive from
  each asset's own duration, because an absolute window length silently
  truncates short sources: the existing rally integration test caught exactly
  that bug on its 47-second fixture.
- **Clips sit inside rallies rather than across a point.** The trimmed window
  was placed at the *middle* of its segment, but rally chunks are cut on quiet
  valleys, so the middle of a 30-second chunk is frequently the pause after a
  point — the reel opened on players towelling off. Placement is now anchored
  at the segment start. Measured on the real game: precision **0.742 → 0.886**,
  recall 0.094 → 0.112, F1 **0.167 → 0.199**.
- Two alternatives were tried and **rejected on measurement**, recorded so
  nobody re-spends the effort: anchoring on the median audio onset (P 0.789)
  and on the motion peak (P 0.770) both lost to plain start placement — in a
  shared hall the loudest smash is often another court's, and our own peak
  lands at the point's end, pushing the window over the boundary.
- Known cost, stated rather than hidden: `ranges_hit` went 7 → 6, and it is
  capped by the budget anyway (8 clips of 8 s cannot represent 41 rallies of
  ~10 s). Representing more of a match needs a longer reel or several scored
  windows per long segment, which needs a per-window activity profile on
  `event.Segment` that does not exist yet.

### Improved — resource and transfer footprint (measured, session #14)
- **`GET /api/v1/projects/{id}` shrank from 5936 to 558 bytes (−90.6 %)** on a
  real recording. `storage.Asset.ProbeJSON` holds the whole ffprobe document —
  kept for diagnostics, read back on load, and therefore serialized into every
  response, where no client of the API used it (zero references in the UI) and
  the UI polls that endpoint. It is now `json:"-"`: stored, not served, with a
  test that fails if the blob reappears. A 20-asset project drops from ~100 KB
  to ~11 KB per poll.
- Re-measured rather than assumed: idle serve is 16.4 MB working set flat and
  **0.00 s CPU over 45 s** with the auth gate and session store in place (a
  loopback serve never allocates the session map), and a real 603-second 720p30
  analysis runs in 28.1 s (0.047× realtime) peaking at 23.8 MB in-process /
  55.7 MB in the ffmpeg child — so the streaming analyzer work from session #7
  still holds under real load. Numbers are in docs/PERFORMANCE.md.

### Fixed
- **Kylin V10 aarch64 could not import anything.** Its packaged FFmpeg's
  Hisilicon OMX plugin logs to stdout while ffprobe writes JSON there,
  interleaving mid-line, so every import failed with "cannot parse probe
  output: invalid character '1'" — a message pointing at the user's file for a
  toolchain problem. XCut now names the offending build and the remedy
  (`XCUT_FFPROBE` at a stock build). A buffer-repairing filter was built, run
  against the real captured output, and rejected on evidence: it recovers a
  parseable document with the log text inside `codec_long_name`. Refusing
  outranks repairing ambiguously (DECISIONS D13), and the rejected approach is
  pinned by `TestFilteringWouldNotBeSafe`.
- **ARM64 is now runtime-verified end to end** on that Kylin box (it was
  compile-checked only): with a stock FFmpeg the full chain works — import →
  analyze (3 tracks / 48 samples / 1 event) → timeline → render 4.8 MB in
  3.1 s → output probed back at 10.02 s with both streams → cache/cleanup.
- **The Linux gate had been red for twelve sessions.** Running
  `scripts/check.sh full` on a Linux node for the first time since the
  Windows-only installer landed found 8 failures, all A/B-confirmed
  pre-existing at the previous session's HEAD. Three tests asserted one OS's
  shape as universal truth (subtitle-path escaping, a path resolved through
  `filepath.Abs`, the installer happy path); they now assert what each
  platform guarantees, the installer tests skip off Windows, and two new tests
  pin the refusal non-Windows users actually get. The node is green: 38 Go
  packages including `-race`, plus the Rust leg, against FFmpeg 6.1.1.
- **Content-Type no longer comes from the host MIME table.** `ServeContent`
  resolves an extension through the platform, and the answer differs by host:
  Go's built-in table calls `.webm` `audio/webm` while a distro's
  `/etc/mime.types` calls it `video/webm`, so the same build served a video
  preview as audio on Linux and the `<video>` element refused to play it. The
  containers and subtitle formats XCut actually serves are now named in code,
  everything else keeps the sniffed fallback.
- Eight Linux test failures found by running `scripts/check.sh full` on a
  Linux node for the first time since the Windows-only installer landed
  (A/B-confirmed pre-existing at the previous session's HEAD, not
  auth-related): the installer tests now skip off Windows with two new tests
  pinning the refusal non-Windows users really get, `TestEscapeSubsPath`
  asserts the OS-independent escaping with the separator conversion left to
  Windows, and `TestMatchAssetIDs` builds fixtures absolute in the running
  OS's shape (a hardcoded `D:\…` is only absolute on Windows, where
  `filepath.Abs` is the identity).
- `TestSetupStatusShapeWhileDownloading` raced the installer goroutine against
  Go's TempDir cleanup (a write landing after removal started), which made the
  gate fail roughly one run in four with "directory is not empty"; cleanup now
  waits for a terminal install phase.
- SECURITY.md still described the HTTP API as unbuilt ("when it exists", "no
  HTTP API yet") several sessions after it shipped; its controls section now
  matches what the code does.

## [Unreleased] — 2026-09-18 night session #13 (owner directive: UI + installer)

### Added
- **One-click FFmpeg install** (owner directive): when the app detects a
  missing FFmpeg, the warning strip grows an install button — source
  (official Gyan.dev build on GitHub), size (~110 MB) and license (GPL)
  are stated before it runs. The artifact URL, exact byte size and SHA256
  are pinned in code: a moved or tampered download refuses loudly. Only
  `ffmpeg.exe`/`ffprobe.exe` are extracted (zip-slip guarded) into the
  exe-neighbor `bin\` directory; the installed ffprobe must answer
  `-version` before the app calls it done. Tool resolution re-probes the
  neighbor locations live, so the pipeline is usable the moment the
  install lands — no restart. Non-Windows refuses honestly (use the
  system package manager).
- **True Windows installer** (owner directive): `xcut-<version>-windows-
  setup.exe` (~6 MB, Inno Setup) with Start-menu/desktop shortcuts, an
  InfoBefore page stating the auto-install and configure-your-own-AI
  policies (en+zh), and a real uninstaller. FFmpeg stays unbundled by
  design — the pinned in-app download keeps the installer small and the
  distribution license-clean.
- **AI sidecar visibility**: `/health` reports `ai_sidecar: ok|missing`
  (LookPath only — health never spawns the sidecar), and the subtitles
  panel appends a configure hint when nothing resolves. The configure-
  your-own-AI interface itself (sidecar protocol v1, `workers.ai_bin`,
  `--model`/`--lang`) already existed; it is now visible when unset.
  Bundling a model into the core stays rejected by design (D3): useful
  STT models weigh tens-to-hundreds of MB against a ~13 MB binary — the
  sidecar route keeps capability growth on the user's terms.

### Changed
- **UI v2 visual refresh** (owner directive): gradient brand/accent
  system, layered surfaces with hairline highlights, focus-visible
  rings, refined buttons/inputs/status pills, animated dropdown and
  banner, pulsing running-job state, ambient glow, reduced-motion
  support, favicon. Trim handles are now drawn — the JS pointer targets
  existed since session #8 but no CSS ever painted them. Timeline blocks
  repaint once thumbnails finish capturing (no more dark blocks until
  the first click).

### Fixed
- **`xcut subtitles --out` / `xcut eval --out` could overwrite their own
  inputs**: a subtitle file or eval results written onto the source
  media (or a hand-written annotation manifest) truncated irreplaceable
  data. Both commands now share the render guard's same-file identity
  check and refuse loudly before any work starts.
- **Hidden state resurrection in the UI**: author `display:flex` on the
  editor shell and warning strip outranked the UA's `[hidden]` rule, so
  the empty-state editor rendered beneath the placeholder and the
  warning strip could never hide. A `[hidden]{display:none!important}`
  base rule now owns hiding.
- **`.gotmp/` no longer trips the secret scanner**: the never-committed
  scratch area (verification scaffolding: browser profiles, fixture
  media) is dense with token-shaped strings; the gitleaks allowlist
  covers only that ignored directory — tracked tree and full history
  stay scanned.

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
  normalization does not remove it) — on a real 10-minute match the
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
