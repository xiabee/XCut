package api

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/subs"
)

// TestSubtitlesStatusNamesBothFramesAndRestyleFixesThem is the caption mismatch made
// visible and fixable without a render. The export tap already restyles on its own
// second; what the panel could not do before was *say* that the styled file is sized
// for another frame, or act on it without cutting a reel.
//
// The .ass is put out of shape by the product's own writer (not by editing bytes), and
// the sidecar is then taken away entirely: if the restyle reached for a transcription
// it would fail here rather than pass on a machine that happens to have one.
func TestSubtitlesStatusNamesBothFramesAndRestyleFixesThem(t *testing.T) {
	s, pid := stagedReel(t, "restyled")

	// Before any transcription: the reel claims a frame, the captions claim nothing.
	rec, out := do(t, s, "GET", "/api/v1/projects/"+pid+"/subtitles", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status before transcribe: %d", rec.Code)
	}
	if out["styled_frame"] != "" || out["transcript"] != false || out["mismatch"] != false {
		t.Fatalf("a project with no captions reported %+v, want no styled frame and no transcript", out)
	}
	if out["reel_frame"] != "1080x1920" {
		t.Fatalf("the staged reel is %v, not the 1080x1920 this test restyles back to", out["reel_frame"])
	}

	fakeSidecarScript(t, s, false)
	rec, out = do(t, s, "POST", "/api/v1/projects/"+pid+"/subtitles", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("transcribe trigger: %d %v", rec.Code, out)
	}
	awaitJob(t, s, out["job_id"].(string))

	rec, out = do(t, s, "GET", "/api/v1/projects/"+pid+"/subtitles", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status after transcribe: %d", rec.Code)
	}
	if out["styled_frame"] != "1080x1920" || out["mismatch"] != false || out["transcript"] != true {
		t.Fatalf("fresh captions read %v / %v / %v, want them agreeing with the reel and a transcript on disk",
			out["styled_frame"], out["mismatch"], out["transcript"])
	}

	// Now the same words laid out for a horizontal frame, which is what a project
	// that switched style is left holding.
	tr, err := subs.Parse([]byte(`{"language":"zh","segments":[{"start":0.5,"end":1.5,"text":"你好"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var wrong strings.Builder
	if err := subs.WriteCaptionASS(tr, subs.KaraokeStyle{Width: 1920, Height: 1080}, &wrong); err != nil {
		t.Fatal(err)
	}
	assPath, err := s.Pipe.SubtitlesPath(pid, "ass")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(assPath, []byte(wrong.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, out = do(t, s, "GET", "/api/v1/projects/"+pid+"/subtitles", "")
	if out["mismatch"] != true || out["styled_frame"] != "1920x1080" {
		t.Fatalf("a .ass sized for another frame read %v / %v, want mismatch true and 1920x1080",
			out["styled_frame"], out["mismatch"])
	}

	s.Pipe.Cfg.Workers.AIBin = "" // no sidecar resolves from here on
	rec, out = do(t, s, "POST", "/api/v1/projects/"+pid+"/subtitles/restyle", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("restyle with no sidecar: %d %v", rec.Code, out)
	}
	if out["styled_frame"] != "1080x1920" || out["mismatch"] != false {
		t.Fatalf("the restyle answered %v / %v, want the reel's own frame and no mismatch",
			out["styled_frame"], out["mismatch"])
	}
	// The body of the file, not just the report: a status that agreed with itself and
	// a caption file that did not would be the same bug wearing a better message.
	rec, _ = do(t, s, "GET", "/api/v1/projects/"+pid+"/subtitles/file?format=ass", "")
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "PlayResX: 1080") ||
		!strings.Contains(body, "PlayResY: 1920") {
		t.Fatalf("the restyled file is not what the endpoint reported (%d): %s", rec.Code, body)
	}
	if !strings.Contains(body, "你好") {
		t.Errorf("the restyle lost the words it re-laid out: %s", body)
	}
}

// TestRestyleRefusesWithoutATranscript is the endpoint's own negative: with no words on
// disk there is nothing to lay out, and the answer has to name what is missing rather
// than 200 its way to an unchanged file. A project that was never transcribed is the
// same path as one whose transcript was deleted, and it is the one a user actually
// reaches.
func TestRestyleRefusesWithoutATranscript(t *testing.T) {
	s, pid := stagedReel(t, "norestyle")
	rec, out := do(t, s, "POST", "/api/v1/projects/"+pid+"/subtitles/restyle", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a restyle with no transcript answered %d %v, want 404", rec.Code, out)
	}
	if out["error"] != "not_found" {
		t.Errorf("code %v, want not_found", out["error"])
	}
	msg, _ := out["message"].(string)
	if !strings.Contains(strings.ToLower(msg), "transcript") {
		t.Errorf("the refusal says %q, want it to name the missing transcript", msg)
	}
	// And it refused without producing the thing it could not lay out.
	assPath, err := s.Pipe.SubtitlesPath(pid, "ass")
	if err != nil {
		t.Fatal(err)
	}
	if _, serr := os.Stat(assPath); serr == nil {
		t.Error("a refused restyle still wrote an .ass")
	}
}
