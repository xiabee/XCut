package cli

import (
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/timeline"
)

func pacingReel(clips ...timeline.Clip) *timeline.Timeline {
	for i := range clips {
		clips[i].TimelineStart = float64(i) * 10
	}
	return &timeline.Timeline{Tracks: []timeline.Track{{ID: "v1", Kind: "video", Clips: clips}}}
}

func pacingShot(srcStart, srcEnd float64, score string) timeline.Clip {
	c := timeline.Clip{SourceStart: srcStart, SourceEnd: srcEnd, Speed: 1}
	if score != "" {
		c.Metadata = map[string]string{"score": score}
	}
	return c
}

// TestPacingLine: the readout exists so a style can be compared on shape, which
// no selection metric sees. Each clause is a number the reel itself carries —
// nothing here may be inferred from the style's own promises.
func TestPacingLine(t *testing.T) {
	line := pacingLine(pacingReel(
		pacingShot(0, 4, "0.3"),     // 4 s
		pacingShot(100, 102, "0.9"), // 2 s, the top score
		pacingShot(200, 206, "0.4"), // 6 s
	))
	for _, needle := range []string{"3 shots", "mean 4.0s", "median 4.0s", "longest 6.0s", "top shot starts at 10.0s"} {
		if !strings.Contains(line, needle) {
			t.Errorf("pacing line %q lacks %q", line, needle)
		}
	}
	if !strings.HasPrefix(line, "pacing: ") {
		t.Errorf("pacing line %q does not read as a pacing line", line)
	}
}

// TestPacingLineStaysSilent: an empty project and a document with nothing to
// measure must not print a line of zeros — "mean 0.0s" would read as a reel made
// of instant cuts, and a caller would chase a defect that is not there.
func TestPacingLineStaysSilent(t *testing.T) {
	for name, tl := range map[string]*timeline.Timeline{
		"nil":        nil,
		"no tracks":  {},
		"empty clip": pacingReel(),
	} {
		if got := pacingLine(tl); got != "" {
			t.Errorf("%s: wanted silence, got %q", name, got)
		}
	}
}

// TestPacingLineWithoutScores: a hand-edited document has no top shot, so the
// length clauses stay and the claim about which shot leads the reel goes.
func TestPacingLineWithoutScores(t *testing.T) {
	line := pacingLine(pacingReel(pacingShot(0, 4, ""), pacingShot(100, 102, "")))
	if !strings.Contains(line, "2 shots") || !strings.Contains(line, "mean 3.0s") {
		t.Errorf("pacing line %q lost the lengths it can measure", line)
	}
	if strings.Contains(line, "top shot") {
		t.Errorf("pacing line %q claims a top shot the document never scored", line)
	}
}
