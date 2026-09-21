package worker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/xiabee/XCut/internal/testmedia"
)

// scoreChangesSidecar locates the repo's reference sidecar; the test drives the
// real script, not a Go stand-in, because the point of this client is the
// cross-language contract.
func scoreChangesSidecar(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("python"); err != nil {
		t.Skip("python not on PATH")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Skip("cannot locate repo root")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	script := filepath.Join(root, "scripts", "xcut-ai-sidecar.py")
	if _, err := os.Stat(script); err != nil {
		t.Skipf("reference sidecar missing: %v", err)
	}
	return script
}

// scoreboardFixture makes a 16 s clip whose only pixel change is the synthetic
// score box in the top-left corner, flipping at 4 s and 12 s. The spacing is
// deliberate: frames from one cross-fading change merge inside the sidecar's
// 5 s cluster window, and a scoreboard flips once per point — points are
// further apart than that. Expected times are hand-derived from the fixture
// construction, not computed by the code under test.
func scoreboardFixture(t *testing.T, marks ...float64) string {
	t.Helper()
	return scoreboardFixtureIn(t, t.TempDir(), marks...)
}

func scoreboardFixtureIn(t *testing.T, dir string, marks ...float64) string {
	t.Helper()
	path, err := testmedia.GenerateScoreboard(dir, "board.mp4", 320, 240, 10, 16, marks)
	if err != nil {
		t.Skipf("cannot generate fixture: %v", err)
	}
	return path
}

// TestScoreChangesResolvesARelativePath is the regression for what the real
// match A/B hit: the sidecar runs ffmpeg with its own temp dir as cwd, so a
// relative input — exactly what a manifest-relative eval case hands it — was
// looked up in a directory the caller never chose, and failed as "no such file"
// on a file that plainly exists.
func TestScoreChangesResolvesARelativePath(t *testing.T) {
	script := scoreChangesSidecar(t)
	dir := t.TempDir()
	scoreboardFixtureIn(t, dir, 6)
	t.Chdir(dir)

	times, err := ScoreChanges(context.Background(), script, "board.mp4",
		[]float64{0, 0, 0.4, 0.3}, time.Minute)
	if err != nil {
		t.Fatalf("relative path: %v", err)
	}
	if len(times) != 1 {
		t.Fatalf("want the one change through a relative path, got %v", times)
	}
}

func TestScoreChangesAgainstReferenceSidecar(t *testing.T) {
	script := scoreChangesSidecar(t)
	media := scoreboardFixture(t, 4, 12)

	times, err := ScoreChanges(context.Background(), script, media,
		[]float64{0, 0, 0.4, 0.3}, time.Minute)
	if err != nil {
		t.Fatalf("score_changes: %v", err)
	}
	if len(times) != 2 {
		t.Fatalf("want the two corner changes, got %v", times)
	}
	for i, want := range []float64{4.0, 12.0} {
		if d := times[i] - want; d < -0.75 || d > 0.75 {
			t.Errorf("boundary %d = %.2f, want within 0.75s of %.1f", i, times[i], want)
		}
	}
}

// TestScoreChangesCountsOnlyChangesInsideTheCrop pins that params.crop is
// actually honored — a detector that ignored it would still pass the timing
// test above, because the corner is the only thing that ever changes. Both
// halves matter: a table that only asserted "found nothing" would also be
// satisfied by a detector that never reports anything.
func TestScoreChangesCountsOnlyChangesInsideTheCrop(t *testing.T) {
	script := scoreChangesSidecar(t)
	media := scoreboardFixture(t, 6)

	quiet, err := ScoreChanges(context.Background(), script, media,
		[]float64{0.6, 0.65, 0.4, 0.35}, time.Minute)
	if err != nil {
		t.Fatalf("a crop aimed at a still corner is not an error: %v", err)
	}
	if len(quiet) != 0 {
		t.Fatalf("boundaries %v found outside the changed corner; params.crop was ignored", quiet)
	}

	loud, err := ScoreChanges(context.Background(), script, media,
		[]float64{0, 0, 0.4, 0.3}, time.Minute)
	if err != nil {
		t.Fatalf("score_changes on the changed corner: %v", err)
	}
	if len(loud) != 1 {
		t.Fatalf("want the one corner change, got %v", loud)
	}
	if d := loud[0] - 6.0; d < -0.75 || d > 0.75 {
		t.Errorf("boundary = %.2f, want within 0.75s of 6.0", loud[0])
	}
}

func TestScoreChangesRejectsBadCrop(t *testing.T) {
	script := scoreChangesSidecar(t)
	media := scoreboardFixture(t, 4, 12)
	if _, err := ScoreChanges(context.Background(), script, media,
		[]float64{0.8, 0.8, 0.5, 0.5}, time.Minute); err == nil {
		t.Fatal("a crop extending past the frame must be an error, not an empty result")
	}
}

func TestScoreChangesNeedsAFile(t *testing.T) {
	script := scoreChangesSidecar(t)
	if _, err := ScoreChanges(context.Background(), script,
		filepath.Join(t.TempDir(), "absent.mp4"), []float64{0, 0, 0.2, 0.2}, time.Minute); err == nil {
		t.Fatal("a missing media file must not report boundaries")
	}
}
