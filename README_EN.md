<div align="center">

# 🎬 XCut

**Local-first automatic video editing / 本地优先的自动视频剪辑**

Raw footage in, highlight cut out — one deterministic pipeline:
**probe → analyze → events → style → timeline → render**
No cloud · no telemetry · no AI required

[![Release](https://img.shields.io/github/v/release/xiabee/XCut?include_prereleases&label=release&color=blue)](https://github.com/xiabee/XCut/releases)
[![License](https://img.shields.io/badge/license-MIT-green)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25%2B-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![Platform](https://img.shields.io/badge/Windows%20%7C%20Linux-amd64%20%7C%20arm64-lightgrey)](https://github.com/xiabee/XCut/releases)
[![Status](https://img.shields.io/badge/status-Alpha-orange)](docs/ROADMAP.md)

English | [中文](README.md)

</div>

---

## ✨ Highlights

| | |
|---|---|
| 🖱️ **Double-click friendly** | A native desktop window (WebView2) over the embedded web UI — no Node, no build step, no extra files |
| 🎞️ **Drag-and-drop import** | Drop video files onto the page, never overwriting an existing copy; local-path import stays too |
| ✂️ **A real visual timeline** | Clip blocks sized by duration with thumbnails, drag reorder, edge-handle trimming, transition badges (cut / fade / xfade), a full inspector with keyboard shortcuts |
| 🏸 **A style engine that knows the court** | Badminton rally mode: hit-driven scoring, court-ROI motion analysis, diversity dedup — every clip carries its "why" |
| 🎤 **Subtitles & karaoke** | Speech-to-text via an AI sidecar → plain SRT or word-swept karaoke ASS, burned into the render with one checkbox |
| 🔒 **Local-first** | Loopback-only, no telemetry; AI is an optional sidecar enhancer, never the foundation |
| 📦 **Bounded by design** | Jobs, processes, caches, temp files and logs all have configured ceilings; idle footprint ~15 MB RAM, ~0% CPU |

Target scenarios: **badminton · KTV · vlogs · stage performance · sports highlights**

## 🚀 Quick start

> Prerequisites: **FFmpeg + ffprobe** (on PATH, via `XCUT_FFMPEG` / `XCUT_FFPROBE`, or placed beside the executable).

### Option 1: download a prebuilt build (recommended)

Grab from [**Releases**](https://github.com/xiabee/XCut/releases):

- **Windows installer** `xcut-*-windows-setup.exe` (~6 MB) — double-click
  to install, with Start-menu/desktop shortcuts and a real uninstaller.
  For unattended installs pick the scope explicitly:
  `xcut-...-setup.exe /CURRENTUSER /VERYSILENT /DIR=D:\XCut` (without a scope
  the installer asks "just me / all users" first, which blocks a script);
- **portable Windows zip** (includes a QUICKSTART.txt) — unzip,
  double-click `xcut.exe`;
- static Linux binaries for amd64/arm64. Install FFmpeg yourself on Linux
  (the one-click installer covers Windows only). **Kylin V10 SP1 note:** its
  packaged FFmpeg writes Hisilicon OMX decoder-plugin logs to stdout, which
  corrupts ffprobe's JSON and makes import fail — point `XCUT_FFMPEG` /
  `XCUT_FFPROBE` at a stock build and the full chain works (verified on real
  hardware: import → analyze → timeline → render).

First launch detects your environment: if FFmpeg is missing, a bar at
the top offers a **one-click install** (official build, checksum-verified,
landed next to the exe — never bundled, never silent). AI features such
as subtitles expect a local backend you configure yourself (set
`workers.ai_bin` or put `xcut-ai` on PATH) — the app provides the
interface and never downloads models on its own.

### Option 2: build from source

Go 1.25+ to build:

```sh
git clone https://github.com/xiabee/XCut.git && cd XCut
go build -o xcut ./cmd/xcut          # produces xcut.exe on Windows

# check your environment
./xcut doctor
```

### One-shot cut

```sh
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

## 🖥️ Serve (local web UI + HTTP API)

```sh
./xcut client     # native desktop window (WebView2) over the embedded UI
./xcut serve      # same UI in your browser at http://127.0.0.1:8619
```

Create a project, drag video files onto the page (or use the file picker)
or import a local path, and run analyze → timeline → render with live job
progress — the rendered MP4 plays right in the page.
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

<details>
<summary><b>HTTP API reference</b> (<code>/api/v1</code>, loopback-only)</summary>

```sh
curl http://127.0.0.1:8619/api/v1/health
curl http://127.0.0.1:8619/api/v1/projects
curl -X POST http://127.0.0.1:8619/api/v1/projects -d '{"name":"new-project"}'
curl -X POST http://127.0.0.1:8619/api/v1/projects/<id>/assets -d '{"path":"D:/videos/clip.mp4"}'
curl -X POST "http://127.0.0.1:8619/api/v1/projects/<id>/assets/upload?filename=clip.mp4" --data-binary @clip.mp4  # content upload (where drag-drop lands; 8 GiB per file)
curl -X POST http://127.0.0.1:8619/api/v1/projects/<id>/render -d '{}'
curl -X POST http://127.0.0.1:8619/api/v1/jobs/<jobID>/cancel            # cancel a queued/running job (202; 409 when terminal)
curl http://127.0.0.1:8619/api/v1/projects/<id>/assets/<assetID>/file   # clip preview (range-capable)
curl -X POST http://127.0.0.1:8619/api/v1/projects/<id>/subtitles -d '{}'  # speech-to-text via the AI sidecar (202 + job)
curl http://127.0.0.1:8619/api/v1/projects/<id>/subtitles               # which subtitle artifacts exist
curl -X POST http://127.0.0.1:8619/api/v1/projects/<id>/render -d '{"subs": true}'  # burn the subtitles into the render
curl -X PUT  http://127.0.0.1:8619/api/v1/projects/<id>/assets/<assetID>/roi -d '{"x":0.1,"y":0.1,"w":0.5,"h":0.6}'  # per-source court ROI
```

Async job endpoints return `202` with a `job_id`; poll `GET /api/v1/jobs/{id}`.
Only one analyze/timeline/render job may be queued or running per project — a
duplicate trigger returns `409` (imports are never deduplicated). Active jobs
show a Cancel button in the web UI; CLI runs (sync in your own terminal) are
cancelled with Ctrl+C.
Loopback-only by design: `xcut serve` **refuses** any non-loopback address
unless you both opt in (`server.listen_remote`) and configure a bearer token
(`server.auth_token`) — see `docs/SECURITY.md` and `docs/DECISIONS.md` D12.

<details>
<summary><b>Remote access (optional)</b>: a bearer token for non-local peers</summary>

```powershell
# 1. generate a token strong enough (< 24 chars is refused at startup)
$tok = (openssl rand -hex 24)
# 2. put it in <workspace>/config.json (or set XCUT_AUTH_TOKEN)
#    {"server":{"listen":"0.0.0.0:8619","listen_remote":true,"auth_token":"..."}}
./xcut serve   # the startup line states the posture: loopback only / remote bind, authentication required
curl -H "Authorization: Bearer $tok" http://<host-ip>:8619/api/v1/health
```

- Loopback peers are always trusted: the desktop client, the double-clicked exe
  and `xcut client` need no token.
- The token comes from config or env only — **never a CLI flag**, which would
  leak it into process listings and shell history.
- `xcut config show` prints it as `<set>`; the starter file written by
  `xcut init` never contains it.
- Constant-time comparison; failures counted per peer (20 per 5 min, then `429`).
- Trust is decided from the socket address. `Host`/`X-Forwarded-For` never count.
- The transport is plaintext HTTP: keep it on a trusted network or inside a
  Tailscale/SSH tunnel, and **do not** front it with a same-machine TLS reverse
  proxy — that would make every remote peer "loopback" and bypass the gate
  (SECURITY.md explains).
- The web UI works remotely: a browser cannot attach a header to `<video src>`,
  thumbnails or download links, so the first login (proven by the bearer token)
  mints an `HttpOnly; SameSite=Strict` session cookie used for **reads**, while
  **writes** still must echo the session id in `X-Cut-Session` — a cross-site
  page cannot read an HttpOnly cookie to produce that echo, so CSRF is
  structurally impossible rather than token-guarded (D14). The token itself is
  never persisted, so reloading does not ask again.
- Sessions are in-memory: 12 h TTL, capped at 256, all gone on restart;
  `DELETE /api/v1/session` or the topbar "Sign out" revokes one immediately.
- The runbook — the SSH-tunnel recipe that was actually driven end to end (and
  who it hands authentication to), what 401/403/429 each mean, backup scope,
  rotation and revocation: **docs/OPERATIONS.md**.

</details>

</details>

## 🎨 Styles

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

A style's `target_duration` is only the **default**: one run can override it
with `xcut timeline/auto --duration <seconds>` (or the "reel length (s)" field
beside the style picker), and the preset file is never rewritten. That knob is
worth turning where further parameter tuning is not — measured against
annotated rallies from a real match, a 60 s reel can show at most 12.7% of the
473 s of rally time that exists, while a 240 s target reaches 18 of 43 rallies
(recall 0.112 → 0.289) for about 7 points of precision
([docs/EVAL.md](docs/EVAL.md)).

For court-confined motion analysis, the web UI can draw the region of
interest directly on a frame of the project's asset ("draw court
ROI…" in the sidebar); the rect is saved per SOURCE
(`GET/PUT/DELETE /api/v1/projects/{id}/assets/{aid}/roi`) and overrides the
selected style's per-preset region during timeline generation — each fixed
camera can have the court in a different spot, and assets without their own
rect fall back to the style's region
(`GET/PUT/DELETE /api/v1/styles/{name}/roi`).

Every selected clip carries its score, score breakdown and the reason it was
picked in its metadata — the web UI shows the "why" per clip.

The web UI speaks English and Chinese: the selector in the topbar switches
instantly, the choice persists, and first-time visitors get whichever
language their browser prefers (no dependencies — a plain JSON dictionary
keyed by the English strings, drift-checked by a Go test).

## ✂️ Timeline & rendering

The renderer applies the timeline exactly as validated: `cut`, `fade`
(through black) and `xfade` (real crossfade) transitions may be freely mixed
within one timeline; per-clip `speed` is honored for both video and audio.
Unsupported constructs (audio tracks, multi-track timelines, clip effects)
are refused loudly instead of silently dropped. Output is ffprobe-verified
before an atomic publish, and a render is refused if its `--out` would
overwrite any source media.

## 🎤 Subtitles (KTV/guitar sing-along)

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

<details>
<summary><b>Semantic / vision AI (reserved seam)</b></summary>

The reference sidecar also supports two env-configured OpenAI-compatible
HTTP backends — no bundled models, no silent downloads; configure and the
capability lights up:

```sh
# speech-to-text through a remote Whisper server (POST /v1/audio/transcriptions)
export XCUT_SIDECAR_STT_URL="http://127.0.0.1:9000"

# single-frame semantic description (POST /v1/chat/completions, multimodal) —
# the seam for match-phase awareness and content tagging
export XCUT_SIDECAR_VISION_URL="https://your-gateway.example:8443"
export XCUT_SIDECAR_VISION_MODEL="vision"
export XCUT_SIDECAR_INSECURE_TLS=1   # for self-signed certificates
```

Once configured, the `frame_describe` capability lights up in the sidecar's
capabilities (pipeline consumption of it is future work);
`XCUT_SIDECAR_TIMEOUT` (seconds) bounds one HTTP call.

</details>

## ⚙️ Configuration

`./xcut config show` prints the effective config; precedence is
defaults < config file (`<workspace>/config.json`) < environment (`XCUT_*`)
< CLI flags.

<details>
<summary><b>All resource/config knobs</b> (defaults shown)</summary>

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
| `server.listen_remote` | false | allow a non-loopback bind; requires `auth_token` (D12) |
| `server.auth_token` | (empty) | bearer token for non-local peers, min 24 chars (or `XCUT_AUTH_TOKEN`) |
| `workers.audio` | `auto` | `auto`/`ffmpeg`/`rust` audio analyzer |
| `workers.ai_bin` | `xcut-ai-sidecar` | AI sidecar binary (capability-detected) |
| `ffmpeg.bin` / `ffmpeg.ffprobe_bin` | `ffmpeg` / `ffprobe` | toolchain override (or `XCUT_FFMPEG`/`XCUT_FFPROBE`) |

</details>

Nothing runs unbounded: jobs, processes, cache, proxies, temp and logs all
have configured ceilings. `xcut cleanup [--dry-run]` reclaims temp space;
`xcut cache stats|clear` inspects and clears the analysis/proxy caches.

## 🦀 Optional Rust worker

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

## 🛠️ Development

```sh
go build ./... && go vet ./... && go test ./...   # Go side
cargo test                                          # Rust side (crates/)
```

Tests generate all media fixtures on the fly with FFmpeg `lavfi` — no binary
fixtures in the repo. Integration tests skip automatically when FFmpeg is
absent.

Architecture, decisions, security model, performance policy and the nightly
log live in [`docs/`](docs/):

| Doc | Contents |
|---|---|
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | pipeline, boundaries, worker protocol |
| [docs/DECISIONS.md](docs/DECISIONS.md) | ADR log (why Go/Rust/SQLite/JSON…) |
| [docs/SECURITY.md](docs/SECURITY.md) | threat model and controls |
| [docs/PERFORMANCE.md](docs/PERFORMANCE.md) | measured baselines |
| [docs/PROJECT_STATE.md](docs/PROJECT_STATE.md) | what actually works right now |
| [docs/ROADMAP.md](docs/ROADMAP.md) | where this is going |
| [docs/USAGE.md](docs/USAGE.md) | per-command reference |
| [docs/EVAL.md](docs/EVAL.md) | selection-quality evaluation (`xcut eval`) |

## 📦 Packaging

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

## 📄 License

[MIT](LICENSE)
