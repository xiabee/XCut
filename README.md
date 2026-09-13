# XCut

**Local-first automatic video editing.** XCut turns raw footage into highlight
cuts with a deterministic pipeline: probe → analyze → events → style →
timeline → render. No cloud, no telemetry, no AI required.

- Core: **Go** (single static binary)
- Optional hot-path worker: **Rust** (`xcut-worker-media`)
- Media tooling: **FFmpeg / ffprobe**
- Storage: **SQLite** (pure-Go driver, no CGO)
- Status: **Alpha** — pipeline, localhost web UI and HTTP API work end-to-end
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
#   regenerating overwrites manual edits — the previous document is kept
#   as timeline.backup.json; `--restore-backup` swaps it back
./xcut render badminton-2026 --out cut.mp4
./xcut jobs badminton-2026                    # job history (crash-safe)
```

## Configuration

`./xcut config show` prints the effective config; precedence is
defaults < config file (`<workspace>/config.json`) < environment (`XCUT_*`)
< CLI flags.

Knobs (the complete resource/config surface; defaults shown):

| Key | Default | Meaning |
|---|---|---|
| `workspace` | `~/.xcut` | data directory (DB, cache, temp, projects) |
| `resource.max_concurrent_jobs` | 2 | hard cap on parallel jobs |
| `resource.max_ffmpeg_processes` | 2 | hard cap on parallel ffmpeg/ffprobe |
| `resource.max_render_workers` | 1 | independent cap on concurrent render jobs |
| `resource.max_analysis_workers` | 2 | per-analysis ffmpeg call concurrency |
| `resource.ffmpeg_threads` | 2 | per-process `-threads` |
| `resource.frame_sample_fps` | 2 | analysis sampling rate |
| `resource.analysis_width` | 640 | analysis downscale width |
| `resource.proxy_enabled` | `false` | generate low-res analysis proxies (opt-in) |
| `resource.max_proxy_gb` | 2 | proxy disk budget (LRU-evicted) |
| `resource.proxy_threads` | inherit | one-shot proxy encode threads (decode-bound; higher cuts cold-start) |
| `resource.analyzer_call_timeout` | `30m` | per-analyzer ffmpeg budget (hang protection) |
| `resource.max_temp_gb` / `max_cache_gb` | 20 / 10 | disk budgets |
| `jobs.max_history` | 500 | terminal job rows kept (pruned as jobs finish) |
| `job.stale_running_after` | `2h` | age-gate for CLI startup orphan reconciliation |
| `log.level` / `log.max_size_mb` / `log.max_files` | info / 50 / 3 | serve log file rotation |
| `server.listen` | `127.0.0.1:8619` | loopback-forced unless `listen_remote` |
| `workers.audio` | `auto` | `auto`/`ffmpeg`/`rust` audio analyzer |
| `workers.ai_bin` | `xcut-ai-sidecar` | AI sidecar binary (capability-detected) |
| `ffmpeg.bin` / `ffmpeg.ffprobe_bin` | `ffmpeg` / `ffprobe` | toolchain override (or `XCUT_FFMPEG`/`XCUT_FFPROBE`) |

Nothing runs unbounded: jobs, processes, cache, proxies, temp and logs all
have configured ceilings. `xcut cleanup [--dry-run]` reclaims temp space;
`xcut cache stats|clear` inspects and clears the analysis/proxy caches.

## Styles

Styles are data, not code — validated JSON presets in
`internal/style/presets/` (embedded) overridable from `<workspace>/styles/`:

- `generic_highlight` — balanced motion/audio scoring
- `generic_xfade` — like generic_highlight, with real crossfade (xfade) joins
- `ktv_mv` — audio-led (singing/energy), onset-density weighted, longer clips
  for music scenes
- `badminton_highlight` — motion-heavy rally mode: onset-density clustering
  with hysteresis (real court audio never goes silent — ambience keeps the
  detector firing through every break), hit-driven scoring, court-ROI motion
  analysis

For court-confined motion analysis, the web UI can draw the region of
interest directly on a frame of the project's first asset ("draw court
ROI…" in the sidebar); saving persists a workspace override of the
selected style (`<workspace>/styles/<name>.json`, normalized 0..1 rect
in `motion_roi`). The API surface is
`GET/PUT/DELETE /api/v1/styles/{name}/roi`.

Every selected clip carries its score, score breakdown and the reason it was
picked in its metadata — the web UI shows the "why" per clip.

The web UI speaks English and Chinese: the selector in the topbar switches
instantly, the choice persists, and first-time visitors get whichever
language their browser prefers (no dependencies — a plain JSON dictionary
keyed by the English strings, drift-checked by a Go test).

## Timeline & rendering

The renderer applies the timeline exactly as validated: `cut`, `fade`
(through black) and `xfade` (real crossfade) transitions may be freely mixed
within one timeline; per-clip `speed` is honored for both video and audio.
Unsupported constructs (audio tracks, multi-track timelines, clip effects)
are refused loudly instead of silently dropped. Output is ffprobe-verified
before an atomic publish, and a render is refused if its `--out` would
overwrite any source media.

## Serve (local web UI + HTTP API)

```sh
./xcut serve                # http://127.0.0.1:8619, ctrl+c to stop
```

Two ways in:

```sh
./xcut client     # native desktop window (WebView2) over the embedded UI
./xcut serve      # same UI in your browser at http://127.0.0.1:8619
```

Create a project, import a local video path, and run analyze → timeline →
render with live job progress — the rendered MP4 plays right in the page.
The editing workspace is a real timeline: clips render as blocks sized by
duration with client-captured thumbnails, joins show editable transition
badges (cut / fade / xfade), blocks drag to reorder, edge handles trim,
and the inspector edits trim, speed, volume and the transition of the
selected clip (Delete removes, Space plays, Ctrl+S saves). The per-clip
preview follows the ruler playhead. Projects can also transcribe speech
to subtitles through an AI sidecar and burn them (plain SRT or
karaoke-style word-fill ASS) into the render, and the court ROI is drawn
directly on a frame. The UI is vanilla HTML/JS embedded in the binary
(`go:embed`): no Node, no build step, no extra files. Design:
docs/CLIENT_DESIGN.md.

![xcut web UI: a project with imported asset, four succeeded jobs, and the
rendered highlight playing in the result panel](docs/img/web-ui.png)

HTTP API (`/api/v1`, loopback-only):

```sh
curl http://127.0.0.1:8619/api/v1/health
curl http://127.0.0.1:8619/api/v1/projects
curl -X POST http://127.0.0.1:8619/api/v1/projects -d '{"name":"new-project"}'
curl -X POST http://127.0.0.1:8619/api/v1/projects/<id>/assets -d '{"path":"D:/videos/clip.mp4"}'
curl -X POST http://127.0.0.1:8619/api/v1/projects/<id>/render -d '{}'
curl -X POST http://127.0.0.1:8619/api/v1/jobs/<jobID>/cancel            # cancel a queued/running job (202; 409 when terminal)
curl http://127.0.0.1:8619/api/v1/projects/<id>/assets/<assetID>/file   # clip preview (range-capable)
curl -X POST http://127.0.0.1:8619/api/v1/projects/<id>/subtitles -d '{}'  # speech-to-text via the AI sidecar (202 + job)
curl http://127.0.0.1:8619/api/v1/projects/<id>/subtitles               # which subtitle artifacts exist
curl -X POST http://127.0.0.1:8619/api/v1/projects/<id>/render -d '{"subs": true}'  # burn the subtitles into the render
```

## Subtitles (KTV/guitar sing-along)

Speech-to-text is an AI capability, so it follows the sidecar rule: the core
never runs or downloads models. The reference sidecar
(`scripts/xcut-ai-sidecar.py`) probes for a locally installed Whisper backend
— `openai-whisper`, `faster-whisper` (`pip install faster-whisper`) or
whisper.cpp's `whisper-cli` — and honestly reports unavailable until one is
installed; install any of them and the capability turns on with zero XCut
changes. Then:

```sh
./xcut subtitles song.mp4 --ass        # transcript with word timings → karaoke ASS (SRT by default)
./xcut render proj --subs lyrics.ass   # burn subtitles into the render (audio stream-copied)
```

In the web UI the same loop is a button: Transcribe (pick the asset) →
status + download links → check "burn subtitles" → Render. Word timings
drive the karaoke fill (each word sweeps as it is sung; sidecar text is
escaped, so stray braces or newlines cannot corrupt the ASS events);
without them only plain SRT is produced. A "preview transcript" toggle
shows the cue text inline once an SRT exists.

Async job endpoints return `202` with a `job_id`; poll `GET /api/v1/jobs/{id}`.
Only one analyze/timeline/render job may be queued or running per project — a
duplicate trigger returns `409` (imports are never deduplicated). Active jobs
show a Cancel button in the web UI; CLI runs (sync in your own terminal) are
cancelled with Ctrl+C.
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
- [docs/USAGE.md](docs/USAGE.md) — per-command reference
- [docs/EVAL.md](docs/EVAL.md) — selection-quality evaluation (`xcut eval`)

## Packaging

```sh
scripts/build-release.ps1   # Windows (PowerShell 5.1+)
scripts/build-release.sh    # Linux/macOS (bash)
```

produces versioned binaries under `dist/`:

| Artifact | Platforms |
|---|---|
| `xcut` (core + embedded web UI) | windows/amd64, linux/amd64, linux/arm64 |
| `xcut-worker-media` (optional) | windows/amd64, linux/amd64 (static musl) |

The Go binaries are fully static (no CGO) — drop-in executables. The Linux
Rust worker is built with the bundled `rust-lld` against the musl target, so
no platform toolchain is needed for the build.

## License

[MIT](LICENSE)
