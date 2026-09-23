package cli

import (
	"runtime"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/media"
)

// The sandbox posture is what an operator checks before trusting a busy
// machine to a render queue: doctor must always name it, whatever the knob is
// set to (its default has been 1536 MB since D16, and `0` is the explicit
// opt-out). Other checks may fail around it — this asserts the line's presence
// and platform-correct wording only.
func TestDoctorReportsProcessSandbox(t *testing.T) {
	testWorkspace(t)
	_, out, _ := runCapture(t, "doctor")
	if !strings.Contains(out, "Process sandbox") {
		t.Fatal("doctor lost the process-sandbox posture line")
	}
	if runtime.GOOS == "windows" {
		if !strings.Contains(out, "kill-on-close") {
			t.Error("windows doctor must state the kill-on-close backstop")
		}
		if !strings.Contains(out, "ffmpeg_max_memory_mb") {
			t.Error("windows doctor must name the knob that bounds a runaway encoder")
		}
	}
	if runtime.GOOS == "linux" && media.SandboxArmed() {
		// The row must name the property it armed, because that is the only number
		// an operator can compare against the config. Whether systemd *attached* it
		// is SandboxEnforcement's question, and its answer — the byte count, or the
		// manager's own "infinity" — is asserted in internal/media, where a host that
		// declines to answer can skip honestly instead of quietly passing.
		line := ""
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, "Process sandbox") {
				line = l
			}
		}
		if !strings.Contains(line, "MemoryMax") {
			t.Errorf("the linux row must name the systemd property it arms, got %q", line)
		}
	}
}
