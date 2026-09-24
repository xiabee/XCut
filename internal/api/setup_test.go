package api

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xiabee/XCut/internal/setup"
)

// setupTestFetch returns a Fetcher serving a minimal but valid tool archive
// (bin/ffmpeg.exe + bin/ffprobe.exe inside one build dir), built in memory.
func setupTestFetch(t *testing.T) setup.Fetcher {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range []string{"ffmpeg-x/bin/ffmpeg.exe", "ffmpeg-x/bin/ffprobe.exe"} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte("fake " + name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	payload := buf.Bytes()
	return func(_ context.Context, dst io.Writer, _ func(fetched int64)) error {
		_, err := dst.Write(payload)
		return err
	}
}

// setupTestPin pins the installer to exactly the bytes setupTestFetch serves.
func setupTestPin(t *testing.T) setup.Pin {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range []string{"ffmpeg-x/bin/ffmpeg.exe", "ffmpeg-x/bin/ffprobe.exe"} {
		w, _ := zw.Create(name)
		w.Write([]byte("fake " + name))
	}
	zw.Close()
	sum := sha256.Sum256(buf.Bytes())
	return setup.Pin{URL: "https://fake.local/ffmpeg.zip", SHA256: hex.EncodeToString(sum[:]), Bytes: int64(buf.Len())}
}

// setupServer returns a server whose installer is driven by the fake fetch.
func setupServer(t *testing.T) (*Server, *setup.Installer) {
	t.Helper()
	s := testServer(t)
	root := t.TempDir()
	in := &setup.Installer{
		TargetDir:  filepath.Join(root, "bin"),
		ScratchDir: filepath.Join(root, "scratch"),
		Fetch:      setupTestFetch(t),
		Verify:     func(string, string) error { return nil },
		Artifact:   setupTestPin(t),
	}
	s.Setup = in
	return s, in
}

func TestSetupStatusNilInstaller(t *testing.T) {
	s := testServer(t)
	rec, out := do(t, s, "GET", "/api/v1/setup/ffmpeg", "")
	if rec.Code != 200 {
		t.Fatalf("status: %d %v", rec.Code, out)
	}
	if out["phase"] != "unavailable" {
		t.Fatalf("nil installer must answer unavailable, got %v", out["phase"])
	}
	rec, _ = do(t, s, "POST", "/api/v1/setup/ffmpeg", "{}")
	if rec.Code != 415 {
		t.Fatalf("start with nil installer must refuse, got %d", rec.Code)
	}
}

// The FFmpeg installer drives a Windows-only path (the pinned artifact is a
// Windows build); the HTTP tests below that exercise a real install belong on
// Windows, where TestSetupRefusedOffWindows pins the honest refusal instead.
func requireWindowsInstaller(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("automatic FFmpeg install is a Windows-only path; refusal tested by TestSetupRefusedOffWindows")
	}
}

func TestSetupRefusedOffWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the refusal is the non-Windows behavior")
	}
	s, _ := setupServer(t)
	rec, out := do(t, s, "POST", "/api/v1/setup/ffmpeg", "{}")
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("POST on a platform with no pinned artifact: %d %v, want 415", rec.Code, out)
	}
	if out["error"] != "unsupported_media" {
		t.Errorf("error code %v, want unsupported_media", out["error"])
	}
	if msg, _ := out["message"].(string); !strings.Contains(msg, "package manager") {
		t.Errorf("the UI strip must carry the alternative, got %q", msg)
	}
	// GET must keep answering with a usable status rather than an error, so
	// the warning strip can still render.
	rec, out = do(t, s, "GET", "/api/v1/setup/ffmpeg", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status read must not fail: %d %v", rec.Code, out)
	}
	if out["phase"] != "idle" {
		t.Errorf("phase %v, want idle after a refused start", out["phase"])
	}
}

func TestSetupInstallFlow(t *testing.T) {
	requireWindowsInstaller(t)
	s, in := setupServer(t)
	rec, out := do(t, s, "GET", "/api/v1/setup/ffmpeg", "")
	if rec.Code != 200 || out["phase"] != "idle" {
		t.Fatalf("initial status must be idle: %d %v", rec.Code, out)
	}

	// Stall the fetch so the install is genuinely in-flight; the second
	// start below then hits the single-flight gate for real.
	orig := in.Fetch
	releaseCh := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(releaseCh) }) }
	t.Cleanup(release)
	in.Fetch = func(ctx context.Context, dst io.Writer, progress func(fetched int64)) error {
		<-releaseCh
		return orig(ctx, dst, progress)
	}

	rec, out = do(t, s, "POST", "/api/v1/setup/ffmpeg", "{}")
	if rec.Code != 202 {
		t.Fatalf("start must be 202, got %d %v", rec.Code, out)
	}

	// Concurrent start while running → 409 (single-flight).
	rec, _ = do(t, s, "POST", "/api/v1/setup/ffmpeg", "{}")
	if rec.Code != 409 {
		t.Fatalf("second start must be 409, got %d", rec.Code)
	}

	// Release the fetch; the install goroutine finishes and the status
	// endpoint reports done.
	release()
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, out = do(t, s, "GET", "/api/v1/setup/ffmpeg", "")
		if out["phase"] == "done" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("install never finished: %v", out)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if in.Status().FFmpegPath == "" || in.Status().FFprobePath == "" {
		t.Fatal("done status must carry both tool paths")
	}
}

func TestSetupStatusShapeWhileDownloading(t *testing.T) {
	requireWindowsInstaller(t)
	s, in := setupServer(t)
	// Stall the fetch so the status is sampled mid-download: the response
	// must carry source, size and target for the UI's progress line.
	block := make(chan struct{})
	in.Fetch = func(ctx context.Context, dst io.Writer, progress func(fetched int64)) error {
		<-block
		return setupTestFetch(t)(ctx, dst, progress)
	}
	t.Cleanup(func() {
		close(block)
		// Let the install goroutine finish. It resumes with a real write into
		// this test's TempDir, and Go removes that dir the moment the test
		// returns — racing the write against the removal was a flaky
		// "RemoveAll cleanup: directory is not empty".
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
			switch in.Status().Phase {
			case setup.PhaseDownloading, setup.PhaseExtracting, setup.PhaseVerifying:
				time.Sleep(5 * time.Millisecond)
				continue
			}
			return
		}
		t.Errorf("install goroutine never reached a terminal phase: %v", in.Status().Phase)
	})
	do(t, s, "POST", "/api/v1/setup/ffmpeg", "{}")
	_, out := do(t, s, "GET", "/api/v1/setup/ffmpeg", "")
	if out["phase"] != "downloading" {
		t.Fatalf("expected downloading, got %v", out["phase"])
	}
	if out["source"] == "" || out["size_bytes"] == nil || out["target_dir"] == "" {
		t.Fatalf("downloading status must describe the pinned install: %v", out)
	}
}
