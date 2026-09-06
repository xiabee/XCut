# AGENTS.md — XCut

Instructions for AI agents (and humans) working in this repository.

## First move

Read **`docs/PROJECT_STATE.md`** — it is the single source of truth for what
actually works right now, what is tested, and what the known issues are.
Then skim `docs/DECISIONS.md` (why the architecture looks like this) and
`docs/ARCHITECTURE.md` (module map, worker protocol).

## Non-negotiable rules

1. **Go is the core; Rust is an optional worker; Python is never a core
   dependency.** No FFI — workers speak JSON over stdin/stdout.
2. **No shell, ever, for external processes.** `exec.CommandContext(bin,
   args...)` with per-call timeouts only. User text is argv data, never a
   command string.
3. **User media is untrusted input.** All workspace-internal paths go through
   `workspace.Workspace.SafeJoin`. The HTTP server binds loopback only;
   refusing remote binds until auth exists is a feature, do not "fix" it.
4. **Nothing unbounded**: jobs, ffmpeg processes, cache bytes, temp bytes,
   log growth all have configured ceilings (config `resource.*`). If you add
   a new growth axis, give it a budget and enforce it.
5. **Timeline before FFmpeg**: analyzers never generate ffmpeg commands; all
   editing decisions materialize as a validated `timeline.Timeline`.
6. **`xcut serve` idle CPU ≈ 0 and idle RAM < 100 MB** are product goals —
   no background scanning loops, no eager work.

## Commands

```sh
go build ./... && go vet ./... && go test ./...     # Go (CI runs with -race)
cd crates/xcut-worker-media && cargo clippy --all-targets -- -D warnings \
  && cargo fmt --check && cargo test                # Rust
powershell -File scripts/build-release.ps1          # package (or the .sh)
```

CI (GitHub Actions) runs Go on ubuntu+windows with `-race`, Rust lint/test,
and a packaging job with an artifact smoke test. Keep it green; fix causes,
never assertions.

## Conventions

- Commits: `feat(core|media|analysis|timeline|style|render|api|worker): …`,
  `fix:`, `perf:`, `security:`, `test:`, `docs:`, `ci:`, `build:`.
  Milestone-sized, not mega-commits.
- Error model: wrap with `xcerr.E(code, userSafeMessage, cause)`. Messages are
  user-facing (no internal paths); causes carry technical detail.
- Config precedence: defaults < `<workspace>/config.json` < env (`XCUT_*`) <
  CLI flags. New knobs need defaults, validation in `config.Resolve`, and a
  test.
- Tests: fixtures are generated with FFmpeg lavfi (`internal/testmedia`) —
  never commit binaries; integration tests skip when ffmpeg is absent.
- Migrations: append-only in `internal/storage/db.go`. Never edit an applied
  migration.
- Docs that must stay truthful: `docs/PROJECT_STATE.md` (update after every
  milestone), `docs/PERFORMANCE.md` (measured numbers only — "Not measured"
  beats invented), `docs/NIGHTLY_LOG.md` (facts per milestone),
  `docs/ACCEPTANCE.md` (checkboxes with evidence), `CHANGELOG.md`.

## Boundaries quick map

```
cmd/xcut            CLI entry (thin)
internal/cli        command wiring + presentation only
internal/pipeline   import/analyze/timeline/render — shared by CLI and API
internal/api        localhost HTTP API + embedded web UI (static/, no deps)
internal/{analysis,event,style,timeline,render,media,job,storage,
          workspace,config,worker,xcerr}   core packages, see ARCHITECTURE.md
crates/xcut-worker-media   Rust worker (optional; describe + audio_rms)
```
