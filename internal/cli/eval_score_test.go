package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/testmedia"
	"github.com/xiabee/XCut/internal/worker"
)

// evalCaseJSON mirrors the one part of the results document these two tests
// read. Written by hand on purpose: decoding into the command's own struct would
// let a field rename quietly un-pin the assertion.
type evalCaseJSON struct {
	Name       string          `json:"name"`
	Error      string          `json:"error"`
	ScoreMarks int             `json:"score_marks"`
	Metrics    json.RawMessage `json:"metrics"`
}

func scoreROICase(t *testing.T) (manifest, results string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XCUT_WORKSPACE", root)
	if _, err := testmedia.GenerateScoreboard(root, "board.mp4", 320, 240, 10, 16, []float64{4, 12}); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	manifest = filepath.Join(root, "score.json")
	body := `{"version":1,"cases":[{"name":"board_case","media":"board.mp4",` +
		`"style":"generic_highlight","expected":[{"start":4,"end":12,"label":"rally"}],` +
		`"score_roi":{"x":0,"y":0,"w":0.4,"h":0.3}}]}`
	if err := os.WriteFile(manifest, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return manifest, filepath.Join(root, "results.json")
}

func firstCase(t *testing.T, results string) evalCaseJSON {
	t.Helper()
	raw, err := os.ReadFile(results)
	if err != nil {
		t.Fatalf("results document missing: %v", err)
	}
	var doc struct {
		Cases []evalCaseJSON `json:"cases"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("results unreadable: %v (%.200s)", err, raw)
	}
	if len(doc.Cases) != 1 {
		t.Fatalf("want one case, got %d: %s", len(doc.Cases), raw)
	}
	return doc.Cases[0]
}

// TestEvalScoreROIRefusesWithoutSidecar: a manifest that asks for score_roi is
// asking for a measurement, so the case must come back refused and named — not
// with a reel that quietly ignored the region (D13). The refusal is the half that
// runs on any host, which is why it is asserted here rather than assumed from
// the sidecar test below.
func TestEvalScoreROIRefusesWithoutSidecar(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	if worker.ResolveAIBin("") != "" {
		t.Skip("a scoreboard sidecar is discoverable on PATH; this test measures the refusal")
	}
	manifest, results := scoreROICase(t)
	var stdout, stderr bytes.Buffer
	Run([]string{"eval", manifest, "--out", results}, &stdout, &stderr)

	c := firstCase(t, results)
	if c.Error == "" {
		t.Fatalf("a score_roi case with no sidecar reported no error; stdout:\n%s", stdout.String())
	}
	for _, want := range []string{"board_case", "score_roi", "workers.ai_bin"} {
		if !strings.Contains(c.Error, want) {
			t.Errorf("refusal %q does not mention %q", c.Error, want)
		}
	}
	if c.ScoreMarks != 0 {
		t.Errorf("score_marks = %d on a refused case, want 0", c.ScoreMarks)
	}
	if len(c.Metrics) != 0 && string(c.Metrics) != "null" {
		t.Errorf("a refused case must not carry metrics: %s", c.Metrics)
	}
}

// TestEvalScoreROIMeasuresThroughTheProductionWrite is the other half: with the
// reference sidecar configured, eval must store the fixture's two corner changes
// and report the count, through `pipeline.ScoreScan` — the same function the
// analyze fan-out uses. The count has to survive even though this case's *reel*
// fails: the fixture is a black board with a changing number, so it carries no
// scene motion and the style is right to refuse it ("no events satisfy the
// style's clip constraints"). That refusal is what first exposed the bug —
// evalRunCase returned 0 marks on every failure path, so the results document
// said "scanned nothing" about a case that had measured two. Do not "fix" the
// motionless fixture into a success: that would need a generator with both
// motion and a scoreboard, and would test the style rather than this write.
func TestEvalScoreROIMeasuresThroughTheProductionWrite(t *testing.T) {
	script := boundariesSidecar(t)
	manifest, results := scoreROICase(t)
	t.Setenv("XCUT_AI_BIN", script)

	var stdout, stderr bytes.Buffer
	// The exit code is deliberately not asserted: a motionless fixture may or
	// may not yield a reel, and neither answer is this feature.
	Run([]string{"eval", manifest, "--out", results}, &stdout, &stderr)

	c := firstCase(t, results)
	if strings.Contains(c.Error, "workers.ai_bin") {
		t.Fatalf("the configured sidecar was not found: %s", c.Error)
	}
	if c.ScoreMarks != 2 {
		t.Fatalf("score_marks = %d, want the fixture's 2 corner changes (stdout:\n%s)", c.ScoreMarks, stdout.String())
	}
}
