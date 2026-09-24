package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

// TestAISidecarHTTPBackends: the env-configured OpenAI-compatible backends
// (XCUT_SIDECAR_STT_URL for transcription, XCUT_SIDECAR_VISION_URL for
// frame description) must round-trip through the reference sidecar against
// a stub gateway — the reservation stays honest (tested seam, not a doc
// wish). No external network: httptest serves both endpoints.
func TestAISidecarHTTPBackends(t *testing.T) {
	bin := sidecarCmd(t)

	media := filepath.Join(t.TempDir(), "a.mp4")
	if err := os.WriteFile(media, []byte("fake audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	image := filepath.Join(t.TempDir(), "f.jpg")
	if err := os.WriteFile(image, []byte("fake jpeg"), 0o644); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/audio/transcriptions", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("multipart: %v", err)
		}
		if r.FormValue("model") == "" {
			t.Error("transcription request missing model field")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"language":"zh","segments":[{"start":0.5,"end":1.5,"text":"测试","words":[{"start":0.5,"end":1.5,"word":"测试"}]}]}`)
	})
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Content []map[string]any `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Model != "vision-test" {
			t.Errorf("vision model = %q, want vision-test", body.Model)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"choices":[{"message":{"content":"a badminton court"}}]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	t.Setenv("XCUT_SIDECAR_STT_URL", srv.URL)
	t.Setenv("XCUT_SIDECAR_VISION_URL", srv.URL)
	t.Setenv("XCUT_SIDECAR_VISION_MODEL", "vision-test")

	ctx := context.Background()
	raw, err := AIAnalyze(ctx, bin, "analyze", media,
		map[string]any{"analyzer": "transcript", "model": "whisper-1"}, 30*time.Second)
	if err != nil {
		t.Fatalf("http transcript: %v", err)
	}
	var tr struct {
		Language string `json:"language"`
	}
	if err := json.Unmarshal(raw, &tr); err != nil {
		t.Fatalf("transcript result shape: %v (%s)", err, raw)
	}
	if tr.Language != "zh" {
		t.Errorf("transcript language = %q, want zh", tr.Language)
	}

	raw, err = AIAnalyze(ctx, bin, "analyze", image,
		map[string]any{"analyzer": "frame_describe"}, 30*time.Second)
	if err != nil {
		t.Fatalf("frame_describe: %v", err)
	}
	var fd struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal(raw, &fd); err != nil {
		t.Fatalf("frame_describe result shape: %v (%s)", err, raw)
	}
	if fd.Description != "a badminton court" {
		t.Errorf("description = %q", fd.Description)
	}
}
