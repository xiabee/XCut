package render

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/xcerr"
)

// TestFFmpegFailureKeepsTheEndOfTheDiagnostics covers the branch no test had
// reached: FFmpeg itself exiting non-zero mid-render. What users and logs get is
// `ffmpeg failed` plus the tail of the child's output, so two things are worth
// pinning — the tail is the END (ffmpeg prints its summary last), and it stays
// bounded (an unbounded error string reaches a UI toast).
func TestFFmpegFailureKeepsTheEndOfTheDiagnostics(t *testing.T) {
	tools := requireTools(t)
	src := fixture(t)
	// The child is this test binary; TestMain turns it into a chatty FFmpeg.
	tools.FFmpeg = os.Args[0]
	t.Setenv("XCUT_FAKE_FFMPEG", "1")

	out := filepath.Join(t.TempDir(), "out.mp4")
	err := Render(context.Background(), twoClipTimeline(src), Options{Tools: tools, TempDir: t.TempDir()}, out)
	if err == nil {
		t.Fatal("a failing ffmpeg child must fail the render")
	}
	if code := xcerr.CodeOf(err); code != xcerr.CodeRenderFailure {
		t.Fatalf("error code = %s, want render failure (chain: %.200v)", code, err)
	}
	if um := xcerr.UserMessage(err); um != "ffmpeg failed" {
		t.Fatalf("user-facing message = %q, want the plain \"ffmpeg failed\" (the diagnostic belongs in the cause)", um)
	}

	full := err.Error()
	if !strings.Contains(full, "END-MARKER") {
		t.Fatalf("diagnostic must carry the end of the child's output, got: %.200s", full)
	}
	if strings.Contains(full, "START-MARKER") {
		t.Fatalf("diagnostic kept the child's whole output, not its tail (%d bytes)", len(full))
	}
	if len(full) > 800 {
		t.Fatalf("error text grew to %d bytes, so the 500-byte tail budget is not holding", len(full))
	}
	if _, serr := os.Stat(out); !os.IsNotExist(serr) {
		t.Fatal("a failed render must not leave a final file behind")
	}
}
