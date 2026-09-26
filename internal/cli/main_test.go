package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"testing"
)

// TestMain doubles as the transcript sidecar when XCUT_TEST_SIDECAR is set:
// the package's own binary is then the child process the worker execs, the
// same mechanism internal/api's TestMain introduced when its python stubs
// grew a cold-interpreter cost that a gate under load could no longer
// ignore. The canned answer travels by environment, never by argument —
// the product execs the sidecar bare, and the stub must survive exactly
// how the real thing is called; children inherit the parent's environment,
// which is why the installing tests use t.Setenv.
//
// The reference sidecar (scripts/xcut-ai-sidecar.py) deliberately stays a
// python child wherever a test points at it: there the stub would gut the
// assertion, because the scoreboard scan's value is that the real detector
// measured a real fixture.
func TestMain(m *testing.M) {
	if mode := os.Getenv("XCUT_TEST_SIDECAR"); mode != "" {
		os.Exit(runFakeSidecar(mode))
	}
	os.Exit(m.Run())
}

func runFakeSidecar(mode string) int {
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake sidecar: read request:", err)
		return 1
	}
	var req struct {
		Op string `json:"op"`
	}
	// An unreadable request falls through to the empty arm, same as the
	// python stub's `or "{}"`.
	_ = json.Unmarshal(raw, &req)

	var result any
	switch {
	case req.Op == "capabilities":
		result = map[string]any{
			"ops":    []any{map[string]any{"op": "analyze"}},
			"models": []any{map[string]any{"name": "transcript", "available": true, "loaded": false, "detail": "fake"}},
		}
	case req.Op == "analyze":
		segments := os.Getenv("XCUT_TEST_SIDECAR_SEGMENTS")
		if segments == "" {
			segments = `[{"start": 0.5, "end": 1.5, "text": "你好"}]`
		}
		result = map[string]any{"language": "zh", "segments": json.RawMessage(segments)}
	default:
		result = map[string]any{}
	}

	out, err := json.Marshal(map[string]any{"protocol": 1, "ok": true, "op": req.Op, "result": result})
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake sidecar: encode answer:", err)
		return 1
	}
	if _, err := os.Stdout.Write(out); err != nil {
		return 1
	}
	return 0
}
