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

### What tuning was tried on this footage, and what it measured

Session #14 swept the rally parameters against the derived manifest. Recording
the null results is the point: they are what tells the next reader not to spend
an evening here.

| change | P | verdict |
|---|---|---|
| committed state (start-anchored + phase cap) | **0.886** | baseline to beat |
| `rally_pad` 0.4 / 0.8 / 1.2 / 2.0 | 0.886 (all) | **inert** — the density walk and chunk snapping absorb it; do not "tune" this expecting an effect |
| `merge_gap` 0.6 / 1.2 / 2.0 | 0.886 (all) | **inert in rally mode** — it is an activity-mode parameter; the preset carries it for historical reasons |
| `min_hits` 3 / 4 / 6 / 8 | 0.886 (all) | inert **in this range** — real segments here carry 61-115 hits, so any value below that never binds. 60 nudges ranges 6→7; 100 collapses the reel (P 0.647, 1 clip) |
| `rally_gap` 0.2 | 0.851 | binds, and for the worse |
| `rally_chunk` 20 / 14 / 10 / 8 / 6 | 0.810 / 0.799 / 0.823 / 0.766 / 0.706 | **the 30 s default is the best measured value.** Shorter chunks do not buy coverage — they cut mid-rally more often and promote neighbour-court density |
| several scored windows per long segment (place each on its densest 8 s of a per-second hit profile) | 0.712, ranges 6→**2** | **much worse, reverted**: chunk boundaries snap to quiet valleys of the *omnidirectional* onset stream, so the densest stretch inside a chunk is frequently the neighbours' rally while ours is retrieving a shuttle. Start-anchoring worked precisely because the boundary, not the density, marks our rally's beginning |
| clip anchored at median onset | 0.789 | loses to start-anchoring |
| clip anchored at motion peak | 0.770 | loses — a rally's peak lands at the point's end, pushing the window over the boundary |
| score onsets corroborated by ROI motion | 0.886, **bit-identical scores** | no effect: within a rally the players are continuously moving, so ROI motion rarely dips below `mean + 0.25·(peak−mean)` and corroboration degenerates to raw counting |
| trim clips to the first/last audible onset (kill the dead head and tail) | measured **0.0 s recoverable of 21.8 s** | **negative, and measured before building it**: every clip that starts before its annotated rally has 5–10 onsets within 0.1–0.3 s of its first frame — the neighbouring courts are mid-rally during our pre-serve pause. The tail is the same story (last onset sits ≤1.1 s before clip end in all 20 clips). Silence-based trimming has nothing to find here; only knowing *which* strokes are ours would. |
| end clips where court-ROI motion decays (players stop after the point) | signed error **+4.90 s** vs the fixed window's **−4.63 s** | **negative, and it inverts the premise.** ROI motion stays above `mean + 0.25·(peak−mean)` for ~5 s *past* the point — they retrieve the shuttle and set up for the next serve — so a motion-based end rule would push 18 of 20 clips further past the point, not earlier. Court motion is not a rally terminator either. |

**What the signed errors actually say (session #15, 20-clip / 160 s cut with the
court ROI set).** The dominant defect is not dead time — it is **truncation**:
the median clip ends 4.63 s *before* its rally does, because `max_clip_duration`
(8 s) is shorter than the median rally (10.5 s); only 4 of 20 clips run past the
point at all. So the reel shows roughly three quarters of each rally it
chooses. Making windows long enough to cover a whole rally was measured above
(11 s, 14 s) and *loses* precision, because the engine trusts the detected
segment boundary, and that boundary is coarser than the rally. That is the wall
stated in both directions: shorten-to-boundary fails (motion, audio), and
lengthen-to-fit fails (precision). Only knowing where the point actually ended —
the scoreboard, or a vision pass over the court — resolves it.

**How much that knowing is worth, and what cannot substitute for it.** An oracle
that anchors each 8 s window to the *end of the annotated rally* scores
**P 0.963 / 20 of 43 rallies**, against the engine's 0.822 / 17 — so ~14 points
of precision and three more rallies sit behind that one fact. The engine's own
substitute is measurably worse: anchoring to the **detected segment end** gives
**P 0.759 / 15**, with 12 of 20 windows running past the point (median +1.04 s,
versus the start-anchored −4.63 s), because the hysteresis walk exits late while
the hall keeps making noise. This also explains the session #14 result that
start-anchoring beat every density- or motion-based anchor: the segment's
*beginning* is a far more trustworthy landmark than its end.

Consequence for the sidecar's priority list: the first vision capability worth
asking for is not "tell our strokes from theirs" but **read the score overlay**
— this footage already carries the point boundaries as burned-in digits, which
is how the ground truth in this file was produced in the first place. A model
that detects score changes on the client's own media would turn that oracle into
an input the pipeline can use, without needing to recognise anyone's technique.

| `max_clip_duration` 11 s | P 0.836, R 0.106, ranges 6 | worse: longer windows cannot fit a 10.5 s median rally, so every clip spills past its boundary — and the 60 s budget then holds 6 clips instead of 8 |
| `max_clip_duration` 14 s | P 0.822, R 0.104, ranges **4** | worse again, and it loses distinct rallies. **8 s remains the measured optimum**, like `rally_chunk`'s 30 s default |

**Where the committed selection's remaining error actually is** (decomposing the
60 s reel into "inside an annotated rally" / "adjacent to its own rally" / "far
from any rally"): 53.2 s inside, 6.8 s adjacent, **0.0 s far**. Every clip is
anchored in a real rally; the loss is entirely the few seconds a detected
segment carries past the point it came from. That also bounds what is left to
win: with 473 s of rally time in the match, a 60 s reel can reach at most
recall 60/473 = **0.127**, and the committed state measures **0.112** — 88% of
the budget ceiling. Raising recall is therefore a *duration* decision
(`target_duration`, overridable per workspace style), not an algorithm one.
Trimming that adjacency was then measured directly rather than assumed (session
#15, on a 20-clip / 160 s cut): 21.8 s of it sits *before* the rally and 11.4 s
*after*, and the onset track says why it cannot be recovered — the head is
occupied by neighbours' hits within a few tenths of a second, and the tail ends
on sound too. The idea is dead on this footage, not unimplemented.

### Reel length: the one lever that was not at its optimum

The budget arithmetic above says coverage is capped by reel *length*, so length
became a per-run override (`--duration`, the UI's "reel length (s)" field). The
first measurement of it exposed a second bug: a 240 s request returned exactly
the same 10 clips / 80 s as a 120 s request. The diversity phase quota
(`phases: 5` × `max_per_window: 2`) was a **fixed ceiling on clip count**,
independent of the budget it was asked to fill.

The fix scales the number of *windows* with the budget rather than loosening
the per-window discipline, so the spread rule that the quota exists for still
holds inside every window. Measured on the same match, same style:

| target | clips | P | R | F1 | rallies covered |
|---|---|---|---|---|---|
| 60 s (shipped default) | 8 | **0.886** | 0.112 | 0.199 | 6/43 |
| 120 s, before the fix | 10 | 0.827 | 0.140 | 0.239 | 8/43 |
| 120 s, after | **15** | 0.817 | 0.207 | 0.330 | 12/43 |
| 240 s, before the fix | 10 | 0.827 | 0.140 | 0.239 | 8/43 |
| 240 s, after | **21** | 0.813 | **0.289** | **0.426** | **18/43** |

The 60 s row is bit-identical to the committed state (the quota does not bind
there yet), which is what makes the change safe to ship. Precision falls about
7 points as more marginal rallies enter the cut — that is the trade, stated
rather than hidden: a longer reel covers more of the match, each clip is
slightly less sure of itself.

Read together: **the remaining error is not reachable by re-weighting these
signals.** Precision is limited by clip windows crossing a point boundary (the
decomposition above: 11% adjacent, 0% wrongly picked), and coverage is limited
by the reel budget, not by selection quality.
Separating our strokes from the hall's needs to know *which* strokes are ours —
that is the vision sidecar's job (`frame_describe`), not a threshold.

### The scoreboard boundary, as a product feature

The recipe above stopped being a shell pipeline: the sidecar now has a
`score_changes` op, `xcut boundaries <project> --crop x,y,w,h` stores the result
per asset, an eval manifest takes `score_roi` to A/B it, and the style engine
ends a clip at the nearest reachable mark inside its window
(`style.AssetEvents.Boundaries` → `trimSegment`). The web UI's region picker can
draw either rectangle (court or scoreboard); what it writes for the scoreboard is
the **intent**, and the analyze job is what measures it — re-measuring when the
region has moved. Optional by construction: with no marks stored, selection is
what it always was, and a test pins that equality.

Measured on the owner's match (2026-09-21, same binary, same manifest, the only
difference being `score_roi` over the digits, `--duration` at the style default):

| run | P | R | F1 | ranges | clips | marks used |
| --- | --- | --- | --- | --- | --- | --- |
| no scoreboard, 60 s | 0.886 | 0.112 | 0.199 | 6/43 | 8 | — |
| 44 marks, 60 s | **0.998** | 0.127 | **0.225** | **7/43** | 8 | 44 |
| no scoreboard, 240 s | 0.813 | 0.289 | 0.426 | 18/43 | 21 | — |
| 44 marks, 240 s | **0.977** | **0.304** | **0.463** | **20/43** | 21 | 44 |
| no scoreboard, 120 s | 0.817 | 0.207 | 0.330 | 12/43 | 15 | — |
| 44 marks, 120 s | **0.972** | **0.243** | **0.389** | **16/43** | 16 | 44 |

The full marked ladder from the same harness (`--duration` swept over one match,
44 boundaries scanned each time; every row analysed again in a throwaway
workspace, so the rows are independent):

| asked | clips | reel seconds | ranges | boundary-shaped | P | F1 |
| --- | --- | --- | --- | --- | --- | --- |
| 60 s | 8 | — | 7/43 | 8/8 | 0.998 | 0.225 |
| 90 s | 12 | 85.4 | 12/43 | 12/12 | 0.997 | 0.305 |
| 120 s | 16 | — | 16/43 | 16/16 | 0.972 | 0.389 |
| 180 s | 21 | 147.2 | 20/43 | 21/21 | 0.977 | 0.463 |
| 240 s | 21 | 147.2 | 20/43 | 21/21 | 0.977 | 0.463 |
| 300 s | 21 | 147.2 | 20/43 | 21/21 | 0.977 | 0.463 |

**180 s is where this match stops answering**: 180, 240 and 300 give the same 21
clips and the same 147.2 s, because the footage offered 18 candidate rallies and
the selector had already taken all it would accept. That is a footage limit, not
a budget one — the reason `xcut timeline` now says which one it hit rather than
printing a short total in silence. (Reel seconds are only recorded for the rows
measured with the ladder script.)

One discrepancy is recorded rather than smoothed over: the same file through the
**CLI** project (`analyze → timeline --duration 300`) gives 18 clips / 126.4 s
where the **eval** harness gives 21 / 147.2 s, so the two paths do not segment
identically (proxy-vs-original analysis and cache generation are the suspects).
Every ladder row above is eval-side; reading a CLI number into this table would
misstate the curve.

The longer pairs are the more interesting ones: at a **fixed** budget the same
21 clips reach two more distinct rallies (+1.5 points of recall, +3.7 of F1)
*and* 16 points more precision, and at 120 s the marked run covers **16**
rallies where the unmarked one covered 12 with a similar clip count. Coverage is
not bought with more reel — it is bought by not paying for the dead seconds
after a point.

What moved is placement, not ranking: **all 8** marked clips end within 0.05 s
of an annotated point end (that residual is the manifest's one-decimal
rounding), while the same 8 unmarked clips miss a point end by 0.5–5.6 s, and
the first of them sits at `0.0..8.0` — inside the overlay's own fade-in — where
the marked run plays `9.2..17.2`, ending exactly as the point ends. So the
feature does the one thing it claims: the reel stops when the rally stops.

Four things this number cannot say, and should never be quoted as if it could:

1. **It is partly circular.** The annotations were derived from the same
   scoreboard recipe (step 2 above), so P/R/F1 here measure agreement with the
   score overlay, not with a judge. What is *not* circular is the mechanism: a
   signal the selector never used (the digits) is what moved the cut, and the
   montage check (step 5) is still the arbiter of whether a rally ending on a
   point is what the operator wants.
2. **Recall is unmoved** (0.112 → 0.127) because it is budget-bound, not
   placement-bound — see the reel-length section. Better ends do not buy more
   coverage out of the same 60 s.
3. **It needs a burned-in scoreboard** and an optional sidecar. Cost measured:
   8.6 s of scanning for this 10:03 720p source, once per asset, stored; the
   timeline read is free.
4. **A cross-fading overlay is sampled, not read** — at `fps=4` a mark can be up
   to ~0.25 s late, and one point can emit several raw changes until clustering
   merges them.

**The head is not recoverable the same way, and trying costs the tail.** The
marks also make the clip's *start* look fixable — 21.8 s of the measured 60 s
reel sits before the rally — so the first mark after the segment start is a
candidate head (it is the previous point's end, i.e. the serve). Measured at
240 s, same manifest, same 44 marks:

| rule | P | R | F1 | ranges | clips end on a point |
| --- | --- | --- | --- | --- | --- |
| trim the tail only (committed) | 0.977 | 0.304 | 0.463 | 20/43 | 21/21 within 1 s |
| snap the head, then trim the tail | 0.641 | 0.221 | 0.328 | 15/43 | **3/21** within 1 s |

Not a tie: moving the head forward consumes the seconds the tail rule needs to
reach its boundary, so the window shifts wholesale into the next rally's start
and ends mid-rally. The session #14 finding survives with a stronger
formulation — the segment's beginning is the trustworthy landmark, and the
boundary that pays is the one at the **end**.

**Per-clip proof, not just aggregate.** A clip the rule shaped carries
`point_end` in its timeline metadata (the inspector labels it "ends at point");
one that was not shaped carries nothing, and both directions are tested. On the
owner's match the 120 s reel's **14 of 14** clips end on a measured boundary,
each `point_end` equal to its own `source_end`. That is the check to run on your
own footage before believing any macro-average written here.

Finding it, in passing: the sidecar runs ffmpeg with its own temp dir as cwd
(its metadata dump must be a relative path), so a **relative** media path failed
as "No such file or directory" on a file that exists — which is exactly what a
manifest-relative eval case hands it. `worker.ScoreChanges` now resolves the
path before the boundary; the eval A/B is what caught it, not the unit tests.

The open half is unchanged: `hits`/`density` still rank "when the hall was
busiest", so the scoreboard fixes **where** a clip ends, not **which** rally is
worth cutting. The crop itself no longer needs a terminal: the web UI's region
picker has a "scoreboard region" target, which writes `assets.score_crop`, and
the next `analyze` run measures it (see `docs/USAGE.md`, `xcut boundaries`).

## Workflow for algorithm changes

1. Annotate a small set of representative clips (a handful of ranges each is
   enough; consistency beats volume).
2. Record a baseline at the current HEAD:
   `xcut eval manifests/x.json --out /tmp/base.json`.
3. Make the algorithm change. To test a *budget* hypothesis (does the reel get
   better if it is longer?) use `--duration` — it overrides the style's target
   for the run without editing any preset, which is what separated the length
   question from the quota question in session #15.
4. `xcut eval manifests/x.json --out /tmp/after.json --baseline /tmp/base.json`
   — it prints per-case and macro deltas (P/R/F1, ranges, dup) instead of
   leaving you to diff two JSON files by hand. It also refuses to pretend:
   a case that errored on either side reports `NOT COMPARABLE`, a case new to
   or gone from the manifest is labelled, and a different `--iou` prints
   `WARN hit_iou differs` because those deltas compare two yardsticks rather
   than two algorithms. The same guard covers the budget: results record the
   `--duration` they ran with (omitted when each style's own target applied),
   and a baseline taken at a different length prints `WARN reel length differs`
   — 8 clips against 21 measures the budget, not the change.
5. Confirm the tool itself: re-running the unchanged HEAD against its own
   baseline must print `+0.000` everywhere. Anything else is nondeterminism.
6. Record the delta in the milestone notes (docs/NIGHTLY_LOG.md).

Synthetic fixtures with construction-known ground truth (as used in the
integration tests) validate harness correctness; real annotated media
measures real quality. Both matter and neither substitutes the other.
