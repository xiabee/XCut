# XCut Project State

> The single source of truth for "what actually works right now".
> A future agent reading only this file should know the real state.

Updated: 2026-09-07 00:45 (+08:00) — nightly session #1, post-M8

## Version / HEAD

- Version: 0.1.0-dev
- HEAD: afeac09 (M8) — M0..M8 all committed as milestone commits
- Branch: main

## Working Architecture

- **Go core** (`cmd/xcut`): CLI (registry-based, signal-aware, exit codes),
  typed error model (10 codes), config precedence defaults<JSON<env<flags
  (loopback-forced listen), resource budget config.
- **Storage**: SQLite via modernc.org/sqlite (pure Go, NO CGO), WAL,
  busy_timeout, FK on, code-driven migrations (schema v1: projects/assets/jobs).
- **Jobs**: DB-backed queue, bounded concurrency semaphore, progress,
  panic→failed, startup orphan reconciliation.
- **Media**: ffprobe/ffmpeg via exec.CommandContext, no shell, timeouts,
  global process-limit semaphore.
- **Analysis**: FFmpeg analyzers (frame_diff via signalstats YDIF @2fps/640w;
  audio RMS 0.5s windows), fingerprint+config cache, atomic saves.
- **Events**: deterministic activity-segment builder (cuts split, gaps merge,
  min duration, explainable score).
- **Timeline**: versioned JSON IR (v1), strict validation w/ MediaLookup.
- **Style**: JSON presets (embedded + workspace overrides), schema-validated,
  deterministic greedy selection.
- **Render**: per-clip normalize → concat demuxer → ffprobe verify → atomic
  rename from .partial.
- **Rust worker**: protocol defined, implementation pending (next milestone).

## Implemented & Working (verified by tests/CLI)

- `xcut version|config show|init|doctor|cleanup [--dry-run]`
- `xcut project create|list|show|delete`, `xcut jobs <project>`
- `xcut import <project> <file...>` (probe + fingerprint, job-recorded)
- `xcut analyze <project> [assetID...]` (cached, deterministic)
- `xcut timeline <project> [--style generic_highlight|badminton_highlight|<file>]`
- `xcut render <project> [--out path]`
- `xcut auto <file...> [--style --project --out]` — full pipeline

## Actually Tested

- `go test ./...`: 11 packages green, including:
  - CLI-level E2E (real ffmpeg): init→auto→jobs on lavfi fixture
  - render: verified MP4 (codec/size/duration), failure atomicity
  - timeline: 20 mutation cases + 300-iteration property test
  - workspace: traversal/UNC/reserved-name/symlink-escape matrix
  - storage: FK enforcement, migration idempotence, cascade delete
  - job: concurrency bound, orphan reconciliation
- Benchmarks: see docs/PERFORMANCE.md (measured 60s 1080p fixture)

## Known Issues

- Luma-based cut detection misses chroma-only cuts (red→green). Future: add
  chroma-diff signal or scdet filter.
- Mixed audio/no-audio sources supported per-clip (anullsrc), but transition
  types other than "cut" are rejected by the renderer (fade pending).
- Concurrent xcut processes on one workspace are unsupported (job reconcile
  assumes single live process). File lock pending.
- `go run` smoke tests show CRLF warnings (cosmetic; .gitattributes in place).

## Performance (measured, 60s 1080p30 testsrc2 fixture, 32-core desktop)

- Analysis cold: ~2.5s job time ≈ **0.05x realtime** (cache: ~instant)
- Render: 10s timeline → 1.5s ≈ **0.15x** output duration
- Details: docs/PERFORMANCE.md

## Security Posture

- exec: arg-vector only, no shell, timeouts everywhere, thread caps,
  global process limit.
- Workspace path safety: SafeJoin (traversal/absolute/UNC/reserved/symlink).
- Loopback-only listen; remote mode requires explicit config (auth pending →
  serve refuses remote).
- No telemetry; secrets via env/git-ignored files.
- Known limitation: ffmpeg parses untrusted media in-process (no OS sandbox).

## Next Priorities

1. Rust worker (`xcut-worker-media`): protocol v1 + audio RMS op + Go client,
   benchmarked against the FFmpeg path (decision D2 evidence).
2. `xcut serve`: localhost HTTP API /api/v1 (projects, jobs, health).
3. CI (GitHub Actions): go vet/test on windows+linux, cargo test.
4. Security tests: malformed timeline/style JSON fuzz-lite, oversized config.
5. Benchmark harness script + longer-fixture baselines.
