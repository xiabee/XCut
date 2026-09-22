# XCut Roadmap

Living document. Near-term milestones are concrete; far-term is directional.

## Phase 1 — Deterministic Core (current)

- [x] M0 Scaffold: Go module, error model, config, CLI framework, docs
- [x] M1 `xcut doctor`: environment detection (ffmpeg/ffprobe/workspace/disk/db/optional workers)
- [x] M2 Media core: import + ffprobe + fast fingerprint
- [x] M3 Storage & jobs: SQLite + migrations, project/asset/job model, crash reconciliation
- [x] M4 Baseline analyzers: scene, motion, audio RMS/silence → FeatureTracks → EventSegments
- [x] M5 Timeline IR: versioned JSON, validation, serialization
- [x] M6 Style engine: presets, scoring, clip selection (generic_highlight, badminton_highlight, ktv_mv)
- [x] M7 Renderer: timeline → normalized clips → concat → verified MP4
- [x] M8 E2E: `xcut auto` import→analyze→timeline→render on real FFmpeg fixtures
- [x] Nightly hardening: tests, benchmarks, security checks, packaging notes (session #1)
- [x] Local quality gate (scripts/check.*) + manual-dispatch CI (D11) + workspace lock + UI XSS hardening + worker output caps (session #2)

## Phase 2 — Service & UI

- [x] `xcut serve`: localhost HTTP API (`/api/v1`: health, projects,
      assets, jobs) — the embedded zero-dep web UI shipped alongside it
      (project CRUD, import, analyze → timeline → render, subtitles).
- [x] Proxy generation for analysis (configurable resolution/fps decision logic)
- [x] Cache eviction (LRU, size-capped) + `xcut cache` tooling
- [x] Cross-platform CI (Windows/Linux amd64+arm64), release packaging
  (CI matrix runs ubuntu+windows with `-race` plus a packaging job on
  manual dispatch — D11; linux amd64+arm64 compile-checked every full
  gate; Linux runtime verified end-to-end via WSL kali + ffmpeg 8.1.1 in
  session #1 and re-verified in session #5; the "full mode on the MR
  node" ops item stays tracked in the night backlog — that node's
  transport is `local` and cannot be driven remotely).
  **arm64 status, stated precisely (session #15):** the published arm64
  binary runs on real Kylin V10 SP1 — `doctor` green, and the full token +
  session path driven over its tailnet address — but the arm64 *test suite*
  is not green there: 15+ tests fail on the distro FFmpeg (ffprobe JSON
  corruption, and 4.2.2 has no `xfade`), not on product logic. Getting a real
  arm64 suite run needs a pinned stock FFmpeg on that node, which is an owner
  decision, not something to install quietly.

- [x] API authentication (D12, session #14): a bearer token gates every
      non-loopback peer of `/api/v1`, loopback stays trusted, and
      `listen_remote` became a usable option (with a token) instead of a
      refusal.
- [x] Remote web UI (D14, session #14): a browser cannot put a header on media
      URLs, so login mints an HttpOnly session cookie for reads while writes
      must echo the id — the asymmetry is the CSRF defence. Verified in a real
      browser over a LAN peer.
- [x] Remote-access finishing (closed across sessions #14–16): the TLS/tunnel
      runbook (`docs/OPERATIONS.md` — SSH tunnel driven end to end, plus the
      Tailscale recipe now measured on a real tailnet peer), the sign-in panel's visual pass (real
      browser, 1280×800 + 390×844 — which caught the credentialless-poll
      budget defect, fixed), and token rotation decided as "edit config +
      restart" with the reason recorded in D12. The open question that remains
      is owner-level, not engineering: the token still crosses a cleartext
      wire by design, so remote binds stay trusted-network/tunnel-only.

- [x] Platform legs of the quality gate (session #14): the same gate now runs
      green on a Linux node (Go incl. `-race`, Rust incl. clippy) and the
      binary runs a full workflow on Kylin V10 SP1 aarch64. Twelve sessions of
      "remote CI" had been Windows-on-Windows and could not see platform
      drift (D11 amendment).
- [ ] FFmpeg component install off Windows: the pinned one-click installer is
      Windows-only, so a Kylin/ARM64 box needs a manual `XCUT_FFMPEG`/
      `XCUT_FFPROBE` — proven to work, unproven as product UX. Deciding this
      also decides the packaged-vs-stock FFmpeg question (D13).

## Phase 3 — Rust worker & vertical depth

- [x] `xcut-worker-media` (Rust): protocol v1 + audio RMS shipped (benchmark parity with ffmpeg — kept as optionality); frame diff / onset pending real need
- [x] Manual timeline editing polish: per-clip preview, drag reorder
- [x] Badminton pipeline v2: rally clustering (transient-based), court ROI analysis, hit-driven scoring, diversity dedup; `xcut eval` harness for measurement
- [x] Point boundaries imported from a burned-in scoreboard (sidecar `score_changes`,
      `xcut boundaries`, manifest `score_roi`, UI region picker, analyze-stage measurement) —
      the one rally-end signal the core could not derive itself; measured P 0.886 → 0.998 on
      the owner's match (D15). Stroke-level ranking (which rally is best) still open
- [x] KTV pipeline v2: onset-density weighted selection (honest naming — high-energy signal, no chorus claims)
- [x] AI sidecar protocol v1 (capabilities/health/analyze, bounded output), capability detection in doctor; reference sidecar ships, models remain optional/local

## Phase 5 — Product-grade auto-editing (opened 2026-09-22, owner direction)

Goal: an auto-edit that produces something worth posting, not just something
correct. Grounded in what short-form practice reports today rather than on
impression — the recurring numbers are: a decision happens in the first 3
seconds, a visual change every 3–5 s (2–3 s in high-tempo content), key cuts
synced to the music beat, captions of 5–7 characters per line held 2–3 s in
white-with-thin-outline or a translucent box, and a slightly longer static shot
after a rapid burst to let the viewer breathe. Sources are listed at the bottom
of this section; they set targets, they are not evidence about our own footage.

Every item states how it is measured before it is built, because the eval harness
(`xcut eval`) is what keeps "new style" from meaning "new untested heuristic".

- [x] B1 — Beat grid (`卡点` foundation). `analysis.EstimateBeatGrid` folds the
      onset track to a single phase, keeps the *longest* period that explains
      ≥90% of the onsets, refines it by least squares, and stops at the last
      onset plus half a period rather than at the requested horizon.
      Deliberately a pure derivation, not a fourth `FeatureTrack`: estimating it
      from the cached onset track costs microseconds, so a cache entry would be
      one more thing to invalidate for no gain. Wiring is B2's.
      Measured: 5 estimator cases (exact click grid, ±30 ms jitter, too-few-onsets
      refusal, every-other-click, horizon clamp) plus one through the real chain —
      `testmedia.GenerateRally(HitEvery: 0.5)` → shipped `AudioOnsetAnalyzer` →
      estimator, reporting `period=0.4999 bpm=120.0 coverage=1.00 beats=24`.
      Four mutations of the constants and the two rules each killed a named
      assertion (`ran=5`, no build failure).
- [x] B1b — The grid refused (and mis-fitted) a bed longer than ~96 beats, found by
      using `--music` through the UI on a 60 s metronome: 20 s of exact 0.5 s clicks
      gave `bpm=120.00 coverage=1 beats=39`, 60 s of the *same* clicks gave "no beat
      grid the estimator will believe, onsets=119", and 119 onsets at 1.0 s came back
      as `period=0.2 bpm=300 coverage=1.000` — a confidently wrong grid, worse than a
      refusal. The guess in the original version of this line (judge the candidate by
      its fitted period) was right about the cause and wrong about the cure: doing only
      that traded the refusal for the mis-fit, because the fit's *mean phase* lands
      exactly halfway between the clicks of an alternating lattice, where every click
      sits at precisely the tolerance distance and a grid twice as slow scores full
      coverage. The shipped shape has no ladder at all: every interval count of the
      first and last onset proposes a period, the least-squares fit sharpens it, and
      the phase is anchored on a real onset.
      Measured: over every whole BPM from 30 to 300 on a 60 s click lattice, **121
      believed / 142 refused / 8 wrong before, 271 / 0 / 0 after**; `--music` on the
      60 s bed now reports `bpm=120.00 coverage=1 beats=119` with `cuts on the beat: 1
      of 8 clips`, a 3-minute bed (359 onsets) reports the same grid, and the match's
      own hall audio (145 onsets) is still refused — the answer that was already right.
      The six B1 cases stayed green. Seven mutations each killed one named assertion;
      the fit budget (1 200 onsets, beats still projected to the last click) is the one
      part no test can observe — 20 000 onsets cost 127 ms with it and 44.7 s without —
      so it is carried by timings in `docs/PERFORMANCE.md` rather than a fake assertion.
- [x] B2 — Beat-snapped selection (`卡点`). `beat_snap_tolerance` on the preset,
      `--beat-snap seconds|off` on timeline/auto/eval, `beat_snap` on the API: a
      clip end that nothing else fixed may move to the nearest beat of the
      source's own grid — never past the event's own end, never lengthening the
      clip past `max_clip_duration`, and never over a measured point end (the
      scoreboard rule wins by construction). Each moved end carries `beat`
      metadata, at four decimals because a refined grid is not a round number.
      Measured: end to end on the 120 BPM click fixture through the real analyzer
      and cache — the same reel asked for twice, off and on, with a control that
      the ends *start* off the lattice and an assertion that every one finishes on
      it (tolerance = half a period, so "the rule never ran" cannot pass); four
      mutations, each killed by the assertion named. On the owner's match the rule
      is **inert, and the reason is now measured rather than assumed**: 1493
      onsets in the hall audio yield no grid the estimator will believe (no period
      explains ≥90% of them), and in the marked configuration all 16 ends are
      point-pinned to begin with. Eval reports the same numbers at off, 0.12 and
      0.25 s (F1 0.389 marked / 0.330 unmarked) — recorded, not tuned away, because
      it reshapes B4: the pulse a reel cuts to has to come from the music laid
      under it, not from the source's own audio.
- [x] B3 — Camera motion (`运镜`): a per-clip framing plan in the timeline IR
      (`{"motion":{"zoom":…,"from":[x,y],"to":[x,y]}}`) rendered through `crop` —
      window sized to the canvas's aspect, magnified to fill it, center sliding
      over the clip's own time. Styles ask for it with `camera_motion`
      (`punch_in` | `drift` | `roi`, plus a zoom); `roi` centers the window on the
      region the project was analyzed with, and a 9:16 canvas over a 16:9 source
      is the vertical reframe (same arithmetic, asserted).
      Measured: three levels, because the filter string proves nothing about the
      picture — the text (aspect-derived window, per-frame `t`, clamps at both
      edges of both axes, no time term for a still plan), the command (rendered
      through `Render()` against the package's stand-in FFmpeg and read back from
      the child's argv, including that a clip with no plan grows no crop stage),
      and the pixels (a fixture whose only content is the top-left quadrant:
      YAVG 94.17 → 19.24 when the window drifts to the far corner, 39.25 → 39.36
      with no plan). Cost is in `docs/PERFORMANCE.md`: +0.5 s per minute of output
      (8.6 s against 8.1 s) and +11.5% bytes at fixed CRF, so the render budget
      needs no new ceiling — the size is the number to watch.
      Not claimed: a plan *centered* on the ROI is not a plan that keeps the whole
      region inside the frame — the selector does not know the source's pixel
      aspect, and only the renderer does. A fit guarantee is either the renderer's
      or the UI's, and it is recorded as the remainder rather than asserted.
      Per-clip picker stays B6; no shipped preset enables motion, because cropping
      a broadcast can cut the score bug out of the shot and no aesthetic claim has
      been measured here to trade against that.
- [x] B4a — The music bed, which B2's measurement made the prerequisite: a run
      names a track (`--music`), the pipeline analyzes it with the shipped onset
      analyzer through the same cache, estimates *its* grid, and that grid outranks
      whatever pulse the location audio carries. Naming a bed implies cutting to it
      (the product's ±0.12 s default; `--beat-snap off` opts out), and the timeline
      document records the choice (`music`, `music_gain`, `source_gain`, `music_bpm`)
      so the render mixes it without being told twice. A file with no audio stream is
      refused; a file with audio but no believable grid is not an error — the music
      plays and the cuts stay where the length rules put them.
      Measured: on a footage clicking at 0.4 s under a bed clicking at 0.5 s, the
      rendered reel's one free end moved to the **bed's** grid (1/1 moved, 1 on the
      bed's lattice, 0 on the footage's, `music_bpm` 120.04) — precedence read off
      the geometry, not asserted by the code. The mix is audible in the output file
      on its own terms: 9 transients that only the bed's lattice explains, against 0
      in the same cut rendered without one, with the duration unchanged to the
      millisecond. Five mutations killed by named assertions, one of them a renamed
      metadata key (`the mix command lost "volume=0.9000"`, `a missing bed must fail
      the render`). Cost: 11.3 s against 10.2 s render wall per 60 s of output,
      +3.3% bytes (docs/PERFORMANCE.md).
- [x] B4b — The ruler before the cut: a pacing readout measured off the built
      document (`timeline.Pacing`, printed by `xcut timeline` and `xcut auto`) —
      shot count, mean/median/longest played shot, and the output position of the
      reel's top-scored shot. It exists because every metric the harness has is
      set-based: one 15 s stretch and five 3 s cuts score identically, which is how
      "new style, same F1" kept being allowed to mean "new untested heuristic".
      Measured: 7 tests over real documents (an equal-length control, a half-speed
      clip so source span ≠ played span, an out-of-order score tie, an unparsable
      score, and the unscored/empty cases that must say nothing), and 7 mutations
      each killed by the assertion it targets (`median = 6, want 3.5`,
      `HookSeconds = 900, want 200`, `ScoredShots = 3, want 1`). On the owner's
      match the shipped preset reads
      `14 shots, mean 8.0s, median 8.0s, longest 8.0s, top shot starts at 24.0s`:
      every shot sits on the preset's `max_clip_duration`, so the ceiling — not the
      scoring — sets its pace, and the best moment lands a quarter of the reel in.
- [x] B4c — Two presets cut on that ruler, plus the IR knob one of them needed:
      `clip_order` (`chronological` — the unset value, so nothing existing changes
      — or `hook_first`: the style's top-scored shot leads, the rest stay in match
      order; an unknown name is refused at load). `sports_vertical` (9:16, 4.0 s
      ceiling, `roi` framing that yields **no plan** when the project has no
      analyzed region) and `beat_shortform` (1080×1920, 1.0–2.8 s, ±0.12 s snap,
      hook first) are embedded, so the style list and UI picker see them without a
      hand-kept registry.
      Measured, at the incumbent's 60 s budget on the owner's match: 15 clips vs 8,
      recall 0.100 vs 0.108, precision 0.785 vs 0.850, **longest untouched rally run
      4 vs 10** — and its `ranges_hit` *lower* (4 vs 6) because a hit needs IoU
      ≥ 0.3 and a shot inside a rally scores `len(shot)/len(rally)`: 4 s in 15 s is
      0.267. The pacing line reads `mean 4.0s, median 4.0s, longest 4.0s` where the
      incumbent reads 8.0/8.0/8.0: halving the ceiling halves the shots, and the
      saturation at the ceiling **persisted** — recorded, not argued away. Per the
      rule stated before building it, the incumbent stays the default: the new
      preset does not beat it on the annotated case.
      Not claimed: `beat_shortform` has been demonstrated on no footage this project
      owns. Activity-mode segmentation finds one continuous span on a fixed
      broadcast camera (`chunks_considered=0`), so its eval row is 0.000/1 clip —
      the shipped `generic_highlight` behaves identically here, which is the content
      talking, not the preset. Its shape is carried by constructed segments plus 7
      mutations, each killed by the named assertion. A multi-cut moving source is
      the missing input (same gap as the second-match question).
- [ ] B5 — Caption/subtitle styling to the convention above (line length,
      dwell time, white + thin outline or translucent box), shared by the
      subtitle burn and the KTV lyric path.
      Partially landed as B5a (geometry): `subs.KaraokeStyle` carries the reel's
      canvas and the writer derives PlayRes, font, outline, shadow and margins from
      it — 1080×1920 → `Fontsize 72 / MarginV 107`, 720p unchanged at 48/40, and a
      frame too small to round a stroke to ≥1 still gets one. The transcript stage
      reads the project's timeline for that canvas (proved end to end, and by five
      mutations including one on the caller). B5b added the geometry that can be
      computed: lines wrapped to the frame's usable width (twelve units on a
      1080×1920 frame, a word's separator included), two lines a cue, longer segments
      split into successive cues, and a 1.2 s display floor that borrows silence
      without moving the karaoke fill (`speechEnd` is what the sweep runs to). Six
      assertions on the generated file, six mutations each killed by one of them, and
      one regression caught only by the existing CLI end-to-end test. B5c closed the
      plain path: a transcript with no word timings now gets the same frame, the same
      wrap and the same dwell as a karaoke one — `WriteCaptionASS` lays it out by
      character (nothing said where a word ends) and shares the span by the characters
      each cue carries, with no `{\kf}` sweep to invent a syllable. A second
      transcription replaces the karaoke file the first left behind instead of only
      deleting it, and `xcut subtitles --ass` answers an ordinary transcript with a
      caption rather than an error.
      Remaining: re-styling at burn time (the transcript payload is not stored, so a
      canvas change needs a re-transcription), and any pixel claim about where the
      caption lands — libass's own layout is not verified here, only the file handed
      to it.
      Measured: the generated ASS text is asserted (the wire format, not a
      struct), including a long-lyric case that must wrap rather than overflow.
- [ ] B6 — UI for all of it: beat ticks on the timeline ruler, a per-clip motion
      picker in the inspector, a pacing chip (mean shot length vs the 3–5 s
      target) and a one-tap "post-ready" export (vertical + captions + music
      sync). The web UI and the client shell stay one asset tree.
      Measured: the DOM-structure guards the repo already has, plus a browser
      probe reading the *computed style of the nodes that changed* — a CSS rule
      that renders on nothing has fooled this project before.
      Partially landed as B6a (the pacing chip: `GET …/timeline` returns a derived
      `pacing` object computed by the same `timeline.Pacing` the CLI line uses, the
      chip renders it including the unscored-document branch, its wire keys are
      pinned in Go on both ends, and the browser confirmed the rendered node — with
      the note that no browser runs in CI, so that last level is a performed
      observation, not a gate) and B6b (the music-bed field, the three-way beat-snap
      selector, and a second line stating what the saved document chose — bed + BPM +
      how many cuts followed it, or which of the three "no" cases applied). Remaining:
      beat ticks on the ruler.
      B6c — the per-clip motion picker — was specified here before it was built, and it
      hit the numbers. The mode→geometry rule moved into `style.MotionFor`, which
      `framingPlan` now calls, so the builder and the picker cannot hold two versions of
      what "drift" means; the inspector asks `POST …/motion/plan` and stores the answer
      on the clip through the existing Apply → PUT round trip. Measured against each
      criterion: (1) the two callers agree — a case walks every mode × ordinal ×
      has-region combination, and the pre-existing framing and preset tests pass
      unchanged, which is the half that says the refactor moved nothing; (2) a mode that
      needs a window without one is a refusal naming the fix ("this asset has no region
      to aim at — draw one in the Regions panel first"), and a zoom outside (0,1] is
      refused in the preset's own words; (3) `none` and an unset mode both answer with no
      window and no `framing` claim; (4) the endpoint is project-scoped (another
      project's asset is a 404), reads the region off the asset row instead of trusting
      the page, and answers with `motion.zoom` / `motion.from` / `motion.to` /
      `framing`; (5) the write-back is the ordinary clip save, so the revision check and
      the pre-regeneration backup were not touched — a text guard fails if the page
      starts writing the geometry without its claim; (6) six mutations, each killed by a
      named case: drift stops alternating, roi without a region invents a centre, the
      framing claim is never answered, the zoom bound is dropped, the builder stops
      asking the shared rule, and the page writes the geometry without its claim.
      Not done: no browser was opened, so the select's on-screen behaviour rests on
      `node --check` and that text guard, and hand-picked motion still does not survive
      regenerating the reel — which the pane already says.
      B6d — one tap to a post-ready reel — was specified here before it was built,
      with the numbers it had to hit, and it hit them. A new `export` job type does
      three things in one body and waits for none of them: it builds a timeline if the
      project has none, transcribes if subtitles are asked for and none exist, then
      queues the ordinary render. What was claimed, and what was measured:
      (1) an empty project with a sidecar ends with a timeline file, both subtitle
      artifacts and a *separate* render job row — the test counts both kinds of row and
      wants exactly one of each; the render is not run inside the tap, so it waits for
      `resource.max_render_workers` like any other;
      (2) the `.ass` the tap writes declares 1080×1920 for a canvas that did not exist
      when the request arrived, because the reel is built first;
      (3) with no sidecar the tap still succeeds and the subtitles step reads `skip`
      with a reason naming the sidecar, on the wire and in the panel;
      (4) a project that already has both artifacts gets `reuse` for both and their
      bytes unchanged — proved against a hand-written horizontal reel and a sentinel
      `.ass`, so a stage that ran anyway could not pass;
      (5) two presses cannot both run: 409 from the queue, and the widened partial
      unique index from migration v7 tested by inserting the second active row and by
      upgrading a database that predates v7 with an active job in it;
      (6) six mutations replayed, each killed by a named assertion (the body always
      rebuilds, the body always transcribes, the skip stops naming itself, the render
      runs inside the tap, `export` leaves the exclusive set, v7's type list narrows).
      Not done: no browser was opened to watch the readout render — its markup, keys
      and request literal are pinned by tests, its on-screen form is not — and
      `xcut auto` still has no caption step, so the CLI's one shot stays the
      three-step one.
- [ ] B7 — Resource occupancy: idle targets stay (serve ≈0 CPU, <100 MB RAM),
      and the new stages get measured ceilings — analysis fan-out memory, proxy
      cache bytes, the motion render's cost.
      Measured: numbers in `docs/PERFORMANCE.md` from real runs, "not measured"
      where it has not been measured.
      Partially landed as B7a (2026-09-23, four rows added to `docs/PERFORMANCE.md`):
      serve idle re-measured after the whole Phase 5 arc — 18.1 and 17.7 MB on two
      runs, CPU delta 0.000 s each, no media child alive at any sample; the analysis
      fan-out traced to the knob that actually owns it (`max_ffmpeg_processes`: 2
      children at 107 MB, 4 at 215 MB, wall 18.4 → 12.1 s, ~54 MB per 720p child,
      while `max_analysis_workers` bounds assets in flight and changed nothing on its
      own — the goal table named the wrong knob and now names the right one); the one
      tap measured against the same stages run separately (9.2 s / 343 MB against
      10.1 s / 339 MB, so the orchestration is free); and the xfade render's child
      weighed (566 MB, single process, 1.75× the concat path).
      Remaining: the decision B7a surfaced but did not take — whether
      `resource.ffmpeg_max_memory_mb` should ship with a default instead of 0 =
      uncapped. The number any default has to clear is 566 MB, and the surface that
      tells the user today is `xcut doctor`'s `Process sandbox: OPTIONAL … memory
      uncapped`. Proxy/cache bytes are enforced and tested at unit level
      (`internal/analysis/cache_test.go`) but have no measured row yet.

Order of attack is B1 → B2 → B3 → B4 (each depends on the one before), with B5
independent and B6 landing per feature as its surface exists.

Sources behind the numbers above (industry guidance, not measurements of our own
output — kept visible so nobody mistakes them for evidence):
[ShortGenius 高互动视频制作最佳实践 (2026-03)](https://shortgenius.com/cn/blog/shipin-zhizuo-zuijia-shijian),
[Teleprompter — Trending YouTube Shorts 2026: Top 10 Formats](https://www.teleprompter.com/blog/trending-youtube-shorts),
[Metricool — CapCut Video Editing Tutorial](https://metricool.com/capcut-video-editing/).

## Phase 4 — Desktop client & polish

The desktop client is designed in docs/CLIENT_DESIGN.md (native WebView2
shell over the existing serve pipeline + a modern editing workspace; the
web UI stays the same asset tree served to browsers).

- [x] C1 — native shell: `xcut client` (WebView2 window over the
      in-process loopback server, Windows build-tagged with a
      `--browser`/serve fallback elsewhere; WebView2 detection in
      doctor). Design: docs/CLIENT_DESIGN.md §2–3.
- [x] C2 — workspace redesign: three-pane editing layout (media pool /
      preview / timeline + inspector), modern design tokens, asset
      thumbnails. Shipped 2026-09-13 (client thumbnails, project-scoped
      refresh guards) — session #8.
- [x] C3 — visual timeline: clip blocks sized by duration, editable
      transition badges on joins, drag reorder, click-select, ruler +
      playhead linked to the preview. Shipped 2026-09-13 — session #8.
- [x] C4 — inspector & polish: clip property editing (trim/speed/
      volume), keyboard shortcuts, empty states, docs. Shipped
      2026-09-13 (Delete/Space/Ctrl+S shortcuts; score + why per clip)
      — session #8.
- [x] Desktop packaging (icon, installer/zip, tray) — zip + icon shipped
      in session #8; the true installer (Inno Setup `xcut-*-windows-
      setup.exe`, shortcuts, InfoBefore policy page, uninstaller)
      shipped in session #13 (2026-09-18, owner directive). tray and
      auto-update remain open.
- [x] Model registry (explicit installs, no silent downloads) — resolved
      in session #13 (2026-09-18) as the explicit-configuration surface:
      `/health` reports `ai_sidecar: ok|missing`, the subtitles panel
      shows the configure hint, and the sidecar protocol + `workers.ai_bin`
      stay the only registration path. The core never downloads models
      (D3); FFmpeg auto-install is the one pinned-source exception and it
      downloads nothing until the user clicks.
- [x] Sandbox options for FFmpeg, job-object rung (Windows): every child joins
      a kill-on-close job object (session #8), and
      `resource.ffmpeg_max_memory_mb` caps each child's memory through that
      job (session #16, opt-in, uncapped default; doctor reports the posture).
- [ ] Sandbox options for FFmpeg, container rung (Linux): sandbox the child
      pipeline under a container/cgroup boundary.
