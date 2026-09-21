package worker

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/xiabee/XCut/internal/xcerr"
)

// requireWorker finds a built worker binary: repo-relative crate target first
// (works on any dev machine that ran `cargo build` once), then PATH.
func requireWorker(t *testing.T) string {
	t.Helper()
	if p, ok := crateWorkerBin(); ok {
		return p
	}
	bin := ResolveBin("")
	if bin == "" {
		t.Skip("xcut-worker-media not built (cargo build in crates/xcut-worker-media)")
	}
	return bin
}

func crateWorkerBin() (string, bool) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", false
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	exe := "xcut-worker-media"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	for _, profile := range []string{"debug", "release"} {
		p := filepath.Join(root, "crates", "xcut-worker-media", "target", profile, exe)
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}
	return "", false
}

func TestDescribe(t *testing.T) {
	bin := requireWorker(t)
	d, err := Probe(context.Background(), bin)
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "xcut-worker-media" || d.Protocol != Protocol {
		t.Fatalf("describe: %+v", d)
	}
	found := false
	for _, op := range d.Ops {
		if op == "audio_rms" {
			found = true
		}
	}
	if !found {
		t.Fatalf("audio_rms op missing: %v", d.Ops)
	}
}

func TestStructuredErrorSurfaces(t *testing.T) {
	bin := requireWorker(t)
	_, err := Call(context.Background(), bin, Request{
		Protocol: Protocol,
		Op:       "audio_rms",
		Input:    "definitely-missing-file.mp3",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if got := xcerr.CodeOf(err); got != xcerr.CodeAnalyzerFailure {
		t.Fatalf("code = %s", got)
	}
	if msg := err.Error(); msg == "" {
		t.Fatal("empty error message")
	}
}

func TestUnknownOpFails(t *testing.T) {
	bin := requireWorker(t)
	_, err := Call(context.Background(), bin, Request{Protocol: Protocol, Op: "bogus"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestHelperWorkerStub is re-executed by the stub tests below (same pattern
// as the media package): it answers one protocol envelope on stdout, then
// either exits, hangs forever, or sleeps long past any test budget —
// selected by XCUT_TEST_WORKER_STUB.
func TestHelperWorkerStub(t *testing.T) {
	mode := os.Getenv("XCUT_TEST_WORKER_STUB")
	if mode == "" {
		return
	}
	switch mode {
	case "answer":
		// Result content proves the parsed envelope is the real answer.
		os.Stdout.WriteString(`{"protocol":1,"ok":true,"result":{"value":42}}`)
	case "answer-then-hang":
		os.Stdout.WriteString(`{"protocol":1,"ok":true,"result":{"value":42}}`)
		// Close stdout so the reader sees a complete response, then keep
		// running — the shape of a sidecar stuck in a non-daemon thread.
		os.Stdout.Close()
		time.Sleep(10 * time.Minute)
	case "never-answer":
		time.Sleep(10 * time.Minute)
	}
	os.Exit(0)
}

func stubWorkerCmd(t *testing.T, mode string) string {
	t.Helper()
	t.Setenv("XCUT_TEST_WORKER_STUB", mode)
	return os.Args[0]
}

// TestCallWithExplicitTimeout: the explicit deadline parameter governs the
// call — a worker that never answers is cut off by a short budget instead
// of the built-in 10-minute default.
func TestCallWithExplicitTimeout(t *testing.T) {
	bin := stubWorkerCmd(t, "never-answer")
	start := time.Now()
	_, err := CallWithTimeout(context.Background(), bin, Request{Protocol: Protocol, Op: "x"}, 500*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("timeout took %s, want ~500ms", elapsed)
	}
}

// TestCallReturnsBeforeWorkerExits: a sidecar that answered and closed
// stdout but never exits (non-daemon threads, atexit hangs) must not hold
// the call until the deadline — the answer is returned after the grace
// window and the process is reaped.
func TestCallReturnsBeforeWorkerExits(t *testing.T) {
	// The property is "an answered call is not held open by a worker that never
	// exits". Asserting it against a wall clock is a load detector: the cost of
	// this test's own harness — re-executing the test binary as the worker —
	// measured 5.0 s idle and 7.7–22.7 s while the suite runs other packages in
	// parallel, several times the 2 s grace it was meant to watch.
	//
	// So the same spawn is measured twice and subtracted: a clean-exit stub and a
	// hung stub, identical but for what happens after the answer. Whatever the
	// machine costs to start a process, it costs both of them the same.
	const deadline = 60 * time.Second
	call := func(mode string) time.Duration {
		bin := stubWorkerCmd(t, mode)
		start := time.Now()
		raw, err := CallWithTimeout(context.Background(), bin, Request{Protocol: Protocol, Op: "x"}, deadline)
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("%s worker: %v", mode, err)
		}
		if !strings.Contains(string(raw), "42") {
			t.Fatalf("%s worker result: %s", mode, raw)
		}
		return elapsed
	}

	clean := call("answer")
	hung := call("answer-then-hang")
	t.Logf("clean-exit %s, hung %s, difference %s (grace %s)", clean, hung, hung-clean, workerExitGrace)
	// The hung worker must cost little more than the clean one: the answer is
	// followed by a grace window of 2 s and then a kill. Load inflates both
	// numbers together; only a regression that waits on the process (or on the
	// deadline) separates them — verified by setting workerExitGrace to 40 s,
	// which pushes the difference to ~35 s and fails here.
	if d := hung - clean; d > 5*time.Second {
		t.Fatalf("a hung worker held the call %.3fs longer than a clean one; want <5s (grace is %s)", d.Seconds(), workerExitGrace)
	}
}
