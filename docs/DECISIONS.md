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
