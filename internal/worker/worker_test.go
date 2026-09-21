package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
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
		// The listener is how the parent learns whether this process was reaped:
		// the kernel closes it with the process, and nothing here has to be
		// timed. Go's own exit hook cannot report a kill, because a killed
		// process runs no hooks.
		ln, lerr := net.Listen("tcp", "127.0.0.1:0")
		if lerr != nil {
			os.Stdout.WriteString(`{"protocol":1,"ok":false,"error":{"code":"listen","message":"no loopback"}}`)
			os.Exit(1)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		os.Stdout.WriteString(fmt.Sprintf(`{"protocol":1,"ok":true,"result":{"value":42,"port":%d}}`, port))
		// Close stdout so the reader sees a complete response, then keep
		// running — the shape of a sidecar stuck in a non-daemon thread.
		os.Stdout.Close()
		time.Sleep(10 * time.Minute)
	case "garbage":
		os.Stdout.WriteString("this is not a json envelope")
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

// TestCallReapsAWorkerThatNeverExits: a sidecar that answered, closed stdout
// and then hung (non-daemon threads, a stuck atexit) must not hold the call, and
// must not still be running once the call returns.
//
// Both halves are observed, not timed. The previous version subtracted a clean
// spawn from a hung one and bounded the difference at 5 s, which measured the
// machine rather than the product: the spawn re-executes this test binary, so
// its cost is whatever this package's own suite takes before the stub runs —
// 5.0 s idle, 10.9–22.7 s while other packages run in parallel — and a loaded
// gate put the difference at 5.77 s and failed. What is lost with that bound is
// the ability to notice the grace window itself growing; it is recorded here so
// the next reader does not reinstate the clock without knowing that.
func TestCallReapsAWorkerThatNeverExits(t *testing.T) {
	const deadline = 60 * time.Second
	bin := stubWorkerCmd(t, "answer-then-hang")
	start := time.Now()
	raw, err := CallWithTimeout(context.Background(), bin, Request{Protocol: Protocol, Op: "x"}, deadline)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("an answered call reported an error (held open past the answer?): %v", err)
	}
	var got struct {
		Value int `json:"value"`
		Port  int `json:"port"`
	}
	if uerr := json.Unmarshal(raw, &got); uerr != nil || got.Value != 42 {
		t.Fatalf("result = %s (%v), want value 42", raw, uerr)
	}
	t.Logf("call returned in %s with the answer; grace is %s, deadline %s", elapsed, workerExitGrace, deadline)
	if got.Port <= 0 {
		t.Fatalf("the hung worker reported no liveness port in %s", raw)
	}
	addr := fmt.Sprintf("127.0.0.1:%d", got.Port)

	// The control runs first and in the direction that can make the check below
	// meaningless: if a live loopback listener cannot be dialled here, then
	// "cannot dial the worker's port" proves nothing about the worker.
	hold, herr := net.Listen("tcp", "127.0.0.1:0")
	if herr != nil {
		t.Skipf("no loopback listener on this host: %v", herr)
	}
	defer hold.Close()
	c, derr := net.Dial("tcp", hold.Addr().String())
	if derr != nil {
		t.Skipf("a live loopback listener cannot be dialled here (%v); the reap check below cannot distinguish reaped from unreachable", derr)
	}
	_ = c.Close()

	if c2, err2 := net.Dial("tcp", addr); err2 == nil {
		_ = c2.Close()
		t.Fatalf("the hung worker still answers on %s after Call returned: it was left running", addr)
	}
}

// TestUnparseableAnswerNamesTheWorker: a misconfigured workers.ai_bin — the
// interpreter where the sidecar script belongs, say — fails exactly here, and
// an error that only says "unparseable" leaves nothing to act on. This hit me
// while running the documented recipe with the wrong environment variable.
func TestUnparseableAnswerNamesTheWorker(t *testing.T) {
	bin := stubWorkerCmd(t, "garbage")
	_, err := CallWithTimeout(context.Background(), bin, Request{Protocol: Protocol, Op: "x"}, 60*time.Second)
	if err == nil {
		t.Fatal("a non-JSON answer was accepted")
	}
	msg := err.Error()
	if !strings.Contains(msg, "unparseable") {
		t.Fatalf("error lost its meaning: %s", msg)
	}
	if !strings.Contains(msg, bin) {
		t.Fatalf("error does not say which worker was run: %s", msg)
	}
}
