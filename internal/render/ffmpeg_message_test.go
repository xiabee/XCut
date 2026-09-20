package render

import (
	"strings"
	"testing"
)

// Captured from a real failure: Kylin V10 SP1 ships FFmpeg 4.2.2 built without
// the xfade filter, so a valid timeline fails there for a reason the user can
// only act on if the message says what is missing.
const kylinXfadeStderr = `Stream mapping:
  Stream #0:0 -> #0:0 (h264 (native) -> h264 (libx264))
[AVFilterGraph @ 0x55ba5c1e10] No such filter: 'xfade'
Error initializing complex filters.
Invalid argument
`

func TestFFmpegFailureMessageNamesMissingFilter(t *testing.T) {
	msg := ffmpegFailureMessage([]byte(kylinXfadeStderr))
	if !strings.Contains(msg, "'xfade'") {
		t.Fatalf("message must name the missing filter, got %q", msg)
	}
	for _, want := range []string{"FFmpeg build", "xcut doctor"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message should mention %q: %q", want, msg)
		}
	}
	// The remedy must be reachable without reading source: a style that works.
	if !strings.Contains(msg, "generic_highlight") {
		t.Errorf("message should name a style that needs no transitions: %q", msg)
	}
}

// Everything else keeps the previous generic wording — a special case that
// swallows unrelated failures is worse than no special case.
func TestFFmpegFailureMessageStaysGenericOtherwise(t *testing.T) {
	for _, out := range []string{
		"",
		"Invalid argument\n",
		"[libx264 @ 0x1] encoded 0 frames\n",
		// A line that merely mentions the words must not be mistaken for the
		// filter error ffmpeg actually prints.
		"filter: xfade not applied because input is short\n",
	} {
		if got := ffmpegFailureMessage([]byte(out)); got != "ffmpeg failed" {
			t.Errorf("ffmpegFailureMessage(%q) = %q, want the generic message", out, got)
		}
	}
}
