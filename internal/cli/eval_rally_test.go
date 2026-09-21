package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/testmedia"
)

// TestEvalBadmintonRally runs the full evaluation loop over a synthetic
// badminton-like fixture with ground truth known by construction: three
// rallies (motion + hit bursts) separated by still, silent gaps. The v2
// badminton preset (rally mode: transient clustering + hit scoring) must
// find the rallies and avoid duplicates. Skipped when ffmpeg is absent.
func TestEvalBadmintonRally(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	root := t.TempDir()
	t.Setenv("XCUT_WORKSPACE", root)

	// 47s timeline: rallies at [6,16], [22,32], [38,46]; hits every 0.7s.
	rallies := []testmedia.RallySpec{
		{Start: 6, End: 16, HitEvery: 0.7},
		{Start: 22, End: 32, HitEvery: 0.7},
		{Start: 38, End: 46, HitEvery: 0.7},
	}
	if _, err := testmedia.GenerateRally(root, "rallies.mp4", 320, 240, 10, 47, rallies); err != nil {
		t.Fatal(err)
	}

	manifestPath := filepath.Join(root, "manifest.json")
	manifest := `{
		"version": 1,
		"cases": [
			{"name": "match_rallies", "media": "rallies.mp4",
			 "style": "badminton_highlight",
			 "expected": [
				{"start": 6, "end": 16, "label": "r1"},
				{"start": 22, "end": 32, "label": "r2"},
				{"start": 38, "end": 46, "label": "r3"}
			 ]}
		]
	}`
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	resultsPath := filepath.Join(root, "results.json")
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"eval", manifestPath, "--out", resultsPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("eval failed (exit %d)\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	b, err := os.ReadFile(resultsPath)
	if err != nil {
		t.Fatal(err)
	}
	var results struct {
		Cases []struct {
			// Selected is in the failure message on purpose: "recall 0.455
			// below 0.6" says a gate fired but not whether the clips were
			// missing, short, or placed between rallies — three different
			// bugs with three different fixes.
			Selected []struct {
				Start float64 `json:"start"`
				End   float64 `json:"end"`
			} `json:"selected"`
			Metrics *struct {
				Precision   float64 `json:"precision"`
				Recall      float64 `json:"recall"`
				RangesHit   int     `json:"ranges_hit"`
				RangesTotal int     `json:"ranges_total"`
				Duplicate   float64 `json:"duplicate_rate"`
				Clips       int     `json:"clips"`
			} `json:"metrics"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(b, &results); err != nil {
		t.Fatalf("results JSON: %v", err)
	}
	if len(results.Cases) != 1 || results.Cases[0].Metrics == nil {
		t.Fatalf("no metrics in results:\n%s", b)
	}
	m := results.Cases[0].Metrics
	picked := make([]string, 0, len(results.Cases[0].Selected))
	for _, s := range results.Cases[0].Selected {
		picked = append(picked, fmt.Sprintf("%.1f-%.1f", s.Start, s.End))
	}
	t.Logf("selected %d clips covering %s", len(picked), strings.Join(picked, " "))
	if m.RangesHit < 3 {
		t.Errorf("rally detection missed ranges: %d/%d hits", m.RangesHit, m.RangesTotal)
	}
	if m.Recall < 0.6 {
		t.Errorf("rally recall %.3f below 0.6 (clips %s)", m.Recall, strings.Join(picked, " "))
	}
	if m.Precision < 0.6 {
		t.Errorf("rally precision %.3f below 0.6 (selection spills into gaps)", m.Precision)
	}
	if m.Duplicate != 0 {
		t.Errorf("rallies must be distinct, dup rate %.2f", m.Duplicate)
	}
	if m.Clips < 1 || m.Clips > 6 {
		t.Errorf("clip count %d outside [1,6]", m.Clips)
	}
}
