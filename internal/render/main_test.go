package render

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestMain doubles as a stand-in FFmpeg when XCUT_FAKE_FFMPEG is set: the render
// package's own binary is then the child process, which is how the rest of this
// repository drives a failing external tool without a shell script or a
// platform-specific stub (see internal/media/limit_test.go).
//
// It has to be TestMain because the real caller decides the argument list — the
// child is invoked as `<this binary> -y -i …`, which the testing flag parser
// would reject before any test ran.
func TestMain(m *testing.M) {
	if os.Getenv("XCUT_FAKE_FFMPEG") == "1" {
		// A test that wants the command line, not the failure, names a file for
		// it: the arguments the product assembled are the thing under test, and
		// reading them back from the child is the only way to see them without
		// handing production a seam.
		if p := os.Getenv("XCUT_FAKE_FFMPEG_ARGV"); p != "" {
			if f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
				fmt.Fprintln(f, strings.Join(os.Args[1:], " "))
				f.Close()
			}
		}
		// XCUT_FAKE_FFMPEG_CREATE names a file the child creates before
		// failing: the "died mid-encode with the output already opened"
		// case whose litter the cleanup arms must answer for.
		if p := os.Getenv("XCUT_FAKE_FFMPEG_CREATE"); p != "" {
			_ = os.WriteFile(p, []byte("partial bytes"), 0o644)
		}
		var flood strings.Builder
		flood.WriteString("START-MARKER ")
		for flood.Len() < 3000 {
			fmt.Fprint(&flood, "diagnostic noise ")
		}
		flood.WriteString(" END-MARKER")
		fmt.Fprint(os.Stderr, flood.String())
		os.Exit(1)
	}
	os.Exit(m.Run())
}
