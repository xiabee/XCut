package cli

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/config"
)

func TestFileLoggerRotation(t *testing.T) {
	dir := t.TempDir()
	fl, err := newFileLogger(dir, "serve", 1 /* 1 MiB */, 3)
	if err != nil {
		t.Fatal(err)
	}

	// Write > 1 MiB in small chunks to force at least one rotation.
	chunk := strings.Repeat("x", 4096)
	for i := 0; i < 300; i++ {
		if _, err := fl.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if err := fl.Close(); err != nil {
		t.Fatal(err)
	}

	main, err := os.Stat(filepath.Join(dir, "serve.log"))
	if err != nil {
		t.Fatal(err)
	}
	if main.Size() > 1<<20 {
		t.Fatalf("main log %d bytes exceeds budget", main.Size())
	}
	if _, err := os.Stat(filepath.Join(dir, "serve.log.1")); err != nil {
		t.Fatalf("rotated file missing: %v", err)
	}
}

func TestFileLoggerUnderBudgetNoRotation(t *testing.T) {
	dir := t.TempDir()
	fl, err := newFileLogger(dir, "serve", 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fl.Write([]byte("small")); err != nil {
		t.Fatal(err)
	}
	fl.Close()
	if _, err := os.Stat(filepath.Join(dir, "serve.log.1")); !os.IsNotExist(err) {
		t.Fatal("rotation happened under budget")
	}
}

func TestServeLoggerWritesToFile(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	_ = config.Resolve(cfg)
	a := &App{Cfg: cfg, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Stderr: &bytes.Buffer{}}
	log, closeFn := newServeLogger(a, dir)
	log.Info("hello from serve", "k", 1)
	closeFn()

	b, err := os.ReadFile(filepath.Join(dir, "logs", "serve.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "hello from serve") {
		t.Fatalf("log file missing record: %q", string(b))
	}
}

// TestFileLoggerRotationReopenFailureWarns: when the rotated log file cannot
// be reopened (here: a directory sits where the file belongs), the logger
// TestFileLoggerRotationReopenFailureWarns: when the rotated log file cannot
// be reopened, the logger prints exactly one warning to stderr instead of
// dead-ending silently (every later write used to fail with no trace).
func TestFileLoggerRotationReopenFailureWarns(t *testing.T) {
	dir := t.TempDir()
	fl, err := newFileLogger(dir, "serve", 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fl.Close() })
	if _, err := fl.Write([]byte("first")); err != nil {
		t.Fatal(err)
	}

	// Sabotage the reopen: point the path at a directory that does not
	// exist, and force the next write past the rotation threshold.
	fl.mu.Lock()
	fl.path = filepath.Join(dir, "rotated-away", "serve.log")
	fl.written = fl.maxBytes
	fl.mu.Unlock()

	r, w, _ := os.Pipe()
	old := os.Stderr
	os.Stderr = w
	_, _ = fl.Write([]byte("trigger rotation"))
	_, _ = fl.Write([]byte("and again"))
	w.Close()
	os.Stderr = old
	out, _ := io.ReadAll(r)
	if n := strings.Count(string(out), "log rotation failed"); n != 1 {
		t.Fatalf("stderr warnings = %d, want exactly 1 (%q)", n, string(out))
	}
}
