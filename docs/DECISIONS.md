# XCut Decisions (ADR log)

One entry per decision that shapes the architecture. Newest at the bottom.
Format: Context → Decision → Consequences.

## D1: Go as the core language

Context: personal local-first tool; author knows Go and Rust; needs fast CLI,
good process orchestration, single static binary.
Decision: Go owns application core (CLI, scheduler, storage, timeline, style,
FFmpeg orchestration, future HTTP API).
Consequences: fast builds, trivial cross-compilation (incl. Windows/ARM64),
rich standard library. CPU-hot algorithms move to Rust only with benchmark
evidence.

## D2: Rust as optional worker processes, no FFI in phase 1

Context: Go GC and safety are fine for control plane; heavy per-frame/per-sample
math may later need Rust/SIMD; FFI (cgo) breaks easy cross-compilation.
Decision: workers are separate processes (`xcut-worker-*`) speaking versioned
JSON on stdin/stdout (files for bulk). Introduced only when profiling proves
need. IPC overhead is accepted until measured as a bottleneck.
Consequences: crash isolation, independent benchmarking, simple cross-platform
builds; slightly more orchestration code; deferred (not avoided) FFI.

Status (2026-09-07): protocol validated end-to-end. `crates/xcut-worker-media`
(Rust, symphonia) implements `describe` + `audio_rms`; Go client
(`internal/worker`) with timeouts + structured errors; `workers.audio` config
(auto: worker-first with FFmpeg fallback, rust: strict, ffmpeg: builtin).
Benchmark: parity with ffmpeg astats on 60s mp3 (0.127s vs 0.143s) — kept as
optionality, no rewrites. Known gap: symphonia cannot decode ffmpeg-encoded
AAC ("predictor data"); auto mode's fallback covers this until fixed upstream.

## D3: No Python in the core; AI as optional sidecar

Context: AI ecosystems (Whisper/YOLO/SAM/Demucs) are Python; core must not
depend on PyTorch or large model downloads.
Decision: base install = XCut binary + FFmpeg and nothing else. AI runs in an
optional sidecar worker with capability detection, timeouts, resource limits,
and protocol versioning. Missing sidecar degrades quality, never availability.
Consequences: deterministic baseline is a first-class product; AI features are
progressive enhancements.

## D4: SQLite via modernc.org/sqlite (no CGO)

Context: local-first single-user storage; must cross-compile Windows/AMD64,
Linux/AMD64+ARM64 without a C toolchain.
Decision: SQLite with the pure-Go `modernc.org/sqlite` driver; WAL mode; busy
timeout; migrations in code. No PostgreSQL/Redis — unacceptable operational
weight for a desktop tool.
Consequences: zero CGO, single-binary deploys, adequate performance for
single-user workloads; driver is slower than mattn/go-sqlite3 (CGO) — accepted,
benchmarks later if it matters.

## D5: Standard library first (no web framework, no Cobra yet)

Context: small CLI surface; net/http is sufficient for a localhost API;
dependencies are audit surface.
Decision: `flag`-based subcommands, `net/http` for the future API, `encoding/json`
everywhere, `log/slog` for logging. Revisit Cobra only when flag parsing hurts.
Consequences: zero-cost dependency audit for now; slight manual wiring.

## D6: JSON (not YAML) for config and style presets in v1

Context: styles need schema validation; stdlib has no YAML parser.
Decision: JSON for config + style presets, validated by explicit validation
code on load. Revisit YAML if user-authored styles demand it.
Consequences: no new dependency; slightly less human-friendly than YAML.

## D7: Timeline as versioned JSON intermediate representation

Context: Analyzer→FFmpeg direct coupling would make styles, previews, and
testing impossible.
Decision: all editing decisions materialize as a validated, serializable,
deterministic Timeline (version field) before any FFmpeg process starts.
Consequences: testable without media files; styles become data; renderer is a
dumb, safe executor.

## D8: Localhost-only by default

Context: local-first privacy tool; network exposure is a risk, not a feature.
Decision: the API server binds 127.0.0.1 unless `listen_remote: true`; remote
listening without authentication is rejected outright in v1.
Consequences: safe default; LAN/multi-device use waits for auth design.
(That design landed as D12 — remote listening is still refused without it.)

## D9: Fast file fingerprint (size+mtime+partial hash), full hash deferred

Context: hashing 50 GB videos per analysis is unacceptable; stale caches must
still be impossible to serve for changed files in the common case.
Decision: cache key = SHA256(size, mtime, head+tail partial content hash,
analyzer, analyzer version, config hash). Full-content hash is an optional
future verify step.
Consequences: cheap cache hits; a same-size-mtime-touched file could in
principle fool the partial hash — documented, acceptable for v1, full-hash mode
planned.

## D10: FFmpeg render strategy v1: normalize → concat

Context: arbitrary codecs/resolutions/fps per source clip.
Decision: per-clip normalize pass to a common canvas (H.264 yuv420p, fixed
fps/size, AAC 48 kHz stereo) with `-threads` capped, then concat demuxer +
fast mux. `<tmp>/<job>/*.partial` output, ffprobe-verified, then atomic rename.
Consequences: robust to heterogeneous inputs, no fancy filter graphs yet;
slightly slower than a single-pass smart filter graph — optimize later with
benchmarks.

## D11: Local-first validation; CI becomes explicit, manual dispatch

Context: the GitHub Actions quota ran out mid-night 2026-09-07 (billing
blocker recorded in PROJECT_STATE). `ci.yml` triggered on every push to main
and every PR, running a 2-OS Go matrix + Rust + packaging job (4 jobs/push,
Windows billed at 2×). Push-triggered CI turns GitHub Actions into a remote
debugger and burns quota on docs-only commits.
Decision (2026-09-07, nightly #2): regular development validates locally via
the local quality gate (`scripts/check.ps1` / `scripts/check.sh`: gofmt, vet,
build, test, race, cross-compile, Rust fmt/clippy/test, optional govulncheck,
FFmpeg integration). `.github/workflows/ci.yml` now triggers only on
`workflow_dispatch` — run it explicitly before a release, after large
cross-platform changes, or when quota recovers. `release.yml` stays
tag-triggered (already explicit). Commits stay frequent; pushes stay batched
(0–2 per night) since they no longer trigger anything.
Consequences: no per-push quota spend; CI returns to its role as independent
cross-platform verification. Acceptance criteria say "Local Quality Gate
Green; CI not run (quota policy)" instead of "CI green". When quota recovers,
re-add push/PR triggers (comment in ci.yml shows how) and consider
`concurrency: cancel-in-progress` plus docs-only `paths-ignore`.

## D12: Bearer-token authentication is what unlocks a remote bind

Context: D8 pinned the API to loopback because there was nothing to
authenticate a remote peer with — the whole workspace (every imported path,
every render, the FFmpeg installer trigger) would have been readable and
writable by anyone on the network. AGENTS.md and SECURITY.md both named auth as
the *prerequisite* for remote listening, not a follow-up.

Decision (2026-09-20, session #14): a static bearer token gates non-local
peers. Loopback peers stay trusted, so the desktop client, `xcut client`, and
the double-clicked exe keep working with zero setup.
- The token comes from `server.auth_token` or `XCUT_AUTH_TOKEN` — never a CLI
  flag, which would publish it into process listings and shell history.
- `config.Resolve` refuses `listen_remote: true` without a token of at least
  `config.MinAuthTokenLen` characters, and `serveAddr` re-checks that at the
  last moment before binding: the invariant must not depend on one call site
  having run.
- Trust is decided from `r.RemoteAddr` only. `Host` and forwarding headers are
  attacker-chosen and must never widen the gate.
- Comparison is constant-time; rejections carry no detail (no hint of how close
  an attempt was) and are logged without the token or the supplied header.
- Failed attempts are rate limited per peer (20 per 5 min) with a bounded
  tracker, because the gate is a new growth axis and AGENTS.md rule 4 has no
  exceptions.
- `/api/v1/*` is gated; the embedded UI shell is not, since it carries no user
  data and a remote operator must be able to load it in order to be asked.

Consequences: `listen_remote` is now a usable option rather than a refusal, and
serve says which posture it started in. What is deliberately *not* solved here:
a browser cannot attach a header to `<video src>`, thumbnails, or download
links, so the web UI over a remote bind needs signed capability URLs — tracked
as the next milestone, not quietly half-done by accepting tokens in query
strings (they land in logs, history, and Referer). Configured tokens are masked
in `xcut config show` and never written into the `xcut init` starter file.

