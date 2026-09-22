# XCut Command Reference

_Generated from `xcut <command> --help` output; refreshed for v0.1.4-alpha._

Global flags: `--config`, `--workspace`, `-v`, `-q`, `--help`.

**No subcommand (Windows)**: double-clicking the exe opens the desktop
client (equivalent to `xcut client`); if another instance is already
running, its UI is opened in the browser instead. FFmpeg/ffprobe placed
next to the executable are picked up automatically (before PATH).

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
usage: xcut auto <file...> [--style name] [--duration seconds] [--beat-snap seconds|off] [--project name] [--out path] [--score-crop x,y,w,h]

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
usage: xcut timeline <project> [--style name] [--duration seconds] [--beat-snap seconds|off] | xcut timeline <project> --restore-backup

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

Asking for more than the material holds is safe and does not hang: the selection
is bounded by the usable segments, not by the number. Measured on the same
match — a 240 s target and a 14400 s (4 h) target both yield 21 clips / 168 s,
because that is everything the detector found that fits the style's rules.

