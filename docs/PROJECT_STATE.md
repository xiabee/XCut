# XCut Project State

> The single source of truth for "what actually works right now".
> A future agent reading only this file should know the real state.

Updated: 2026-09-06 ~23:59 (+08:00) — start of nightly session #1

## Version / HEAD

- Version: 0.1.0-dev
- HEAD: (see `git log`; committed incrementally during the night)

## Working Architecture

- **Go core** (`cmd/xcut`): CLI with global flags (--config/--workspace/-v/-q),
  typed error model (`internal/xcerr`), config system (defaults < JSON file <
  env < flags; loopback-forced listen), package layout per docs/ARCHITECTURE.md.
- **Storage**: SQLite via modernc.org/sqlite (pure Go, no CGO), WAL, migrations.
- **Media**: ffprobe/ffmpeg via exec.CommandContext, no shell, timeouts.
- **Rust worker**: not yet introduced (deliberate; see DECISIONS D2).

## Implemented

- `xcut version`, `xcut config show|path`
- (updating as milestones land…)

## Actually Tested

- `go build ./...`, `go vet ./...`, smoke: `version`, `config show`
- (updating…)

## Known Issues

- None yet.

## Performance

- Not measured yet.

## Security Posture

- exec: argument-vector only, no shell; timeouts everywhere (landing M2+).
- Config forces loopback listen unless `listen_remote` (no auth yet → remote
  mode rejected at serve time).
- Path safe-join guard: landing with workspace module (M1+).

## Next Priorities

1. M1 doctor (env detection)
2. M2 media probe + fingerprint
3. M3 storage/jobs
