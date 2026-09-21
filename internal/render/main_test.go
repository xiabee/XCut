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
