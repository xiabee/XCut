package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// A baseline taken at the style's own target and a run at 240 s are not two
// algorithms being compared — they are two different budgets, and the deltas
// would be dominated by how much material each was allowed to take. The
// harness already refuses to pretend when hit_iou differs; the same applies here.
func TestEvalBaselineWarnsOnReelLengthChange(t *testing.T) {
	dir := t.TempDir()
	base := &evalResults{Version: 1, GeneratedAt: "2026-09-21T00:00:00Z",
		HitIoU: 0.3, Cases: []evalCaseResult{{Name: "c", Metrics: m(0.82, 0.14, 0.24, 8)}}}
	basePath := writeDoc(t, dir, "base.json", base)

	run := &evalResults{Version: 1, GeneratedAt: "2026-09-21T00:05:00Z",
		HitIoU: 0.3, Duration: 240,
		Cases: []evalCaseResult{{Name: "c", Metrics: m(0.81, 0.29, 0.43, 18)}}}

	var out bytes.Buffer
	a := &App{Stdout: &out, Stderr: &out}
	if err := reportEvalBaseline(a, run, basePath); err != nil {
		t.Fatal(err)
	}
	msg := out.String()
	if !strings.Contains(msg, "reel length differs") {
		t.Errorf("expected a reel-length warning, got:\n%s", msg)
	}
	if !strings.Contains(msg, "different yardsticks") {
		t.Errorf("warning must say the comparison is not like-for-like:\n%s", msg)
	}
	// The numbers are still printed — the warning informs, it does not hide.
	if !strings.Contains(msg, "c") {
		t.Errorf("deltas should still be reported alongside the warning:\n%s", msg)
	}
}

// The result file is what a later run compares against, so the budget has to be
// recoverable from it. Zero (the style's own target) stays omitted so files
// written before this field keep loading unchanged.
func TestEvalResultsRecordReelLength(t *testing.T) {
	withOverride, err := json.Marshal(&evalResults{Version: 1, HitIoU: 0.3, Duration: 240})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(withOverride), `"duration":240`) {
		t.Errorf("results must record the requested reel length: %s", withOverride)
	}
	defaultTarget, err := json.Marshal(&evalResults{Version: 1, HitIoU: 0.3})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(defaultTarget), `"duration"`) {
		t.Errorf("an unset duration should stay omitted so old readers are unaffected: %s", defaultTarget)
	}
}
