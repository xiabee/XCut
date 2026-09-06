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
	"fmt"
	"os/exec"
	"strings"
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

// Call executes one request against the worker binary with a timeout.
// The worker receives the request on stdin and answers once on stdout.
func Call(ctx context.Context, bin string, req Request) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	payload, err := json.Marshal(req)
	if err != nil {
		return nil, xcerr.E(xcerr.CodeInternal, "cannot encode worker request", err)
	}

	cmd := exec.CommandContext(ctx, bin)
	var stderr bytes.Buffer
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		// Workers answer with a structured envelope even on failure (exit 1);
		// prefer that over the bare exec error.
		if resp, perr := parseResponse(out); perr == nil && resp.Error != nil {
			return nil, xcerr.E(xcerr.CodeAnalyzerFailure,
				fmt.Sprintf("%s (%s)", resp.Error.Message, resp.Error.Code), nil)
		}
		if ctx.Err() != nil {
			return nil, xcerr.E(xcerr.CodeResourceLimit, "worker call timed out", ctx.Err())
		}
		return nil, xcerr.E(xcerr.CodeAnalyzerFailure, "worker failed",
			fmt.Errorf("%v: %s", err, tail(stderr.String(), 300)))
	}

	resp, err := parseResponse(out)
	if err != nil {
		return nil, err
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
