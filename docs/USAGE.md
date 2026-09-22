# XCut Command Reference

_Generated from `xcut <command> --help` output; refreshed for v0.1.4-alpha._

Global flags: `--config`, `--workspace`, `-v`, `-q`, `--help`.

**No subcommand (Windows)**: double-clicking the exe opens the desktop
client (equivalent to `xcut client`); if another instance is already
running, its UI is opened in the browser instead. FFmpeg/ffprobe placed
next to the executable are picked up automatically (before PATH).

`--music` lays a track under the reel and cuts to **it**: the file's own beat grid
is estimated the same way (same analyzer, same cache), its pulses outrank whatever
the location audio carries, and the render loops the track and mixes it under the
clips' audio at `audio.music_gain` / `audio.source_gain` (defaults 0.9 / 0.35,
both in (0,1]). Naming a bed implies cutting to it at the product's default
±0.12 s, so `--beat-snap off` is how you say "music, but leave my cut points
alone". The timeline document then records what it chose (`music`, `music_gain`,
`source_gain`, `music_bpm`), so `xcut render` mixes without being told twice — and
refuses to render silently *without* the track if the file has since moved.
Measured cost of the mix on a 60 s reel: 11.3 s against 10.2 s of render wall,
+3.3% bytes, duration unchanged to the millisecond (docs/PERFORMANCE.md).

A built cut is also reported as a *shape*, not only as a list of picks:
`xcut timeline` and `xcut auto` print
`pacing: N shots, mean …s, median …s, longest …s, top shot starts at …s`.
Selection scores cannot tell one 15 s stretch from five 3 s cuts; short-form
practice can (a visual change every 3–5 s, 2–3 s at high tempo, the viewer
deciding in the first seconds), and every number on this line is measured off the
built document — mean and median over the played shot lengths, and where the
reel's highest-scored shot *begins in the output* (not where it was filmed). On
the owner's match the shipped `badminton_highlight` reports
`14 shots, mean 8.0s, median 8.0s, longest 8.0s, top shot starts at 24.0s`: each
shot sits exactly on the preset's `max_clip_duration`, so that ceiling — not the
scoring — is what sets the pace, and the best moment arrives a quarter of the reel
in. A hand-edited document has no scores in it, and then the line reports lengths
and says nothing about a top shot rather than inventing one.

## xcut analyze
```
usage: xcut analyze <project> [assetID...]

run baseline analyzers over project assets
```

## xcut boundaries
```
usage: xcut boundaries <project> [--asset id] [--crop x,y,w,h] [--clear]

read point ends off a burned-in scoreboard, so clips can stop there
```

## xcut client
```
usage: xcut client [--addr host:port] [--browser]

open the desktop editing client (native window over the local server)
```

## xcut auto
```
usage: xcut auto <file...> [--style name] [--duration seconds] [--beat-snap seconds|off] [--music file] [--project name] [--out path] [--score-crop x,y,w,h]

one-shot: import → analyze → timeline → render
```

## xcut version
```
usage: xcut version

print build information
```

## xcut config
```
usage: xcut config show|path

show or validate configuration
```

## xcut cache
```
usage: xcut cache stats [--json] | clear [--dry-run]

inspect or clear the analysis and proxy caches
```

## xcut doctor
```
usage: xcut doctor

check environment (ffmpeg, workspace, disk, db, optional workers)
```

## xcut init
```
usage: xcut init

create workspace layout and default config
```

## xcut cleanup
```
usage: xcut cleanup [--dry-run]

remove temp files and evict analysis cache to budget
```

## xcut eval
```
usage: xcut eval <manifest.json> [--check] [--style name] [--duration seconds] [--beat-snap seconds|off] [--out results.json] [--iou 0.3] [--baseline results.json]

score pipeline selection quality against an annotated manifest
```

## xcut import
```
usage: xcut import <project> <file...>

probe media files into a project
```

## xcut project
```
usage: xcut project create|list|show|delete <name>

manage projects
```

## xcut jobs
```
usage: xcut jobs <project>

list jobs of a project
```

## xcut render
```
usage: xcut render <project> [--out path] [--subs file]

render a project timeline to MP4
```

## xcut serve
```
usage: xcut serve [--addr host:port]

run the local HTTP API
```

## xcut subtitles
```
usage: xcut subtitles <media-file> [--ass] [--out path] [--lang code] [--model name]

speech-to-text subtitles via the AI sidecar (SRT or karaoke ASS)
```

## xcut timeline
```
usage: xcut timeline <project> [--style name] [--duration seconds] [--beat-snap seconds|off] [--music file] | xcut timeline <project> --restore-backup

generate a timeline for a project
```

`--duration` overrides the style's `target_duration` for this run only; the
preset file on disk is untouched, so an experiment never mutates a shipped
style. The web UI has the same knob as the "reel length (s)" field beside the
style picker (empty = the style's own target). Accepted range: 1–14400 seconds.
Worth knowing why length matters more than tuning: on a real 43-rally match a
60 s ask reaches 0.112 of the 0.127 ceiling its budget imposes, and the shipped
120 s default reaches 0.243 of 0.254 (docs/EVAL.md) — wanting *more of the match*
in the reel is a duration decision, not a parameter one.

`--beat-snap` is 卡点: how far a clip end may move to reach the beat of the
source's own audio (0.12 = ±120 ms; `off` forces the style's rule off for this
run; absent = whatever the preset says). It is bounded to (0, 0.5] seconds, and it
never overrides a measured point end. Expect it to do nothing on sports footage —
the hall's audio carries no pulse a grid can be believed in, and on a scored reel
every end is already pinned; it is there for material that has one, and for the
music bed coming in `docs/ROADMAP.md` Phase 5 B4 (docs/EVAL.md, Cutting on the
beat).

`camera_motion` is the preset's own knob for 运镜: `{"mode":
"punch_in"|"drift"|"roi", "zoom": 0.8}` frames every clip in the window it
describes, which the renderer crops to and magnifies into the canvas (`drift`
alternates the pan direction per clip; `roi` centers the window on the project's
analysis region). No shipped style asks for it, and it is set in a style file
rather than per run — a workspace copy under `<workspace>/styles/` is what the
style editor writes. The measured cost is 8.6 s against 8.1 s per minute of
output, at +11.5% file size (docs/PERFORMANCE.md).

Asking for more than the material holds is safe and does not hang: the selection
is bounded by the usable segments, not by the number. Measured on the same
match — a 240 s target and a 14400 s (4 h) target both yield 21 clips / 168 s,
because that is everything the detector found that fits the style's rules.

