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
- [ ] B4 — New presets on top of B1–B3: `beat_shortform` (music-driven pacing,
      2–3 s shots, hook first), `sports_vertical` (9:16, point-ending clips,
      punch-in on the hit), plus a pacing readout on the existing styles so the
      owner can compare.
      B2 settled what this has to start with: a **music bed the user supplies**, and
      the beat grid estimated from *that* file (import → analyze → `Beats` →
      B2's snap), because sports audio carries no grid to cut to. Rendering has to
      mix it in — which is also where the loudness ceiling lives.
      Measured: each preset has an eval case (synthetic where truth is
      constructed, the owner's match where it is annotated), the harness's
      `--check` gate carries it, and a preset that does not beat the shipped one
      on its own case does not become a default. For the bed: a fixture whose
      clicks are known by construction must have its ends on *its* grid, and the
      muxed output probed for the track's presence and its level.
- [ ] B5 — Caption/subtitle styling to the convention above (line length,
      dwell time, white + thin outline or translucent box), shared by the
      subtitle burn and the KTV lyric path.
      Measured: the generated ASS text is asserted (the wire format, not a
      struct), including a long-lyric case that must wrap rather than overflow.
- [ ] B6 — UI for all of it: beat ticks on the timeline ruler, a per-clip motion
      picker in the inspector, a pacing chip (mean shot length vs the 3–5 s
      target) and a one-tap "post-ready" export (vertical + captions + music
      sync). The web UI and the client shell stay one asset tree.
      Measured: the DOM-structure guards the repo already has, plus a browser
      probe reading the *computed style of the nodes that changed* — a CSS rule
      that renders on nothing has fooled this project before.
- [ ] B7 — Resource occupancy: idle targets stay (serve ≈0 CPU, <100 MB RAM),
      and the new stages get measured ceilings — analysis fan-out memory, proxy
      cache bytes, the motion render's cost.
      Measured: numbers in `docs/PERFORMANCE.md` from real runs, "not measured"
      where it has not been measured.

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
