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
- **Longest missed run** — how many annotated rallies *in a row* the reel did not
  touch at all. Precision, recall, F1 and range hits are all indifferent to where
  the picks fall: three clips on the first three rallies of a six-rally match
  score identically to three spread over it. `TestScoreMissedRunSeparates-
  WhatPRTakesAsEqual` is written against exactly such a pair.

Measured on the owner's match (43 annotated rallies over 0.2..602.5 s, scoreboard
marks in place, the shipped slice rule; replayed at `fbe5ab4` with the reference
sidecar, `xcut eval … --duration`):

| asked | clips | P | F1 | ranges | longest missed run | on a point |
| --- | --- | --- | --- | --- | --- | --- |
| 60 s (old default) | 8 | 0.998 | 0.225 | 7/43 | **10 rallies** | 8/8 |
| 120 s (**default now**) | 16 | 0.972 | 0.389 | 16/43 | 4 | 16/16 |
| 180 s | 23 | 0.979 | 0.455 | 20/43 | 2 | 22/23 |
| 240 s | 27 | 0.987 | 0.562 | 24/43 | 2 | 26/27 |

The default reel leaves ten consecutive rallies unrepresented — a third of the
match's scoring events gone from one stretch while its eight clips sit elsewhere.
The preset looked like the mechanism: `diversity.phases` 5 with `max_per_window` 2
divides a 603 s match into five 120 s windows and caps each at two clips, but
nothing *floors* it, so one window may receive none.

**That attribution was wrong, and writing the floor is what found out.** With the
missing rule added (`diversity.min_per_window`: each window's first clip before
any window takes a second), the 120, 180 and 240 s reels come out the same
selection byte for byte, and the 60 s reel keeps its eight rallies with two
different trims — the budget reaches them in another order, so `344.0..352.0`
becomes `345.8..352.0` and `573.5..577.5` becomes `571.7..577.5`, total still
60.00 s, and the longer head on the last clip reaches a rally that reel had missed
by ~1.8 s (7/43 → 8/43, P 0.998 unchanged). The ten-rally gap is not an empty
window: the ceiling had already spread the picks, and the missing rallies sit
*inside* windows that do have a clip in them. The number that moves this metric is
the budget — 10 → 4 → 2 as the reel is allowed to be longer, with precision flat
across the whole ladder.

So the floor ships because the pathology is real where the ceiling cannot reach it
(`TestPhaseFloorTakesAnEmptyWindowBeforeDoublingUp` builds exactly such a source,
and the reel is unchanged with the knob off), and because on this match it costs
nothing. The coverage problem is answered where the measurement says it lives: the
default `target_duration` went 60 → 120 s on the owner's decision that defaults
may be raised, at a cost already measured for that exact length — 14 clips /
105.7 s out in 17.8 s wall, 0.17x (docs/PERFORMANCE.md, session #18).

**This metric replaced a seconds-based one, and the first version misled me.**
`longest_skip` reported 164 s at the 60 s default, which looked like a damning
clustering number; checking it against the annotations showed only 39 s of that
was rally content — the rest is the between-point dead time no highlight belongs
in. Counting untouched rallies instead measures the thing the metric is for.

One difference between the harness and the plain CLI path is now explained
rather than left open: the manifest's `asset_roi` (court region) is applied to
the case's asset, and the ROI track yields **21** candidate rallies where the
same file without a per-source region yields **18** (three chunks dropped at the
full-frame motion floor). So the ladder above is the *court-ROI + boundaries*
configuration — the best the feature offers — and a project that never drew a
region reaches a shorter reel from the same match. Both numbers are true of
their own path; putting them in one row is not.

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

## Cutting on the beat (`卡点`)

`beat_snap_tolerance` lets a clip end that nothing else fixed move to the nearest
beat of the source's own grid. Two things had to be measured before it could be
trusted, and only one of them came out as hoped.

**That it works, through the real path.** `TestBeatSnappingThroughTheRealAnalysisPath`
generates a 120 BPM click rally, runs it through the shipped analyzer and the
analysis cache, and asks for the same 7.7 s reel twice — rule off, rule on at half
a beat period. Every end starts off the lattice (the control: if they already sat
on it, the assertions below could pass with the rule dead) and finishes on it, with
the per-clip `beat` metadata agreeing with the geometry to four decimals. Selection
is untouched: same clips, same starts, ends moved by ≤ the tolerance.

**That on the owner's match it does nothing, and why.** Three runs at 120 s on the
marked manifest — off, `0.12`, `0.25` — return the same reel to the digit
(P 0.972, R 0.243, F1 0.389, 16/43, 16 clips, **16/16 ends on a measured point**).
That half is the precedence rule working: a beat does not get to move a cut that
already lands as the score changes. The unmarked manifest is the interesting half:
its ends are free, and still nothing moved, because the hall's 1493 onsets carry no
grid the estimator will believe — no period in 30–300 BPM explains ≥90% of crowd
noise plus shuttlecock contact, and B1 was written to say "no grid" rather than
invent one.

So the number that matters for the product is not a metric here but a consequence:
**a reel cuts to the music laid under it, not to its own location audio.** The
snap, the grid and the per-clip proof are ready; what is missing is a beat-bearing
input, which is where `docs/ROADMAP.md` Phase 5 B4 now starts — an imported music
bed, the grid estimated from that file, and a mux that carries it.

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
