// Package worker is the Go-side client for XCut helper worker processes
// (Rust today, optional AI sidecars later). Protocol: one JSON request on
// stdin, one JSON response on stdout (DECISIONS D2).
//
// Workers are strictly optional: every failure maps to a plain error and the
// caller falls back (or degrades) — a missing/broken worker never takes the
// core down.
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/xiabee/XCut/internal/xcerr"
)

// Protocol is the current worker protocol version.
const Protocol = 1

// Request is one worker invocation.
type Request struct {
	Protocol int            `json:"protocol"`
	Op       string         `json:"op"`
	Input    string         `json:"input,omitempty"`
	Params   map[string]any `json:"params,omitempty"`
}

// response mirrors the worker's response envelope.
type response struct {
	Protocol int             `json:"protocol"`
	OK       bool            `json:"ok"`
	Op       string          `json:"op"`
	Result   json.RawMessage `json:"result"`
	Error    *responseError  `json:"error"`
}

type responseError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Describe is the `describe` op result.
type Describe struct {
	Name     string   `json:"name"`
	Version  string   `json:"version"`
	Protocol int      `json:"protocol"`
	Ops      []string `json:"ops"`
}

// ResolveBin returns the configured binary or looks the default name up on
// PATH. Returns "" when unavailable.
func ResolveBin(configured string) string {
	if configured != "" {
		if p, err := exec.LookPath(configured); err == nil {
			return p
		}
		return configured // explicit config wins; Call will surface errors
	}
	if p, err := exec.LookPath("xcut-worker-media"); err == nil {
		return p
	}
	return ""
}

// Probe runs `describe` against a worker binary.
func Probe(ctx context.Context, bin string) (*Describe, error) {
	raw, err := Call(ctx, bin, Request{Protocol: Protocol, Op: "describe"})
	if err != nil {
		return nil, err
	}
	var d Describe
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, xcerr.E(xcerr.CodeInternal, "worker describe unparseable", err)
	}
	return &d, nil
}

// Call executes one request against the worker binary with the default
// timeout. The worker receives the request on stdin and answers once on
// stdout.
func Call(ctx context.Context, bin string, req Request) (json.RawMessage, error) {
	return CallWithTimeout(ctx, bin, req, 10*time.Minute)
}

// CallWithTimeout executes one request with an explicit deadline. Analyzer
// paths must pass their configured per-call budget
// (resource.analyzer_call_timeout) here — the 10-minute default would
// otherwise silently override a larger configured budget.
func CallWithTimeout(ctx context.Context, bin string, req Request, timeout time.Duration) (json.RawMessage, error) {
	return callBounded(ctx, bin, req, timeout, MaxResponseBytes)
}

// MaxResponseBytes caps one worker response (worker output security: a
// worker must never be able to OOM the host through its stdout).
const MaxResponseBytes = 32 << 20

// maxStderrBytes caps captured worker stderr for error reporting.
const maxStderrBytes = 64 << 10

// workerExitGrace is how long a worker that already answered may take to
// exit before it is killed. Real workers exit in milliseconds; the grace
// only exists so a valid answer is not discarded over startup jitter.
const workerExitGrace = 2 * time.Second

// callBounded executes one request with an explicit deadline and response
// caps. The response is streamed with a hard byte limit — oversized output
// aborts the worker instead of exhausting memory.
func callBounded(ctx context.Context, bin string, req Request, timeout time.Duration, maxResp int64) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	payload, err := json.Marshal(req)
	if err != nil {
		return nil, xcerr.E(xcerr.CodeInternal, "cannot encode worker request", err)
	}

	cmd := buildWorkerCmd(ctx, bin)
	cmd.Stderr = &limitedBuffer{max: maxStderrBytes}
	cmd.Stdin = bytes.NewReader(payload)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, xcerr.E(xcerr.CodeInternal, "cannot create worker pipe", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, xcerr.E(xcerr.CodeAnalyzerFailure, "cannot start worker "+bin, err)
	}

	raw, readErr := io.ReadAll(io.LimitReader(stdout, maxResp+1))
	if readErr == nil && int64(len(raw)) > maxResp {
		readErr = errResponseTooLarge{max: maxResp}
	}
	if readErr != nil {
		// Abort the worker immediately: a worker blocked writing into a full
		// pipe would otherwise stall until the deadline.
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
	// The response is complete once stdout hits EOF. A sidecar that answered
	// but does not exit (non-daemon threads, atexit hangs) must not hold the
	// call until the deadline — wait a short grace window, then kill and reap.
	var waitErr error
	if readErr != nil {
		waitErr = cmd.Wait()
	} else {
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case waitErr = <-done:
		case <-time.After(workerExitGrace):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			waitErr = <-done
		}
	}
	if readErr != nil {
		var tooLarge errResponseTooLarge
		if errors.As(readErr, &tooLarge) {
			return nil, xcerr.E(xcerr.CodeResourceLimit,
				fmt.Sprintf("worker response exceeded %d bytes (refusing to buffer it)", maxResp), nil)
		}
		return nil, xcerr.E(xcerr.CodeAnalyzerFailure, "cannot read worker response", readErr)
	}
	resp, parseErr := parseResponse(raw)
	if waitErr != nil {
		// Workers answer with a structured envelope even on failure (exit 1);
		// prefer that over the bare exec error. A worker killed for not
		// exiting after a complete answer still counts as answered.
		if parseErr == nil && resp.Error != nil && !resp.OK {
			return nil, xcerr.E(xcerr.CodeAnalyzerFailure,
				fmt.Sprintf("%s (%s)", resp.Error.Message, resp.Error.Code), nil)
		}
		if !(parseErr == nil && resp.OK) {
			if ctx.Err() != nil {
				return nil, xcerr.E(xcerr.CodeResourceLimit, "worker call timed out", ctx.Err())
			}
			return nil, xcerr.E(xcerr.CodeAnalyzerFailure, "worker failed",
				fmt.Errorf("%v: %s", waitErr, stderrTail(cmd.Stderr)))
		}
	} else if parseErr != nil {
		return nil, parseErr
	}
	if resp.Protocol != Protocol {
		return nil, xcerr.E(xcerr.CodeAnalyzerFailure,
			fmt.Sprintf("worker protocol %d, want %d", resp.Protocol, Protocol), nil)
	}
	if !resp.OK {
		code, msg := "internal", "worker error"
		if resp.Error != nil {
			code, msg = resp.Error.Code, resp.Error.Message
		}
		return nil, xcerr.E(xcerr.CodeAnalyzerFailure, fmt.Sprintf("%s (%s)", msg, code), nil)
	}
	return resp.Result, nil
}

// buildWorkerCmd wraps bin into an exec command. A single-file Python
// sidecar (".py") is run through the first working python interpreter —
// sidecar scripts are the documented distribution form and must not require
// a compile step. Windows ships a non-functional "python3" Store alias, so
// candidates are probed once and the winner cached.
func buildWorkerCmd(ctx context.Context, bin string) *exec.Cmd {
	if strings.EqualFold(filepath.Ext(bin), ".py") {
		if py := pythonInterpreter(); py != "" {
			return exec.CommandContext(ctx, py, bin)
		}
	}
	return exec.CommandContext(ctx, bin)
}

var pythonInterpreter = sync.OnceValue(func() string {
	for _, py := range []string{"python3", "python"} {
		p, err := exec.LookPath(py)
		if err != nil {
			continue
		}
		probe := exec.Command(p, "-c", "print(1)")
		if err := probe.Run(); err == nil {
			return p
		}
	}
	return ""
})

// errResponseTooLarge marks an oversized worker response.
type errResponseTooLarge struct{ max int64 }

func (e errResponseTooLarge) Error() string {
	return fmt.Sprintf("response exceeds %d bytes", e.max)
}

// limitedBuffer is a fixed-capacity buffer that silently discards overflow
// (stderr is diagnostics only — the cap exists so a runaway worker cannot
// grow host memory through it either).
type limitedBuffer struct {
	buf  bytes.Buffer
	max  int
	full bool
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if l.buf.Len()+len(p) > l.max {
		if !l.full {
			l.buf.Write(p[:max(0, l.max-l.buf.Len())])
			l.full = true
		}
		return len(p), nil // pretend success; diagnostics are best-effort
	}
	return l.buf.Write(p)
}

func (l *limitedBuffer) String() string { return l.buf.String() }

func stderrTail(w io.Writer) string {
	if lb, ok := w.(*limitedBuffer); ok {
		return tail(lb.String(), 300)
	}
	return ""
}

func parseResponse(out []byte) (*response, error) {
	var resp response
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, xcerr.E(xcerr.CodeAnalyzerFailure, "worker response unparseable", err)
	}
	if resp.Protocol != Protocol {
		return nil, xcerr.E(xcerr.CodeAnalyzerFailure,
			fmt.Sprintf("worker protocol %d, want %d", resp.Protocol, Protocol), nil)
	}
	return &resp, nil
}

func tail(s string, n int) string {
	if len(s) > n {
		return strings.TrimSpace(s[len(s)-n:])
	}
	return strings.TrimSpace(s)
}
