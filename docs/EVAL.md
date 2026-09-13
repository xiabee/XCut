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
  selected intervals, and duplicate pairs — enough to see *where* and *why*
  the algorithm lost points.

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
