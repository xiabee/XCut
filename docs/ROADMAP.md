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
  transport is `local` and cannot be driven remotely)

- [x] API authentication (D12, session #14): a bearer token gates every
      non-loopback peer of `/api/v1`, loopback stays trusted, and
      `listen_remote` became a usable option (with a token) instead of a
      refusal.
- [x] Remote web UI (D14, session #14): a browser cannot put a header on media
      URLs, so login mints an HttpOnly session cookie for reads while writes
      must echo the id — the asymmetry is the CSRF defence. Verified in a real
      browser over a LAN peer.
- [ ] Remote-access finishing: a documented TLS/tunnel recipe (the token and
      media cross a cleartext wire today), a visual pass over the sign-in
      panel, and token rotation as an operator action.

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
- [x] KTV pipeline v2: onset-density weighted selection (honest naming — high-energy signal, no chorus claims)
- [x] AI sidecar protocol v1 (capabilities/health/analyze, bounded output), capability detection in doctor; reference sidecar ships, models remain optional/local

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
- [ ] Sandbox options for FFmpeg (job objects / containers)
