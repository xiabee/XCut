//go:build unix

package media

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestHelperPipeHolder is the re-exec'd child of the two cases below. It hands the
// descriptor it was given (stdout/stderr, inherited from the caller) to a grandchild and
// exits itself — the shape of a tool that launches a helper (a decoder plugin, a sidecar's
// worker) and goes away while that helper still holds the pipe open.
//
// The grandchild sleeps for a minute. That number is not a tuning knob: it only has to be
// far longer than the call's deadline, so *how long the call takes* separates "the budget
// was enforced on the descriptors too" from "the budget was a suggestion".
func TestHelperPipeHolder(t *testing.T) {
	if payload := os.Getenv("XCUT_TEST_JSON_THEN_HOLD"); payload == "1" {
		body := os.Getenv("XCUT_TEST_JSON_PAYLOAD")
		os.Stdout.WriteString(body)
		sleeper, err := exec.LookPath("sleep")
		if err != nil {
			os.Exit(3)
		}
		hold := exec.Command(sleeper, "60")
		hold.Stdout = os.Stdout
		hold.Stderr = os.Stderr
		if err := hold.Start(); err != nil {
			os.Exit(5)
		}
		os.Exit(0)
	}
	if os.Getenv("XCUT_TEST_PIPE_HOLDER") != "1" {
		return
	}
	sleeper, err := exec.LookPath("sleep")
	if err != nil {
		os.Exit(3)
	}
	helper := exec.Command(sleeper, "60")
	helper.Stdout = os.Stdout
	helper.Stderr = os.Stderr
	if err := helper.Start(); err != nil {
		os.Exit(4)
	}
	os.Exit(0)
}

// helperCmd points the re-exec at the holder and turns the pipe holder on by env.
func helperCmd(t *testing.T) string {
	t.Helper()
	t.Setenv("XCUT_TEST_PIPE_HOLDER", "1")
	return os.Args[0]
}

const holderArgs = "-test.run=TestHelperPipeHolder$"

// TestCancelledCallDoesNotWaitForAStrayPipeHolder: the per-call deadline has to bound the
// descriptors, not only the process. Before cmd.WaitDelay was set, this case took 60.01 s
// against a 1 s deadline and — worse than slow — returned **success** once the grandchild
// finally let go, so a cancelled probe read as a completed one.
func TestCancelledCallDoesNotWaitForAStrayPipeHolder(t *testing.T) {
	bin := helperCmd(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	start := time.Now()
	_, _, err := Run(ctx, bin, holderArgs, "-test.v=false")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("the call reported success although its deadline passed (after %v)", elapsed)
	}
	// 10 s is not a tolerance to fit: without a pipe deadline the call cannot return
	// before the grandchild's own minute, so anything under that proves the cancel was
	// enforced on the descriptors and not only on the process.
	if elapsed > 10*time.Second {
		t.Errorf("Run waited %v after a 1s deadline: a process this call never started held "+
			"stdout open, and exec's default waits for the pipe to drain", elapsed)
	}
}

// TestStreamedCallDoesNotWaitForAStrayPipeHolder is the same claim on the path that reads
// ffprobe's JSON. It failed the same way (60.01 s) before the fix.
func TestStreamedCallDoesNotWaitForAStrayPipeHolder(t *testing.T) {
	bin := helperCmd(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	start := time.Now()
	err := StreamStdout(ctx, bin, func(chunk []byte) error { return nil }, holderArgs, "-test.v=false")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("the streamed call reported success although its deadline passed (after %v)", elapsed)
	}
	if elapsed > 10*time.Second {
		t.Errorf("StreamStdout waited %v after a 1s deadline: want the pipe closed with the "+
			"cancel, not drained until an unrelated process lets go", elapsed)
	}
}

// RunCombined and Version carry the same WaitDelay and are not driven here: the two cases
// above are the same hazard in the same package (both capture into a buffer the caller
// reads after Wait), so they prove the mechanism; the other two sites follow by
// construction rather than by measurement, and that distinction is recorded in
// docs/PROJECT_STATE.md rather than implied by four green lines.

// TestProbeRecoversCompleteJSONFromWaitDelay stages the campaign strike
// (night 2026-10-02, TestE2EAutoWithProxy): a child writes its full JSON
// answer, hands the pipe to a longer-lived grandchild and exits — so Run's
// WaitDelay fires while the answer sits complete in the captured buffer. The
// recovery decision must call that answer complete; and a TRUNCATED payload
// through the same mechanism must fail closed.
func TestProbeRecoversCompleteJSONFromWaitDelay(t *testing.T) {
	full := `{"streams":[{"index":0,"codec_type":"video","codec_name":"h264","width":640,"height":360}],"format":{"format_name":"mov,mp4","duration":"3.000000"}}`
	truncated := `{"streams":[{"index":0,"codec_type":"vi`
	for name, payload := range map[string]struct {
		body   string
		wantOK bool
	}{
		"complete":  {full, true},
		"truncated": {truncated, false},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			out, _, err := Run(ctx, jsonThenHoldCmd(t, payload.body), holderArgs, "-test.v=false")
			if !errors.Is(err, exec.ErrWaitDelay) {
				t.Fatalf("expected ErrWaitDelay from a pipe held past the grace, got %v", err)
			}
			if got := probeWaitDelayRecoverable(out); got != payload.wantOK {
				t.Fatalf("recoverable=%v, want %v (out=%q)", got, payload.wantOK, out)
			}
		})
	}
}

// jsonThenHoldCmd re-execs the test binary as a child that writes `payload` to
// stdout, hands that descriptor to a sleeping grandchild and exits — the
// descriptor outlives the writer, which is what fires WaitDelay with the
// payload already delivered.
func jsonThenHoldCmd(t *testing.T, payload string) string {
	t.Helper()
	t.Setenv("XCUT_TEST_JSON_THEN_HOLD", "1")
	t.Setenv("XCUT_TEST_JSON_PAYLOAD", payload)
	return os.Args[0]
}
