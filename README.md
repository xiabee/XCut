# XCut

**Local-first automatic video editing.** XCut turns raw footage into highlight
cuts with a deterministic pipeline: probe → analyze → events → style →
timeline → render. No cloud, no telemetry, no AI required.

- Core: **Go** (single static binary)
- Optional hot-path worker: **Rust** (`xcut-worker-media`)
- Media tooling: **FFmpeg / ffprobe**
- Storage: **SQLite** (pure-Go driver, no CGO)
- Status: **Alpha** — core pipeline works end-to-end; UI/API still ahead
  (see [docs/ROADMAP.md](docs/ROADMAP.md))

## Why XCut

Most "AI video editing" tools ship your footage to someone else's server.
XCut is built for personal machines — mini PCs, home servers, gaming desktops —
with hard resource budgets, a localhost-only security default, and a
deterministic baseline that works with nothing but FFmpeg installed. AI is a
future *enhancer* (optional sidecar workers), never the foundation.

Target scenarios: badminton, KTV, vlogs, stage performance, sports highlights.

## Quick Start

Prerequisites: **FFmpeg + ffprobe** on PATH (or set `XCUT_FFMPEG` /
`XCUT_FFPROBE`). Go 1.25+ to build.

```sh
# build (Windows / Linux / macOS)
go build -o xcut ./cmd/xcut          # produces xcut.exe on Windows

# check your environment
./xcut doctor

# one-shot: import → analyze → timeline → render
./xcut auto my-video.mp4 --project first-run --style generic_highlight

# output lands in the project directory of your workspace:
#   ~/.xcut/projects/<project-id>/render.mp4  (or the --out path you passed)
```

Verify the result with any player or `ffprobe`.

### Step by step

```sh
./xcut init                                   # create ~/.xcut workspace
./xcut project create badminton-2026          # new project
./xcut import badminton-2026 match.mp4        # probe + fingerprint assets
./xcut analyze badminton-2026                 # motion/audio features → events
./xcut timeline badminton-2026 --style badminton_highlight
./xcut render badminton-2026 --out cut.mp4
./xcut jobs badminton-2026                    # job history (crash-safe)
```

## Configuration

`./xcut config show` prints the effective config; precedence is
defaults < config file (`<workspace>/config.json`) < environment (`XCUT_*`)
< CLI flags.

Key knobs (see `docs/ARCHITECTURE.md` for the full model):

| Key | Default | Meaning |
|---|---|---|
| `workspace` | `~/.xcut` | data directory (DB, cache, temp, projects) |
| `resource.max_concurrent_jobs` | 2 | hard cap on parallel jobs |
| `resource.max_ffmpeg_processes` | 2 | hard cap on parallel ffmpeg/ffprobe |
| `resource.ffmpeg_threads` | 2 | per-process `-threads` |
| `resource.frame_sample_fps` | 2 | analysis sampling rate |
| `resource.analysis_width` | 640 | analysis downscale width |
| `resource.max_temp_gb` / `max_cache_gb` | 20 / 10 | disk budgets |
| `server.listen` | `127.0.0.1:8619` | loopback-forced unless `listen_remote` |
| `workers.audio` | `auto` | `auto`/`ffmpeg`/`rust` audio analyzer |

Nothing runs unbounded: jobs, processes, cache, temp and logs all have
configured ceilings. `xcut cleanup [--dry-run]` reclaims temp space.

## Styles

Styles are data, not code — validated JSON presets in
`internal/style/presets/` (embedded) overridable from `<workspace>/styles/`:

- `generic_highlight` — balanced motion/audio scoring
- `ktv_mv` — audio-led (singing/energy), longer clips for music scenes
- `badminton_highlight` — motion-heavy, longer rally windows

## Serve (local web UI + HTTP API)

```sh
./xcut serve                # http://127.0.0.1:8619, ctrl+c to stop
```

Then open http://127.0.0.1:8619 in a browser: create a project, import a
local video path, and run analyze → timeline → render with live job progress —
the rendered MP4 plays right in the page. The UI is vanilla HTML/JS embedded
in the binary (`go:embed`): no Node, no build step, no extra files.

HTTP API (`/api/v1`, loopback-only):

```sh
curl http://127.0.0.1:8619/api/v1/health
curl http://127.0.0.1:8619/api/v1/projects
curl -X POST http://127.0.0.1:8619/api/v1/projects -d '{"name":"new-project"}'
curl -X POST http://127.0.0.1:8619/api/v1/projects/<id>/assets -d '{"path":"D:/videos/clip.mp4"}'
curl -X POST http://127.0.0.1:8619/api/v1/projects/<id>/render -d '{}'
```

Async job endpoints return `202` with a `job_id`; poll `GET /api/v1/jobs/{id}`.
Loopback-only by design: `xcut serve` **refuses** non-loopback addresses until
authentication exists (see `docs/SECURITY.md`). Idle footprint is tiny —
measured 12 MB RAM, ~0% CPU (docs/PERFORMANCE.md).

## Optional Rust worker

The Rust worker accelerates audio analysis and validates the process-boundary
protocol used by all future workers (AI sidecars included). It is **never
required**:

```sh
cargo build --release -p xcut-worker-media
# then either put target/release/xcut-worker-media on PATH or set:
#   {"workers": {"media_bin": "/path/to/xcut-worker-media", "audio": "auto"}}
```

`auto` mode falls back to the built-in FFmpeg analyzer whenever the worker is
missing or hits a codec gap.

## Development

```sh
go build ./... && go vet ./... && go test ./...   # Go side
cargo test                                          # Rust side (crates/)
```

Tests generate all media fixtures on the fly with FFmpeg `lavfi` — no binary
fixtures in the repo. Integration tests skip automatically when FFmpeg is
absent.

Architecture, decisions, security model, performance policy and the nightly
log live in [`docs/`](docs/):

- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — pipeline, boundaries, worker protocol
- [docs/DECISIONS.md](docs/DECISIONS.md) — ADR log (why Go/Rust/SQLite/JSON…)
- [docs/SECURITY.md](docs/SECURITY.md) — threat model and controls
- [docs/PERFORMANCE.md](docs/PERFORMANCE.md) — measured baselines
- [docs/PROJECT_STATE.md](docs/PROJECT_STATE.md) — what actually works right now
- [docs/ROADMAP.md](docs/ROADMAP.md) — where this is going

## Packaging

```sh
scripts/build-release.ps1   # Windows (PowerShell)
scripts/build-release.sh    # Linux/macOS (bash)
```

produces versioned binaries under `dist/` (see `--help` inside the scripts).

## License

[MIT](LICENSE)
