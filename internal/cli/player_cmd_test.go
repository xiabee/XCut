package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/storage"
)

// seedPlayerAsset creates the workspace rows cmdPlayer addresses, through the
// same db the command will open.
func seedPlayerAsset(t *testing.T, root, project, path string) string {
	t.Helper()
	db, err := storage.Open(filepath.Join(root, "xcut.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	p, err := db.CreateProject(t.Context(), project)
	if err != nil {
		t.Fatal(err)
	}
	a := &storage.Asset{ProjectID: p.ID, Path: path, Filename: path, Fingerprint: "fp-" + path}
	if err := db.UpsertAsset(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	return a.ID
}

// TestPlayerCommandSeedsTheSpot drives `xcut player` end to end: show before
// anything is set says so plainly, --set stores the rect and the drawn-at
// moment on the row, show after names the un-measured signature and the
// command that measures it, and every malformed invocation is refused with
// the sentence that fixes it.
func TestPlayerCommandSeedsTheSpot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XCUT_WORKSPACE", root)
	aid := seedPlayerAsset(t, root, "pf", "cam1.mp4")

	var stdout, stderr bytes.Buffer
	run := func(args ...string) int {
		stdout.Reset()
		stderr.Reset()
		return Run(append([]string{"player", "pf"}, args...), &stdout, &stderr)
	}

	if code := run("--asset", aid); code != 0 || !strings.Contains(stdout.String(), "has no player spot") {
		t.Fatalf("initial show: exit %d, stdout:\n%s%s", code, stdout.String(), stderr.String())
	}

	if code := run("--asset", aid, "--set", "0.3,0.2,0.16,0.3", "--at", "12.5"); code != 0 {
		t.Fatalf("set: exit %d, stderr:\n%s", code, stderr.String())
	}
	db, err := storage.Open(filepath.Join(root, "xcut.db"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := db.GetAsset(t.Context(), aid)
	if err != nil || got == nil || got.PlayerSpot == nil {
		t.Fatalf("asset after set: %+v (%v)", got, err)
	}
	db.Close()
	if got.PlayerSpot.At != 12.5 || len(got.PlayerSpot.Rect) != 4 || got.PlayerSpot.Rect[0] != 0.3 {
		t.Fatalf("stored spot = %v at %.2f, want the flag's values", got.PlayerSpot.Rect, got.PlayerSpot.At)
	}
	if len(got.PlayerSpot.Bins) != 0 {
		t.Fatal("--set must seed intent, not invent a signature")
	}

	if code := run("--asset", aid); code != 0 ||
		!strings.Contains(stdout.String(), "signature not measured yet") ||
		!strings.Contains(stdout.String(), "xcut analyze pf") {
		t.Fatalf("show after set: exit %d, stdout:\n%s%s", code, stdout.String(), stderr.String())
	}

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--asset", aid, "--set", "0.9,0.9,0.5,0.5"}, "0<=x,y and x+w<=1"},
		{[]string{"--asset", aid, "--set", "0.1,0.2,later,0.4"}, "is not a number"},
		{[]string{"--asset", aid, "--set", "0.1,0.2,0.3,0.4", "--at", "oops"}, "--at must be seconds"},
		{[]string{"--set", "0.1,0.2,0.3,0.4"}, "--asset <id> is required"},
		{[]string{"--asset", "asst_missing", "--set", "0.1,0.2,0.3,0.4"}, "asset not found"},
	} {
		if code := run(tc.args...); code != 1 || !strings.Contains(stderr.String(), tc.want) {
			t.Errorf("args %v: exit %d, want 1 naming %q; stderr:\n%s%s",
				tc.args, code, tc.want, stdout.String(), stderr.String())
		}
	}
}

// TestPlayerBareCommandListsEveryAsset: bare `xcut player <project>` names
// each asset's spot state — the listing `xcut roi` already ends with, so a
// script (or a human with two cameras) can see what the person filter will
// eat without walking assets one --asset at a time. The three row states are
// pinned: no spot, full model (bins + bands), and the band-starved marker.
func TestPlayerBareCommandListsEveryAsset(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XCUT_WORKSPACE", root)
	// One project, three assets — seedPlayerAsset makes a project per call.
	p := seedPlayerProject(t, root, "pfl")
	aid := seedProjectAsset(t, root, p, "cam1.mp4")
	aid2 := seedProjectAsset(t, root, p, "cam2.mp4")
	aid3 := seedProjectAsset(t, root, p, "cam3.mp4")

	db, err := storage.Open(filepath.Join(root, "xcut.db"))
	if err != nil {
		t.Fatal(err)
	}
	full := &storage.PlayerSpot{Rect: []float64{0.1, 0.1, 0.2, 0.2}, At: 3,
		Bins: make([]float64, 162), SampledAt: 1770000000}
	bands := make([][]float64, 3)
	for b := range bands {
		bands[b] = make([]float64, 162)
	}
	full.Bands = bands
	if err := db.SetAssetPlayerSpot(t.Context(), aid, full); err != nil {
		t.Fatal(err)
	}
	starved := &storage.PlayerSpot{Rect: []float64{0.3, 0.3, 0.2, 0.2}, At: 0,
		Bins: make([]float64, 162), Bands: make([][]float64, 3), SampledAt: 1770000000}
	if err := db.SetAssetPlayerSpot(t.Context(), aid2, starved); err != nil {
		t.Fatal(err)
	}
	db.Close()

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"player", "pfl"}, &stdout, &stderr); code != 0 {
		t.Fatalf("bare listing: exit %d, stderr:\n%s%s", code, stdout.String(), stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"no spot",
		"signature measured (162 bins + 3 bands)",
		"band-starved shape",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("listing missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, aid) || !strings.Contains(out, aid2) || !strings.Contains(out, aid3) {
		t.Fatalf("listing lost an asset id:\n%s", out)
	}

	// Seeding without an asset stays refused, with the listing's existence
	// named as the way out.
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"player", "pfl", "--set", "0.1,0.1,0.2,0.2"}, &stdout, &stderr); code != 1 ||
		!strings.Contains(stderr.String(), "--asset <id> is required") {
		t.Fatalf("set without asset: exit %d, stderr:\n%s%s", code, stdout.String(), stderr.String())
	}
}

// seedPlayerProject / seedProjectAsset: the bare-listing test needs one
// project carrying several assets — seedPlayerAsset makes a project per call.
func seedPlayerProject(t *testing.T, root, name string) string {
	t.Helper()
	db, err := storage.Open(filepath.Join(root, "xcut.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	p, err := db.CreateProject(t.Context(), name)
	if err != nil {
		t.Fatal(err)
	}
	return p.ID
}

func seedProjectAsset(t *testing.T, root, pid, path string) string {
	t.Helper()
	db, err := storage.Open(filepath.Join(root, "xcut.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a := &storage.Asset{ProjectID: pid, Path: path, Filename: path, Fingerprint: "fp-" + path}
	if err := db.UpsertAsset(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	return a.ID
}
