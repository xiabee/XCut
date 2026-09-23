package api

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/pipeline"
	"github.com/xiabee/XCut/internal/subs"
)

// The one tap that ends with a post-ready reel used to answer "the project
// already has subtitles" from the mere existence of a file. That is the answer
// for a project that kept its shape — and a silent wrong turn for one that did
// not, because the default reel of the tap is vertical while the transcript that
// came before it may have been laid out for a horizontal one. The caption file
// carries the frame it was resolved against in its PlayRes pair, so the tap can
// say which of the two cases it is, in numbers.

// captionStyledFor writes the caption file this product's own writer writes, for
// a named frame: the bytes come from subs.WriteCaptionASS, so the PlayRes pair in
// them is the product's claim rather than a string this test invented.
func captionStyledFor(t *testing.T, s *Server, pid string, w, h int) string {
	t.Helper()
	tr := &subs.Transcript{Segments: []subs.Segment{{Start: 0.5, End: 2.5, Text: "hello there"}}}
	var b strings.Builder
	if err := subs.WriteCaptionASS(tr, subs.KaraokeStyle{Width: w, Height: h}, &b); err != nil {
		t.Fatal(err)
	}
	path, err := s.Pipe.SubtitlesPath(pid, "ass")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func declaredFrame(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	w, h, ok := subs.ReadASSFrame(bytes.NewReader(b))
	if !ok {
		t.Fatalf("%s declares no frame:\n%s", path, b)
	}
	return fmt.Sprintf("%dx%d", w, h)
}

// TestExportSaysWhenTheCaptionsBelongToAnotherFrame: no sidecar means the tap
// cannot fix the mismatch, so the honest answer is "reuse" — but reuse with the
// two frames named, not the sentence it shares with a project that fits.
func TestExportSaysWhenTheCaptionsBelongToAnotherFrame(t *testing.T) {
	s, pid := stagedReel(t, "export-canvas-stale")
	path := captionStyledFor(t, s, pid, 1280, 720)
	t.Setenv("PATH", t.TempDir()) // nothing resolves: no restyle is possible here
	s.Pipe.Cfg.Workers.AIBin = ""

	rec, out := do(t, s, "POST", "/api/v1/projects/"+pid+"/export", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("export: %d %v", rec.Code, out)
	}
	defer do(t, s, "POST", "/api/v1/jobs/"+out["job_id"].(string)+"/cancel", "")

	st := stepOf(t, out, "subtitles")
	if st["action"] != "reuse" {
		t.Fatalf("an unfixable mismatch answered %v; the file is still what will burn", st["action"])
	}
	reason := fmt.Sprint(st["reason"])
	if !strings.HasPrefix(reason, pipeline.ExportStaleSubtitles) {
		t.Errorf("reason %q does not start from the named stale answer %q",
			reason, pipeline.ExportStaleSubtitles)
	}
	// The numbers are the point: a person has to be able to tell which two
	// frames disagreed without opening either file.
	for _, want := range []string{"1280x720", "1080x1920"} {
		if !strings.Contains(reason, want) {
			t.Errorf("reason %q does not name %s", reason, want)
		}
	}
	if reason == pipeline.ExportReuseSubtitles {
		t.Error("a caption from another frame got the plain reuse sentence")
	}
	awaitJob(t, s, out["job_id"].(string))
	if got := declaredFrame(t, path); got != "1280x720" {
		t.Errorf("the file the tap could not restyle was rewritten as %s", got)
	}
}

// TestExportRestylesCaptionsForTheReelsOwnFrame: with a sidecar the mismatch is
// fixable, and the promise is a plan that says so plus an artifact that agrees.
func TestExportRestylesCaptionsForTheReelsOwnFrame(t *testing.T) {
	s, pid := stagedReel(t, "export-canvas-restyle")
	path := captionStyledFor(t, s, pid, 1280, 720)
	fakeSidecarScript(t, s, false)

	rec, out := do(t, s, "POST", "/api/v1/projects/"+pid+"/export", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("export: %d %v", rec.Code, out)
	}
	st := stepOf(t, out, "subtitles")
	if st["action"] != "create" {
		t.Fatalf("with a sidecar the tap can fix this, but it answered %v", st["action"])
	}
	reason := fmt.Sprint(st["reason"])
	if !strings.HasPrefix(reason, pipeline.ExportRestyleSubtitles) {
		t.Errorf("reason %q does not start from the named restyle answer %q",
			reason, pipeline.ExportRestyleSubtitles)
	}
	for _, want := range []string{"1280x720", "1080x1920"} {
		if !strings.Contains(reason, want) {
			t.Errorf("reason %q does not name %s", reason, want)
		}
	}
	awaitJob(t, s, out["job_id"].(string))
	if got := declaredFrame(t, path); got != "1080x1920" {
		t.Errorf("the tap promised the reel's own frame and left %s", got)
	}
}

// TestExportReusesCaptionsThatAlreadyFit is the other direction: a rule that
// always re-transcribed would pass the two tests above and quietly spend a
// model run on every tap. Fitting captions stay the bytes they were.
func TestExportReusesCaptionsThatAlreadyFit(t *testing.T) {
	s, pid := stagedReel(t, "export-canvas-fit")
	path := captionStyledFor(t, s, pid, 1080, 1920)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fakeSidecarScript(t, s, false)

	rec, out := do(t, s, "POST", "/api/v1/projects/"+pid+"/export", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("export: %d %v", rec.Code, out)
	}
	st := stepOf(t, out, "subtitles")
	if got := fmt.Sprint(st["reason"]); got != pipeline.ExportReuseSubtitles {
		t.Errorf("captions that fit the reel answered %q, want the plain reuse sentence", got)
	}
	awaitJob(t, s, out["job_id"].(string))
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("a caption file that already fits was rewritten by the tap")
	}
}

// TestExportSilentAboutASubtitleFileThatClaimsNoFrame: an SRT carries no
// geometry at all, so the tap has no evidence to act on and must not invent a
// mismatch — the plain sentence is the truthful one.
func TestExportSilentAboutASubtitleFileThatClaimsNoFrame(t *testing.T) {
	s, pid := stagedReel(t, "export-canvas-srt")
	srt, err := s.Pipe.SubtitlesPath(pid, "srt")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srt, []byte("1\n00:00:00,500 --> 00:00:02,500\nhello there\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	s.Pipe.Cfg.Workers.AIBin = ""

	rec, out := do(t, s, "POST", "/api/v1/projects/"+pid+"/export", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("export: %d %v", rec.Code, out)
	}
	defer do(t, s, "POST", "/api/v1/jobs/"+out["job_id"].(string)+"/cancel", "")

	if got := fmt.Sprint(stepOf(t, out, "subtitles")["reason"]); got != pipeline.ExportReuseSubtitles {
		t.Errorf("an artifact that claims no frame was reported as a mismatch: %q", got)
	}
}
