package cli

import (
	"runtime"
	"strings"
	"testing"
)

// The sandbox posture is what an operator checks before trusting a busy
// machine to a render queue: doctor must always name it, in both the capped
// and the (default) uncapped state. Other checks may fail around it — this
// asserts the line's presence and platform-correct wording only.
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
}
