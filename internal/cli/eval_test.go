package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/testmedia"
)

// TestEvalHarness runs the evaluation command end-to-end against a synthetic
// fixture whose ground truth is known by construction: 6 distinct scenes of
// 3s each. Annotating every scene must score a perfect selection (the style
// can only pick within scene bounds); partial annotations must score exactly
// the annotated fraction. Skipped when ffmpeg is absent.
func TestEvalHarness(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}

	root := t.TempDir()
	t.Setenv("XCUT_WORKSPACE", root)

	scenes := []testmedia.Scene{
		{Seconds: 3, Color: "red", Frequency: 440},
		{Seconds: 3, Color: "green", Frequency: 880},
		{Seconds: 3, Color: "blue", Frequency: 220},
		{Seconds: 3, Color: "white", Frequency: 660},
		{Seconds: 3, Color: "black", Frequency: 330},
		{Seconds: 3, Color: "yellow", Frequency: 550},
	}
	if _, err := testmedia.Generate(root, "cases.mp4", scenes, 320, 240, 10); err != nil {
		t.Fatal(err)
	}

	manifestPath := filepath.Join(root, "manifest.json")
	manifest := `{
		"version": 1,
		"cases": [
			{"name": "full", "media": "cases.mp4", "expected": [
				{"start": 0, "end": 3, "label": "s1"}, {"start": 3, "end": 6, "label": "s2"},
				{"start": 6, "end": 9, "label": "s3"}, {"start": 9, "end": 12, "label": "s4"},
				{"start": 12, "end": 15, "label": "s5"}, {"start": 15, "end": 18, "label": "s6"}
			]},
			{"name": "partial", "media": "cases.mp4", "expected": [
				{"start": 0, "end": 6}
			]},
			{"name": "missing_media", "media": "nope.mp4", "expected": [
				{"start": 0, "end": 6}
			]}
		]
	}`
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	resultsPath := filepath.Join(root, "results.json")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"eval", manifestPath, "--out", resultsPath}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("eval must exit nonzero when a case fails\nstdout:\n%s", stdout.String())
	}
	// The two runnable cases still reported.
	out := stdout.String()
	for _, want := range []string{"full", "partial", "missing_media", "macro:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("eval output missing %q:\n%s", want, out)
		}
	}

	b, err := os.ReadFile(resultsPath)
	if err != nil {
		t.Fatalf("results file missing: %v", err)
	}
	var results struct {
		Version int `json:"version"`
		Cases   []struct {
			Name    string `json:"name"`
			Error   string `json:"error"`
			Metrics *struct {
				Precision   float64 `json:"precision"`
				Recall      float64 `json:"recall"`
				F1          float64 `json:"f1"`
				RangesHit   int     `json:"ranges_hit"`
				RangesTotal int     `json:"ranges_total"`
				DupRate     float64 `json:"duplicate_rate"`
				Clips       int     `json:"clips"`
			} `json:"metrics"`
		} `json:"cases"`
		Macro struct {
			Cases       int `json:"cases"`
			CasesFailed int `json:"cases_failed"`
			RangesTotal int `json:"ranges_total"`
		} `json:"macro"`
	}
	if err := json.Unmarshal(b, &results); err != nil {
		t.Fatalf("results JSON: %v\n%s", err, b)
	}

	if results.Macro.Cases != 3 || results.Macro.CasesFailed != 1 || results.Macro.RangesTotal != 8 {
		t.Fatalf("macro totals wrong: %+v", results.Macro)
	}

	byName := map[string]int{}
	for i, c := range results.Cases {
		byName[c.Name] = i
	}
	if results.Cases[byName["missing_media"]].Error == "" {
		t.Fatal("missing_media case must fail")
	}

	full := results.Cases[byName["full"]].Metrics
	if full == nil {
		t.Fatal("full case has no metrics")
	}
	// The style selects scene-shaped events; annotated everything → ~1.0.
	if full.Precision < 0.99 || full.Recall < 0.99 || full.F1 < 0.99 {
		t.Errorf("full annotation should score ~1: P=%g R=%g F1=%g", full.Precision, full.Recall, full.F1)
	}
	if full.RangesHit != 6 || full.RangesTotal != 6 {
		t.Errorf("full ranges %d/%d want 6/6", full.RangesHit, full.RangesTotal)
	}
	if full.DupRate != 0 {
		t.Errorf("full dup rate %g want 0", full.DupRate)
	}

	partial := results.Cases[byName["partial"]].Metrics
	if partial == nil {
		t.Fatal("partial case has no metrics")
	}
	// Only [0,6) annotated out of 18s of selection: R=1, P≈1/3.
	if partial.Recall < 0.99 {
		t.Errorf("partial recall %g want ~1", partial.Recall)
	}
	if partial.Precision < 0.30 || partial.Precision > 0.37 {
		t.Errorf("partial precision %g want ~0.33", partial.Precision)
	}
	if partial.RangesHit != 1 || partial.RangesTotal != 1 {
		t.Errorf("partial ranges %d/%d want 1/1", partial.RangesHit, partial.RangesTotal)
	}
}

// TestEvalManifestErrors covers CLI-level manifest validation.
func TestEvalManifestErrors(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XCUT_WORKSPACE", root)

	var stdout, stderr bytes.Buffer
	run := func(args ...string) int {
		stdout.Reset()
		stderr.Reset()
		return Run(args, &stdout, &stderr)
	}

	if code := run("eval"); code == 0 {
		t.Error("eval without manifest must fail")
	}
	if code := run("eval", filepath.Join(root, "absent.json")); code == 0 {
		t.Error("eval with missing manifest must fail")
	}

	bad := filepath.Join(root, "bad.json")
	os.WriteFile(bad, []byte(`{"version":9,"cases":[]}`), 0o644)
	if code := run("eval", bad); code == 0 {
		t.Error("eval with invalid manifest must fail")
	}
	if !strings.Contains(stderr.String(), "eval") {
		t.Errorf("stderr should mention the failure: %s", stderr.String())
	}
}

// TestEvalCaseNameCollision: two manifest cases whose names sanitize
// identically ("a b" and "a/b" -> "a_b") must both run — the project name
// is disambiguated instead of the second case dying on the UNIQUE
// name constraint with a raw storage error.
func TestEvalCaseNameCollision(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	root := t.TempDir()
	t.Setenv("XCUT_WORKSPACE", root)

	if _, err := testmedia.Generate(root, "fx.mp4", testmedia.DefaultFixture(), 320, 240, 10); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, "manifest.json")
	manifest := `{
		"version": 1,
		"cases": [
			{"name": "a b", "media": "fx.mp4", "expected": [{"start": 0, "end": 3}]},
			{"name": "a/b", "media": "fx.mp4", "expected": [{"start": 0, "end": 3}]}
		]
	}`
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	resultsPath := filepath.Join(root, "results.json")
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"eval", manifestPath, "--out", resultsPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("eval failed (%d)\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	b, err := os.ReadFile(resultsPath)
	if err != nil {
		t.Fatal(err)
	}
	// No case may carry an error, and both sanitized-colliding names ran.
	if strings.Contains(string(b), `"error"`) {
		t.Fatalf("cases must succeed without storage errors:\n%s", b)
	}
	for _, name := range []string{`"name": "a b"`, `"name": "a/b"`} {
		if !strings.Contains(string(b), name) {
			t.Fatalf("case %s missing from results:\n%s", name, b)
		}
	}
}

// TestEvalCheckMode: --check validates manifest, media presence, annotation
// ranges against real durations, and style resolution in seconds — without
// running the pipeline or creating any workspace state. Skipped when
// ffmpeg is absent (duration checks need ffprobe).
func TestEvalCheckMode(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	root := t.TempDir()
	t.Setenv("XCUT_WORKSPACE", root)

	scenes := []testmedia.Scene{
		{Seconds: 3, Color: "red", Frequency: 440},
		{Seconds: 3, Color: "green", Frequency: 880},
	}
	if _, err := testmedia.Generate(root, "ok.mp4", scenes, 320, 240, 10); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	run := func(args ...string) int {
		stdout.Reset()
		stderr.Reset()
		return Run(args, &stdout, &stderr)
	}

	good := filepath.Join(root, "good.json")
	manifest := `{
		"version": 1,
		"cases": [
			{"name": "ok", "media": "ok.mp4", "expected": [{"start": 0, "end": 6}]},
			{"name": "roi", "media": "ok.mp4", "style": "badminton_highlight",
			 "asset_roi": {"x": 0.1, "y": 0.1, "w": 0.5, "h": 0.5},
			 "expected": [{"start": 0, "end": 3}]}
		]
	}`
	if err := os.WriteFile(good, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := run("eval", good, "--check"); code != 0 {
		t.Fatalf("--check must pass a clean manifest (code %d)\nstdout:\n%s\nstderr:\n%s",
			code, stdout.String(), stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "check: OK") {
		t.Fatalf("clean manifest must end with check: OK:\n%s", out)
	}
	for _, name := range []string{"ok", "roi"} {
		if !strings.Contains(out, name) {
			t.Fatalf("check output must list case %q:\n%s", name, out)
		}
	}
	// Check mode is read-only: only the fixture and the manifest exist.
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name()] = true
	}
	if len(names) != 2 || !names["ok.mp4"] || !names["good.json"] {
		t.Fatalf("--check must not create workspace state, found: %v", names)
	}

	bad := filepath.Join(root, "bad.json")
	badManifest := `{
		"version": 1,
		"cases": [
			{"name": "overrun", "media": "ok.mp4", "expected": [{"start": 0, "end": 999}]},
			{"name": "gone", "media": "nope.mp4", "expected": [{"start": 0, "end": 3}]},
			{"name": "badstyle", "media": "ok.mp4", "style": "no_such_style",
			 "expected": [{"start": 0, "end": 3}]}
		]
	}`
	if err := os.WriteFile(bad, []byte(badManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := run("eval", bad, "--check"); code == 0 {
		t.Fatal("--check must fail a manifest with problems")
	}
	out = stdout.String()
	for _, want := range []string{
		"overrun", "exceeds media duration",
		"gone", "media missing: nope.mp4",
		"badstyle", "unknown style: no_such_style",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("check output missing %q:\n%s", want, out)
		}
	}

	// Inert flags are rejected loudly instead of silently ignored.
	if code := run("eval", good, "--check", "--out", filepath.Join(root, "r.json")); code == 0 {
		t.Fatal("--out with --check must be rejected")
	}
	if code := run("eval", good, "--check", "--iou", "0.5"); code == 0 {
		t.Fatal("--iou with --check must be rejected")
	}
}
