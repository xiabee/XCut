# XCut Client — Design

> The desktop editing client for XCut. Status: designed & in development
> (2026-09-13). This document is the reference for what the client is,
> why it looks the way it does, and which constraints shaped it.

## 1. What "client" means here

XCut already has a client surface: the embedded web UI served by
`xcut serve` on loopback. It is functional but browser-hosted and its
editing surface is a table of clips with buttons — serviceable, not an
editor.

The client work adds two things on top of the existing architecture:

1. **A native desktop shell** — `xcut client` opens a real OS window
   (WebView2) hosting the same embedded UI. One binary, no installer,
   no new server surface.
2. **A modern editing workspace** — a redesigned UI whose centerpiece is
   a visual timeline, so cutting actually feels like cutting: clips as
   blocks sized by duration, transitions as editable join badges, a
   preview that follows the playhead, and an inspector for the selected
   clip.

## 2. Technology choice

| Option | Verdict | Why |
|---|---|---|
| **Native WebView2 shell (chosen)** | ✅ | `xcut client` = the existing serve pipeline + a WebView2 window (github.com/jchv/go-webview2, MIT, pure Go syscall COM binding). ~200 LOC of shell; zero new API surface; Go-first; the web UI keeps working in a plain browser unchanged. |
| Wails | ❌ for now | Full app framework: its own asset/binding pipeline would duplicate the serve/API wiring, and the CLI/daemon story (headless jobs, `xcut auto`) stays primary. Revisit only if the shell outgrows itself. |
| Tauri | ❌ | Rust frontend shell conflicts with "Rust is an optional worker"; drags in a Node toolchain. |

Constraints honored:

- **Go is the core** — the shell is a Go command; the UI is the same
  `go:embed` asset tree the server already ships.
- **No bundling of unauthorized binaries** — the WebView2 runtime is an
  OS component (preinstalled on Windows 11; evergreen redist on 10).
  The go-webview2 module embeds Microsoft's WebView2Loader.dll
  (~500 KB) — this is Microsoft's **authorized-for-redistribution** SDK
  loader component, distributed inside the MIT-licensed Go module; it is
  flagged here explicitly for maintainer awareness. If the loader
  dependency is ever objected to, a loader-less syscall path (binding
  CreateCoreWebView2Environment directly) is the documented alternative.
- **Loopback-only stands** — the shell talks to the same in-process
  server bound to 127.0.0.1; nothing remote is exposed.
- **Cross-compile stays green** — the shell is Windows-only behind
  build tags; other platforms get `xcut serve` + browser (the embedded
  UI is identical).

## 3. Architecture

```
xcut client                     xcut serve (unchanged)
┌────────────────────┐          ┌──────────────────────┐
│ Win32 window       │          │ loopback HTTP server │
│  └ WebView2        │ loads    │  ├ /api/v1/*         │
│     (Chromium) ────┼─────────▶│  ├ / (embedded UI)   │
│  user closing the  │ HTTP     │  └ projects/jobs/... │
│  window exits the  │          └──────────────────────┘
│  process (single   │
│  instance)         │          Same binary: `xcut client`
└────────────────────┘          = serve + shell, in-process.
```

- `xcut client [--addr 127.0.0.1:port]` starts the server **in-process**
  (the serve pipeline, workspace lock included), picks a free port when
  unspecified, then opens the shell window at that address.
- Closing the window terminates the process (jobs are reconciled by the
  existing crash-recovery machinery — same contract as killing serve).
- `--browser` flag opens the system browser instead (and is the
  behavior on non-Windows builds).
- `xcut doctor` reports WebView2 availability as an optional component.

## 4. Editing workspace (information architecture)

```
┌────────────────────────────────────────────────────────────────┐
│ ✂ xcut   [project ▾]                            v · online     │ top bar
├───────────┬───────────────────────────────────┬────────────────┤
│ MEDIA     │  PREVIEW                          │ INSPECTOR      │
│  + import │   ┌───────────────────────┐       │  selected clip │
│  asset    │   │  video (16:9 canvas)  │       │   trim in/out  │
│  cards    │   └───────────────────────┘       │   speed/volume │
│  (thumb,  │   ▶ ⏸ · scrubber · time           │   transition   │
│  dur,     │───────────────────────────────────│  (type+length) │
│  codec)   │  TIMELINE                         │  remove        │
│ SUBTITLES │   ruler ────────────────────────  │  (Delete key)  │
│  (picker, │   [■ c1][→ xfade 1s][■ c2][■ c3]  │  or project    │
│  preview, │   blocks ∝ duration · DnD reorder │  summary when  │
│  download)│   save · reset · restore · status │  nothing       │
│ ROI       │                                   │  selected      │
│ JOBS      │                                   │                │
└───────────┴───────────────────────────────────┴────────────────┘
```

- **Media pool** (left): import by path, asset cards with a real
  thumbnail (client-side `<video>` seek + canvas capture — no backend
  thumbnails needed), duration, codec.
- **Preview** (center top): the existing range-capable render/player,
  now with a transport bar and a scrubber; the timeline playhead and
  the preview are linked.
- **Timeline** (center bottom): the editor. Clips render as blocks
  whose width is proportional to timeline duration; joins show a
  transition badge (cut / fade / xfade + seconds) that is editable;
  blocks drag to reorder (HTML5 DnD, same save path as today);
  clicking selects for the inspector; the ruler seeks the preview.
  Save/Reset/Restore and the revision guard keep their exact semantics.
- **Inspector** (right): context-sensitive. Clip selected → trim
  in/out, speed, volume, transition, remove (also the Delete key).
  Nothing selected → project summary (counts, style, render state).
- **Jobs, subtitles, ROI** stay in the left rail as collapsible
  sections — same endpoints, same behaviors.

All existing element IDs, endpoints, and flows are preserved: the
redesign is a re-skin plus new components, not a rewrite of the API
contract. Text stays `textContent`-only (XSS rule).

## 5. Visual design

- Dark editor theme (video editors live dark), refined: a single
  accent (green → same family as today's), elevated card surfaces with
  1px hairline borders, 10px radii, a 4/8px spacing grid, 13px base
  type with a clear scale, subtle 120ms hover/focus transitions, and
  visible focus rings.
- Inline SVG icons (no icon font, no external assets).
- Motion is functional only: hover, selection, drag affordances — no
  decorative animation.

## 6. Milestones

- [x] **C1 — native shell**: `xcut client` (WebView2, build-tagged,
      doctor detection, `--browser` fallback), roadmap/design docs.
- [ ] **C2 — workspace redesign**: three-pane layout, design tokens,
      media pool cards, preview transport, jobs/subtitles/ROI rehoused.
- [ ] **C3 — visual timeline**: strip editor (blocks, joins, DnD,
      selection), replacing the clip table.
- [ ] **C4 — inspector & polish**: clip property editing, transition
      badge editing, keyboard shortcuts, playhead↔preview linking,
      empty states, README/CHANGELOG.

## 7. Non-goals (for now)

- Multi-track or audio-track editing (the renderer refuses them loudly
  today; the UI must not promise them).
- Real-time proxy scrubbing of source media (preview plays the render
  or raw source via range requests — good enough at local latency).
- Native menus/tray/auto-update — candidates for the packaging phase.
- macOS/Linux shells — the web UI is identical there via `xcut serve`.
