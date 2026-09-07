package worker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// requirePython locates the reference sidecar script (the protocol contract
// exercised by these tests). The client picks the interpreter itself.
func requirePython(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Skip("cannot locate repo root")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	script := filepath.Join(root, "scripts", "xcut-ai-sidecar.py")
	if _, err := os.Stat(script); err != nil {
		t.Skipf("reference sidecar missing: %v", err)
	}
	if _, err := exec.LookPath("python"); err != nil {
		if _, err := exec.LookPath("python3"); err != nil {
			t.Skip("python not available")
		}
	}
	return script
}

func sidecarCmd(t *testing.T) string {
	t.Helper()
	return requirePython(t)
}

// TestAISidecarProtocol drives the reference sidecar through the full
// discovery handshake: describe → capabilities → health.
func TestAISidecarProtocol(t *testing.T) {
	bin := sidecarCmd(t)
	ctx := context.Background()

	d, err := Probe(ctx, bin)
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "xcut-ai-sidecar" || d.Protocol != Protocol {
		t.Fatalf("describe: %+v", d)
	}

	caps, err := Capabilities(ctx, bin)
	if err != nil {
		t.Fatal(err)
	}
	if len(caps.Ops) == 0 || caps.Device == "" {
		t.Fatalf("capabilities incomplete: %+v", caps)
	}

	h, err := Health(ctx, bin)
	if err != nil {
		t.Fatal(err)
	}
	if !h.Ready {
		t.Fatalf("sidecar health not ready: %+v", h)
	}
}

// TestAISidecarAnalyzeNotImplemented: analyzing a non-advertised analyzer
// must surface a structured error, never a crash or hang.
func TestAISidecarAnalyzeNotImplemented(t *testing.T) {
	bin := sidecarCmd(t)
	_, err := AIAnalyze(context.Background(), bin, "analyze", "media.mp4",
		map[string]any{"analyzer": "whisper"}, 30*time.Second)
	if err == nil {
		t.Fatal("expected structured not_implemented error")
	}
}

// TestCallBoundedRejectsOversized: a worker spraying unbounded output must
// fail with a resource error instead of growing host memory. The sidecar is
// driven with a tiny cap via an oversized raw response from python.
func TestCallBoundedRejectsOversized(t *testing.T) {
	requirePython(t)
	dir := t.TempDir()
	script := filepath.Join(dir, "spammer.py")
	code := "import sys\nsys.stdout.write('{\"protocol\":1,\"ok\":true,\"op\":\"x\",\"result\":\"' + 'a'*100000 + '\"}')\n"
	if err := os.WriteFile(script, []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := callBounded(context.Background(), script,
		Request{Protocol: Protocol, Op: "x"}, 10*time.Second, 1024); err == nil {
		t.Fatal("oversized response must fail")
	}
}

func TestResolveAIBin(t *testing.T) {
	if got := ResolveAIBin(""); got != "" {
		// Environment-dependent: if a sidecar really is on PATH that is fine.
		t.Logf("ResolveAIBin(\"\") = %q", got)
	}
	fake := filepath.Join(t.TempDir(), "no-such-dir", "sidecar.py")
	if got := ResolveAIBin(fake); got != fake {
		t.Fatalf("explicit config must win: %q", got)
	}
}
