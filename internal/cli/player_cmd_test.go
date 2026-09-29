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
