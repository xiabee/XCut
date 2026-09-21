package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/eval"
)

func resDoc(iou float64, cases ...evalCaseResult) *evalResults {
	return &evalResults{Version: 1, GeneratedAt: "2026-09-20T00:00:00Z",
		HitIoU: iou, Cases: cases, Macro: eval.Macro{RangesTotal: 43}}
}

func m(p, r, f1 float64, hits int) *eval.CaseMetrics {
	return &eval.CaseMetrics{Precision: p, Recall: r, F1: f1, RangesHit: hits}
}

func writeDoc(t *testing.T, dir, name string, doc *evalResults) string {
	t.Helper()
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEvalBaselineDeltas(t *testing.T) {
	dir := t.TempDir()
	// The spread column carries its own delta: this reel went from four
	// annotated rallies skipped in a row to two.
	baseImproved := m(0.742, 0.094, 0.167, 7)
	baseImproved.LongestMissedRun = 4
	runImproved := m(0.886, 0.112, 0.199, 6)
	runImproved.LongestMissedRun = 2

	base := writeDoc(t, dir, "base.json", resDoc(0.3,
		evalCaseResult{Name: "improved", Metrics: baseImproved},
		evalCaseResult{Name: "gone", Metrics: m(0.5, 0.5, 0.5, 2)},
		evalCaseResult{Name: "broken", Error: "ffmpeg blew up"},
	))
	var out bytes.Buffer
	a := &App{Stdout: &out, Stderr: &out}
	run := resDoc(0.3,
		evalCaseResult{Name: "improved", Metrics: runImproved},
		evalCaseResult{Name: "newcase", Metrics: m(0.4, 0.4, 0.4, 1)},
		// Present in both runs but the baseline side errored: no delta exists.
		evalCaseResult{Name: "broken", Metrics: m(0.5, 0.5, 0.5, 3)},
	)
	run.Macro = eval.Macro{Precision: 0.886, Recall: 0.112, F1: 0.199, RangesHit: 6, RangesTotal: 43}

	if err := reportEvalBaseline(a, run, base); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{
		"improved", "P +0.144", "R +0.018", "F1 +0.032", "ranges -1", "missed run -2",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q:\n%s", want, s)
		}
	}
	if !strings.Contains(s, "NEW") || !strings.Contains(s, "newcase") {
		t.Errorf("new case not flagged:\n%s", s)
	}
	if !strings.Contains(s, "DROPPED") || !strings.Contains(s, "gone") {
		t.Errorf("dropped case not flagged:\n%s", s)
	}
	if !strings.Contains(s, "NOT COMPARABLE") || !strings.Contains(s, "broken") {
		t.Errorf("baseline-side error not flagged:\n%s", s)
	}
	if strings.Contains(s, "WARN hit_iou") {
		t.Errorf("warned about IoU when both runs used 0.3:\n%s", s)
	}
}

// Two runs scored with different hit-IoU thresholds are not measuring the same
// thing; the diff has to say that instead of printing numbers that look like a
// verdict.
func TestEvalBaselineWarnsOnIncomparableYardstick(t *testing.T) {
	dir := t.TempDir()
	base := writeDoc(t, dir, "base.json", resDoc(0.5,
		evalCaseResult{Name: "c", Metrics: m(0.7, 0.1, 0.18, 7)}))
	var out bytes.Buffer
	a := &App{Stdout: &out, Stderr: &out}
	run := resDoc(0.3, evalCaseResult{Name: "c", Metrics: m(0.72, 0.11, 0.19, 8)})
	if err := reportEvalBaseline(a, run, base); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "WARN hit_iou differs") {
		t.Errorf("missing incomparability warning:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "different yardsticks") {
		t.Errorf("warning does not explain what is wrong:\n%s", out.String())
	}
}

func TestEvalBaselineRejectsForeignFiles(t *testing.T) {
	dir := t.TempDir()
	notResults := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(notResults, []byte(`{"version":1,"cases":[{"name":"x"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	a := &App{Stdout: &out, Stderr: &out}
	if err := reportEvalBaseline(a, resDoc(0.3), notResults); err == nil {
		t.Fatal("a manifest accepted as a results baseline")
	}

	garbage := filepath.Join(dir, "junk.json")
	if err := os.WriteFile(garbage, []byte("not json at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := reportEvalBaseline(a, resDoc(0.3), garbage); err == nil {
		t.Fatal("junk accepted as a results baseline")
	}
	missing := filepath.Join(dir, "nope.json")
	if err := reportEvalBaseline(a, resDoc(0.3), missing); err == nil {
		t.Fatal("missing file accepted")
	}
}

// --baseline pointing at the file --out is about to overwrite makes every later
// comparison a diff against itself.
func TestEvalBaselineRejectsSelfComparison(t *testing.T) {
	dir := t.TempDir()
	media := filepath.Join(dir, "m.mp4")
	if err := os.WriteFile(media, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	man := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(man, []byte(`{"version":1,"cases":[{"name":"c","media":"m.mp4","expected":[]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	same := filepath.Join(dir, "results.json")
	var out, errBuf bytes.Buffer
	code := Run([]string{"eval", man, "--out", same, "--baseline", same}, &out, &errBuf)
	if code == 0 {
		t.Fatal("--baseline == --out accepted")
	}
	if !strings.Contains(errBuf.String()+out.String(), "same file") {
		t.Errorf("refused for the wrong reason:\n%s%s", out.String(), errBuf.String())
	}
}

// The expensive part of an eval is the run; a bad --baseline is discovered
// afterwards and must not cost the results file. Uses a case whose media is
// absent so no ffmpeg is needed: the case errors, the document is still
// written, and the baseline problem is then reported.
func TestEvalWritesResultsBeforeReportingBaselineFailure(t *testing.T) {
	dir := t.TempDir()
	man := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(man, []byte(`{"version":1,"cases":[{"name":"c","media":"missing.mp4","expected":[{"start":1,"end":2}]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "results.json")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"eval", man, "--out", out, "--baseline", filepath.Join(dir, "nope.json")}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("run with a missing baseline exited clean")
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("results were discarded because the baseline was unreadable: %v\n%s", err, stdout.String())
	}
	msg := stdout.String() + stderr.String()
	if !strings.Contains(msg, "cannot read --baseline") {
		t.Errorf("baseline failure not reported: %s", msg)
	}
}
