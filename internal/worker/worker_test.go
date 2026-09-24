package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
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
	case "fail-with-diagnostics":
		// A structured envelope with no error detail, exit 1, and 600 bytes of
		// stderr: what a real sidecar crash looks like, and the only shape that
		// reaches the worker-failure message's stderr tail.
		var flood strings.Builder
		flood.WriteString("HEAD-MARKER ")
		for flood.Len() < 600 {
			flood.WriteString("diagnostic noise ")
		}
		flood.WriteString(" END-MARKER")
		os.Stderr.WriteString(flood.String())
		os.Stdout.WriteString(`{"protocol":1,"ok":false}`)
		os.Exit(1)
	case "flood":
		// A worker that answers more than the budget may never stop answering: the
		// reader has to abort it rather than buffer it. The answer is refused, so the
		// port cannot come back through stdout — it is published to the side channel
		// the parent named, and the kernel closes that listener when the process dies.
		if p := os.Getenv("XCUT_TEST_WORKER_PORTFILE"); p != "" {
			ln, lerr := net.Listen("tcp", "127.0.0.1:0")
			if lerr == nil {
				defer ln.Close()
				port := ln.Addr().(*net.TCPAddr).Port
				if err := os.WriteFile(p, []byte(fmt.Sprint(port)), 0o600); err != nil {
					return
				}
			}
		}
		os.Stdout.WriteString(`{"protocol":1,"ok":true,"result":{"pad":"`)
		for n := 0; n < 8192; n++ {
			os.Stdout.WriteString("0123456789abcdef") // 128 KiB, well past any test budget
		}
		os.Stdout.WriteString(`"}}`)
		// Close the answer, keep the process: stdout EOF means the reader can decide
		// on the byte budget alone, while a live process is still what the kill check
		// below can see. Without this line the verdict waits on the pipe, and the
		// pipe waits on whatever the machine takes to spawn this binary — a deadline
		// of any size is then a coin-flip, not a check.
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

// TestFailedWorkerCarriesTheEndOfItsStderr covers the branch where a worker
// exits non-zero with a structured-but-useless envelope: what an operator needs
// is the last thing the sidecar printed, not its first line, and not all 600
// bytes of it either.
func TestFailedWorkerCarriesTheEndOfItsStderr(t *testing.T) {
	bin := stubWorkerCmd(t, "fail-with-diagnostics")
	_, err := CallWithTimeout(context.Background(), bin, Request{Protocol: Protocol, Op: "x"}, 60*time.Second)
	if err == nil {
		t.Fatal("a worker that exits 1 was accepted")
	}
	if !xcerr.IsCode(err, xcerr.CodeAnalyzerFailure) {
		t.Fatalf("code = %s, want analyzer_failure (%v)", xcerr.CodeOf(err), err)
	}
	chain := err.Error()
	if !strings.Contains(chain, "END-MARKER") {
		t.Fatalf("worker failure lost the tail of its stderr: %.200s", chain)
	}
	if strings.Contains(chain, "HEAD-MARKER") {
		t.Fatalf("worker failure carried the whole stderr, not the tail (%d bytes)", len(chain))
	}
}

// TestOversizedResponseIsRefusedAndTheWorkerKilled: the response budget is a refusal,
// not a buffer. A worker that keeps writing must be aborted mid-answer — "nothing
// unbounded" (AGENTS.md rule 4) otherwise holds only for children that behave.
func TestOversizedResponseIsRefusedAndTheWorkerKilled(t *testing.T) {
	bin := stubWorkerCmd(t, "flood")
	portFile := filepath.Join(t.TempDir(), "port")
	t.Setenv("XCUT_TEST_WORKER_PORTFILE", portFile)

	const budget = 4096
	// The product's own default deadline: what is under test here is the byte
	// budget, and a shorter one would put the machine's spawn time in the
	// verdict instead.
	_, err := callBounded(context.Background(), bin, Request{Protocol: Protocol, Op: "describe"},
		10*time.Minute, budget)
	if err == nil {
		t.Fatal("a 128 KiB response was accepted under a 4 KiB budget")
	}
	if code := xcerr.CodeOf(err); code != xcerr.CodeResourceLimit {
		t.Fatalf("an oversized response reported %s, want resource_limit: %v", code, err)
	}
	msg := err.Error()
	if !strings.Contains(msg, fmt.Sprintf("worker response exceeded %d bytes", budget)) {
		t.Errorf("message = %q, want it to name the budget that was hit", msg)
	}
	// The typed cause has to survive the wrap: a caller distinguishing "too big" from
	// "cannot read" branches on it, and xcerr prints the chain into logs — dropping it
	// would lose the reason in both places that matter.
	var tooLarge errResponseTooLarge
	if !errors.As(err, &tooLarge) {
		t.Fatalf("the refusal lost its cause: %v", err)
	}
	if tooLarge.max != budget {
		t.Errorf("the cause carries max=%d, want the %d bytes actually enforced", tooLarge.max, budget)
	}
	if !strings.Contains(msg, "response exceeds") {
		t.Errorf("message = %q, want the cause's own words in the chain", msg)
	}

	// Aborted, not abandoned: the kernel closes the stub's listener with the process,
	// so a port that still answers says the worker is alive and holding the pipe.
	data, readErr := os.ReadFile(portFile)
	if readErr != nil {
		t.Skipf("the stub could not publish a port to check (%v)", readErr)
	}
	port, convErr := strconv.Atoi(strings.TrimSpace(string(data)))
	if convErr != nil {
		t.Fatalf("port file holds %q", data)
	}
	conn, dialErr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
	if dialErr == nil {
		_ = conn.Close()
		t.Error("the oversized worker is still alive: the answer was refused but the process was not aborted")
	}
	// The *kind* is the claim: a timeout is also an error, and it would say nothing
	// about whether the process is gone. Only a refused connection does.
	if !strings.Contains(dialErr.Error(), "refused") {
		t.Errorf("dial of the killed worker's port returned %v, want a refused connection", dialErr)
	}
}
