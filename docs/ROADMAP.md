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
- [ ] Nightly hardening: tests, benchmarks, security checks, packaging notes

## Phase 2 — Service & UI

- [x] `xcut serve`: localhost HTTP API (`/api/v1`: health, projects, assets, jobs) — web UI still pending
- [ ] Proxy generation for analysis (configurable resolution/fps decision logic)
- [ ] Cache eviction (LRU, size-capped) + `xcut cache` tooling
- [ ] Cross-platform CI (Windows/Linux amd64+arm64), release packaging

## Phase 3 — Rust worker & vertical depth

- [x] `xcut-worker-media` (Rust): protocol v1 + audio RMS shipped (benchmark parity with ffmpeg — kept as optionality); frame diff / onset pending real need
- [ ] Manual timeline editing polish: per-clip preview, drag reorder
- [ ] Badminton pipeline v2: rally clustering, court ROI, quality/stability scoring
- [ ] KTV pipeline: beat/onset-aware selection, chorus heuristics
- [ ] AI sidecar protocol v1 (optional Python worker: Whisper/VLM), capability detection

## Phase 4 — Polish

- [ ] Desktop packaging (Wails/Tauri wrapper, tray), installer
- [ ] Model registry (explicit installs, no silent downloads)
- [ ] Sandbox options for FFmpeg (job objects / containers)
