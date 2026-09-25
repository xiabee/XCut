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

The web UI carries the same measurement as a chip under the timeline strip, read
from the `pacing` object `GET /api/v1/projects/{id}/timeline` computes — one
function behind both readers, so the chip and this line cannot disagree. It
describes the **saved** document: unsaved trims in the strip above it are not in it
yet.

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
usage: xcut auto <file...> [--style name] [--duration seconds] [--beat-snap seconds|off] [--music file] [--subs on|off|auto|file] [--project name] [--out path] [--score-crop x,y,w,h] [--encoder name]

one-shot: import → analyze → timeline → (captions) → render
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

## xcut roi
```
usage: xcut roi <project> [--asset id] [--set x,y,w,h] [--clear]

list, set or clear a per-asset motion ROI (court region)
```

The court region a source's motion signal should look at, as fractions of the frame
(0..1 — not pixels), stored on one asset instead of in the style. It overrides the
style's own region for that source, and it is a different analyzer name, so the
analysis cache keeps the two apart: setting a region makes the next `xcut analyze`
measure the clip again inside it, which is also when a scoreboard region gets its
point boundaries. Bare `xcut roi <project>` lists what each asset carries.

## xcut player
```
usage: xcut player <project> --asset <id> [--set x,y,w,h [--at seconds]]

person filter: mark where you are in a source
```

## xcut render
```
usage: xcut render <project> [--out path] [--subs file|auto] [--encoder name]

render a project timeline to MP4
```

`--subs auto` asks the project what should burn rather than being told: it resolves the project's captions and, when they are laid out for another frame and the stored transcript is still bound to this project's media, re-lays them out first and prints what it did. A named file stays a named file — it burns as it stands, wrong frame included — because the caller pointed at it. `auto` never starts a transcription: that is `xcut auto --subs=on` or the subtitles endpoint, and a render flag that silently spent minutes of Whisper would be a surprise, not a default.

## xcut serve
```
usage: xcut serve [--addr host:port]

run the local HTTP API
```

## xcut subtitles
```
usage: xcut subtitles <media-file> [--ass] [--out path] [--lang code] [--model name]

speech-to-text subtitles via the AI sidecar (SRT or styled ASS)
```

A styled `.ass` declares the frame it was styled for, and libass scales the whole
script by it. For a project the transcript stage reads the reel's own canvas, so a
9:16 cut gets its caption sized and placed for 9:16 (`Fontsize` and the margins scale,
720p stays exactly as it was). This standalone command has no project and so no canvas:
it writes the shipped 1280×720 reference. Restyling an existing project's captions
after changing its canvas means re-running the transcription — the transcript itself is
not kept, only the files rendered from it.

`--ass` asks for the styled file, and only the karaoke fill needs to know where each
syllable fell. A sidecar that reports just the lines (most of them) gets the same frame,
the same wrap and the same dwell without the sweep; `{\kf` tags appear only when words
were actually timed.

Within that file, a caption line is as wide as the frame allows — `(width − 2·margin)`
divided by the font size, separators included — two lines appear at a time, and anything
longer becomes successive cues. A cue is shown for at least 1.2 s where the silence
after it allows (never stealing the next cue's time), and the karaoke fill still follows
the words, not the hold.

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
every end is already pinned; it is there for material that has one, and for a music
bed (`--music` above), whose grid outranks the source's (docs/EVAL.md, Cutting on
the beat).

`camera_motion` is the preset's own knob for 运镜: `{"mode":
"punch_in"|"drift"|"roi", "zoom": 0.8}` frames every clip in the window it
describes, which the renderer crops to and magnifies into the canvas (`drift`
alternates the pan direction per clip; `roi` centers the window on the project's
analysis region). It is set in a style file rather than per run — a workspace copy
under `<workspace>/styles/` is what the style editor writes. One preset asks for it
today: `sports_vertical`, at `roi`, because a 9:16 canvas over a 16:9 source has to
choose a horizontal window and the analyzed region is the one place the project
already says where the action is. A project analyzed full-frame gets **no plan at
all** from that preset (asserted): the alternative is a guessed centre crop over
broadcast footage, which can cut the scoreboard out of the shot. The measured cost
is 8.6 s against 8.1 s per minute of output, at +11.5% file size
(docs/PERFORMANCE.md).

`clip_order` is how the shots a style chose are arranged: absent or
`"chronological"` plays them in the order they happened, `"hook_first"` leads with
the one the style's own scoring rated best and leaves the rest in match order. Only
`beat_shortform` asks for a hook; an unrecognised name is refused when the preset
loads rather than read as the default, and the pacing line above is what shows
whether the promise was kept (`top shot starts at 0.0s`).

In the web UI those knobs are controls rather than flags: a **music bed** path field
and a **cut on the beat** selector (`style's own` sends nothing, `±0.12 s` sends the
product default, `off` sends `-1`). Under the timeline, the document reports what it
actually chose — `music bed "<name>" at <bpm> BPM · 1 of 8 cuts landed on its beat`,
or the sentence for a bed whose audio held no grid, or for one whose cut points were
already pinned. The name shown is the file's, not the caller's path.

Asking for more than the material holds is safe and does not hang: the selection
is bounded by the usable segments, not by the number. Measured on the same
match — a 240 s target and a 14400 s (4 h) target both yield 21 clips / 168 s,
because that is everything the detector found that fits the style's rules.

