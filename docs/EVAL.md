# XCut Evaluation Harness

Algorithm changes must be measured, not eyeballed. The eval harness scores a
style's selection against human annotations so scoring/selection changes are
comparable old-vs-new with hard numbers.

Measured metrics (per case, deterministic):

- **Precision** — selected time inside annotated ranges / total selected time.
  Low precision = the reel contains material nobody highlighted.
- **Recall** — annotated time covered by the selection / total annotated time.
  Low recall = highlights missed.
- **F1** — harmonic mean of the two.
- **Range hits** — an annotated range counts as hit when its best-matching
  clip's temporal IoU ≥ `--iou` (default 0.3).
- **Duplicate rate** — fraction of selected clips that overlap another clip
  with IoU > 0.5. High duplicate rate = a reel of near-identical moments.
- Macro averages across cases are reported at the end (failed cases counted
  separately, never silently dropped).

## Manifest

A manifest describes labeled media. Media paths are relative to the manifest
file. Keep media and manifests in a **gitignored local directory**
(`/eval/` by convention) — no media is ever committed.

```json
{
  "version": 1,
  "cases": [
    {
      "name": "match_day_1",
      "media": "media/badminton1.mp4",
      "style": "badminton_highlight",
      "asset_roi": {"x": 0.05, "y": 0.1, "w": 0.5, "h": 0.6},
      "expected": [
        {"start": 12.0, "end": 24.5, "label": "rally_1"},
        {"start": 41.0, "end": 55.0, "label": "rally_2"}
      ]
    }
  ]
}
```

`asset_roi` (optional) pins a per-source motion region on the case's single
asset before the timeline runs — normalized 0..1, same rule as the preset's
`motion_roi` and the asset endpoint. Use it to A/B the court-ROI override:
run the manifest with different rects and diff `results.json`.

## Run

```sh
xcut eval eval/manifest.json --out eval/results.json
```

Before a full run, `--check` validates the manifest in seconds — media
existence, ffprobe durations vs the annotated ranges (an annotation that
overshoots the media is a typo, caught before any encode), and style
resolution. No workspace is created; nothing is analyzed:

```sh
xcut eval eval/manifest.json --check
```

With ffprobe absent, existence and style checks still run and the duration
check is skipped loudly. `--check` fails (nonzero exit) when any case has a
problem; all problems are reported in one pass.

Style resolution in eval is **embedded presets only**: the run builds each
case's timeline in a throwaway workspace with no style overrides, so
workspace `<workspace>/styles/` presets do not apply inside eval (the UI and
render pipeline honor them; eval deliberately isolates). `--check` enforces
the same rule — a manifest naming a workspace-only style fails the check
exactly as it would fail the run.

```
eval: 1 case(s), hit_iou 0.30 (workspace C:\…\xcut-eval-…)
  match_day_1             P 0.812  R 0.734  F1 0.771  ranges 4/6  dup 0.25  clips 8
macro: P 0.812  R 0.734  F1 0.771  ranges 4/6  dup 0.250
results: eval/results.json
```

- The run happens in an isolated throwaway workspace; user projects and
  caches are untouched. `--style` overrides every case's style (useful for
  A/B: run the manifest twice with different styles and diff results.json).
- Exit code is nonzero when any case fails (e.g. missing media), so eval can
  gate automation.
- `results.json` contains per-case metrics, per-range best IoU/coverage, the
  selected intervals (each carrying the style engine's `score`, `reason` and
  `score_breakdown` — why each moment was picked), and duplicate pairs —
  enough to see *where*, *why*, and *how* the algorithm lost points.

## Worked example: annotating a badminton broadcast

A fixed-camera badminton broadcast with a burned-in scoreboard is ideal
annotation ground truth: every score change marks exactly one finished rally.
Recipe (keep data in the gitignored /eval/):

1. Obtain the recording you have the rights to use, locally.
2. **Derive the score-change times automatically.** Every score change is one
   finished rally, and a burned-in overlay changes *only* when the score does,
   so a scene-difference detector aimed at the scoreboard crop produces the
   annotations. Verified on a 10:03 game that ends 22:19 — 41 real points, and
   this produced 43 candidates (the first is the overlay's fade-in, which the
   recipe below discards):

   ```sh
   # one-time: find the crop that contains only the digits
   ffmpeg -ss 120 -i match.mp4 -frames:v 1 -vf crop=200:90:540:555 probe.png
   # then dump the change times (adjust the crop to your overlay)
   ffmpeg -i match.mp4 -an -vf \
     "crop=180:70:550:560,fps=4,select='gt(scene,0.03)',metadata=print:file=board.txt" -f null -
   # cluster: the overlay cross-fades, so one point spans several frames
   grep -o 'pts_time:[0-9.]*' board.txt | cut -d: -f2 \
     | awk '{t=$1+0; if(prev==""||t-prev>5.0) printf "%.1f ",t; prev=t}'
   ```

   Three things cost time and are worth knowing: the comma inside `gt()` must
   be **quoted, not backslash-escaped** (`select='gt(scene,0.03)'` — recent
   ffmpeg builds no longer accept the escaped form, and `-filter_complex_script`
   is gone); the output path in `metadata=print:file=` must be **relative**,
   because the `:` of a Windows drive letter is itself a filter-argument
   separator; and a low threshold is required precisely *because* the overlay
   cross-fades — at 0.10 the detector found 36 of 41 points, at 0.03 with 5 s
   clustering it found 43 candidates for 41.
   **Validate the count against the final score** before trusting it: read the
   last frame's scoreboard, and the two numbers must sum to the event count.
3. Turn the times into spans: the play between consecutive changes, dropping
   the first (overlay fade-in) and the pause after each point.
4. Baseline → change → compare macro P/R/F1, range hits, dup rate (workflow
   below). The court ROI is worth an A/B: run the manifest with and without
   `asset_roi` (draw the region for your camera once in the web UI and reuse
   the numbers here).
5. **Look at the picks.** P/R/F1 against auto-derived spans measures agreement
   with a heuristic; a montage of the scoreboard crop at +0/+4/+8 s of every
   selected clip shows whether a clip is one whole rally (score constant) or
   straddles a point (score changes mid-clip). That check is what confirmed the
   current placement rule, and it catches things the metrics cannot — e.g. a
   pick that lands in dead time but still overlaps an annotated range.

### What a shared hall does to the signals

The annotated game above is a **multi-court public hall**, not a broadcast: six
other courts move in frame and their shuttle strikes are just as loud as ours.
Two consequences measured on it:

- audio onsets are **not court-specific**, so `hits`/`density` (65 % of the
  badminton preset's score) rank "when the hall was busiest", not "when our
  rally was best";
- anchoring a clip on the median onset or the motion peak therefore **lost** to
  plain segment-start placement (measured: P 0.803 centred → 0.789 median-onset
  → 0.770 motion-peak → **0.886** start-anchored). A rally's loudest smash sits
  right at the point's end, so peaking on it pushes the window across the
  boundary into the dead pause.

The fix for the first problem is semantic (identify the players' own strokes),
which is the AI sidecar's job and still open; the second was a placement bug and
is now fixed.

The PROVISIONAL constants in `internal/event/rally.go` (0.4 adaptive-floor
ratio, P75 baseline, 4x cap, ±6s chunk-snap) are awaiting exactly these
annotations.

## Workflow for algorithm changes

1. Annotate a small set of representative clips (a handful of ranges each is
   enough; consistency beats volume).
2. Record baseline `results.json` at the current HEAD.
3. Make the algorithm change.
4. Re-run the same manifest; compare macro P/R/F1, range hits, duplicate rate.
5. Record the delta in the milestone notes (docs/NIGHTLY_LOG.md).

Synthetic fixtures with construction-known ground truth (as used in the
integration tests) validate harness correctness; real annotated media
measures real quality. Both matter and neither substitutes the other.
