package worker

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

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
