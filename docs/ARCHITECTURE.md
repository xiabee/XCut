# XCut Architecture

XCut is a **local-first, privacy-first automatic video editing platform**.
Target scenarios: badminton, KTV, vlog, stage performance, sports highlights,
party videos.

## Core Pipeline (Deterministic First)

```
Video → Probe → Feature → Event → Score → Style → Timeline → Render → MP4
```

AI is an **enhancer**, never the foundation. The system must work end-to-end
with only FFmpeg + ffprobe installed. AI workers (Python sidecars) are optional
plugins that improve Feature/Event/Score quality later.

## Language Boundaries

| Layer | Language | Responsibility |
|---|---|---|
| Core | **Go** | CLI, HTTP API (later), job scheduler, project/storage, timeline engine, style engine, cache, FFmpeg orchestration, config, logging |
| Hot-path analysis | **Rust** (optional worker) | Frame/audio DSP, SIMD-friendly analysis — introduced only when a benchmark proves the Go path is the bottleneck or for memory-safety-critical parsing |
| AI | **Python sidecar** (optional, later) | Whisper/YOLO/SAM/Demucs etc. Never a core dependency |

### Go ↔ Rust boundary

No FFI in phase 1. Workers are separate processes communicating over
`stdin/stdout` with versioned JSON (or files for bulk data). This isolates
crashes, avoids ABI complexity, and keeps workers independently benchmarkable.
FFI/shared-memory is considered only if profiling proves IPC is the bottleneck.

### Worker protocol sketch

```json
// request on stdin
{"protocol": 1, "op": "analyze_audio_rms", "params": {...}, "input": "path"}
// response on stdout
{"protocol": 1, "ok": true, "result": {...}}
```

Workers must be optional: missing binary = capability unavailable, never a
core failure.

## Repository Layout

Only directories with real code exist:

```
cmd/xcut                  CLI entry (thin)
internal/cli              command wiring + presentation
internal/pipeline         core operations shared by CLI and HTTP API
internal/api              localhost HTTP API + embedded web UI (static/, vanilla JS)
internal/analysis         analyzers + result cache (FFmpeg or Rust worker backends)
internal/event            deterministic activity segmentation
internal/style            style presets + selection (embedded + workspace overrides)
internal/timeline         versioned timeline IR + validation
internal/render           timeline → MP4 (normalize → concat → verify → publish)
internal/media            ffprobe/ffmpeg exec, probe, fingerprint, process limiter
internal/job              DB-backed queue (sync + async), orphan reconciliation
internal/storage          SQLite (no CGO), migrations, typed stores
internal/workspace        data dir layout, SafeJoin, temp lifecycle
internal/config           defaults < file < env < flags, resource budgets
internal/worker           worker-process client (JSON over stdin/stdout)
internal/xcerr            typed error model
internal/version          build identity
internal/testmedia        lavfi fixture generator (tests only)
crates/xcut-worker-media  Rust worker (describe + audio_rms)
docs/, scripts/, .github/
```

## Key Design Decisions

See `docs/DECISIONS.md` for the full list (SQLite, no CGO, JSON configs,
process-per-worker, localhost-only, etc.).

## Data Flow & Storage

- **SQLite** (`<workspace>/xcut.db`): projects, assets, jobs — durable state.
- **Files** (`<workspace>/`): analysis artifacts and timelines are JSON files
  (cacheable, regenerable), renders are MP4s. DB references files by path.
- **Cache key**: `SHA256(file fingerprint + analyzer + analyzer version + config hash)`.
  File fingerprint = size + mtime + partial content hash (cheap); full hash is a
  future verification step.

## Process & Resource Model

- One `xcut` process owns the job scheduler; FFmpeg/ffprobe/workers are child
  processes with `exec.CommandContext`, per-process thread caps, and a global
  concurrency semaphore (config `resource.*`). Async jobs carry a per-job
  cancel context: `POST /jobs/{id}/cancel` (web UI button) stops queued jobs
  before they start and kills running jobs' ffmpeg children; cancelled
  renders reclaim their scratch.
- Nothing unbounded: jobs, ffmpeg processes, cache bytes, temp bytes, log size
  all have configured ceilings.
- Startup reconciles orphaned `running` jobs (crash recovery): CLI opens use
  an age gate (`job.stale_running_after`) so a live writer's rows are never
  touched by readers; `serve` holds the workspace writer lock, so it sweeps
  every queued/running row and all `temp/` debris before listening — anything
  it sees at startup belonged to a dead process. Temp dirs have a lifecycle
  (`xcut cleanup`).

## Security Model

See `docs/SECURITY.md`. Headlines: user media is untrusted input; FFmpeg is
invoked argument-by-argument (no shell); all project paths go through a
safe-join guard; the local API binds to 127.0.0.1 unless explicitly overridden.

## Testing Strategy

- Unit: timeline validation, config resolve, safe paths, event math.
- Integration: real ffprobe/ffmpeg against `lavfi`-generated fixtures
  (auto-skipped if ffmpeg absent).
- E2E: `xcut auto` produces a playable MP4 verified by ffprobe.
- Deterministic fixtures: generated by scripts from FFmpeg lavfi sources, never
  committed as binaries.
