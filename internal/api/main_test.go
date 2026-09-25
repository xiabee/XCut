package api

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"testing"
	"time"
)

// TestMain doubles as the transcript sidecar when XCUT_TEST_SIDECAR is set:
// the package's own binary is then the child process the worker execs, which
// is how this repository drives a fake external tool without a shell script
// or a platform-specific stub (see internal/render/main_test.go). The python
// scripts this replaces cost a cold interpreter start per transcription —
// slow enough under AV scanning and gate load that a canned transcription
// once outran a 30 s test deadline — and every test wearing them skipped
// where python was missing.
//
// The canned answer travels by environment, never by argument: the product
// execs the sidecar bare, and the stub must survive exactly how the real
// thing is called. Children inherit the parent's environment, which is why
// the installing tests use t.Setenv.
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
		switch mode {
		case "slow":
			sec, _ := strconv.Atoi(os.Getenv("XCUT_TEST_SIDECAR_SLEEP"))
			time.Sleep(time.Duration(sec) * time.Second)
		case "blocking":
			// Hold the analyze until the sentinel appears (the exclusive-slot
			// tests), bounded so a test that died before releasing cannot hang
			// the child until the worker deadline.
			gate := os.Getenv("XCUT_TEST_SIDECAR_GATE")
			for i := 0; i < 600 && gate != ""; i++ {
				if _, err := os.Stat(gate); err == nil {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
		}
		segment := os.Getenv("XCUT_TEST_SIDECAR_SEGMENT")
		if segment == "" {
			segment = `{"start": 0.5, "end": 1.5, "text": "你好"}`
		}
		result = map[string]any{"language": "zh", "segments": json.RawMessage("[" + segment + "]")}
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
