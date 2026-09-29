package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/testmedia"
)

// TestAutoPlayerSpotSeedsTheRowAndMeasuresIt is the one-shot's half of the
// person-filter arc: `--player-spot` writes the intent to every asset before
// analyze, the analyze that follows measures the signature from it, and the
// run says both parts out loud. The row after the run is the assertion — a
// flag that parsed but never landed would leave a spotless row behind it.
func TestAutoPlayerSpotSeedsTheRowAndMeasuresIt(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	root := t.TempDir()
	t.Setenv("XCUT_WORKSPACE", root)
	fixture, err := testmedia.GenerateRally(root, "match.mp4", 320, 240, 25, 14.3,
		[]testmedia.RallySpec{{Start: 0, End: 14, HitEvery: 0.5}})
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"auto", fixture, "--project", "spotted", "--style", "generic_highlight",
		"--duration", "6", "--player-spot", "0.3,0.3,0.2,0.2,7",
		"--out", filepath.Join(root, "spotted.mp4")}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("auto exited %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "==> player spot 0.3,0.3,0.2,0.2,7 on 1 asset(s)") {
		t.Fatalf("the run never announced the spot seeding:\n%s", stdout.String())
	}

	db, err := storage.Open(filepath.Join(root, "xcut.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	p, err := db.GetProjectByName(t.Context(), "spotted")
	if err != nil || p == nil {
		t.Fatalf("project: %+v (%v)", p, err)
	}
	assets, err := db.ListAssets(t.Context(), p.ID)
	if err != nil || len(assets) != 1 {
		t.Fatalf("ListAssets: %d (%v)", len(assets), err)
	}
	spot := assets[0].PlayerSpot
	if spot == nil {
		t.Fatal("--player-spot never reached the asset row")
	}
	if spot.At != 7 || len(spot.Rect) != 4 || spot.Rect[0] != 0.3 {
		t.Fatalf("spot = %v at %.2f, want the flag's rect and drawn-at", spot.Rect, spot.At)
	}
	if len(spot.Bins) == 0 || spot.SampledAt == 0 {
		t.Fatal("auto's own analyze did not measure the signature it was seeded with")
	}
}
