package workspace

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func newTestWS(t *testing.T) *Workspace {
	t.Helper()
	ws := New(filepath.Join(t.TempDir(), "ws"))
	if err := ws.Ensure(); err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestSafeJoinAccepts(t *testing.T) {
	ws := newTestWS(t)
	for _, name := range []string{"a.txt", filepath.Join("projects", "p1", "timeline.json"), "cache/analysis/x"} {
		got, err := ws.SafeJoin(name)
		if err != nil {
			t.Fatalf("SafeJoin(%q): %v", name, err)
		}
		if !strings.HasPrefix(got, ws.Root) {
			t.Fatalf("SafeJoin(%q) = %q escapes root", name, got)
		}
	}
}

func TestSafeJoinRejects(t *testing.T) {
	ws := newTestWS(t)
	cases := []string{
		"..",
		"../escape.txt",
		"sub/../../escape",
		"/etc/passwd",
		`\Windows\system32`,
		"",
		"  ",
	}
	if runtime.GOOS == "windows" {
		cases = append(cases, `C:\Windows\evil`, `C:evil`, `\\server\share\x`, "CON", "aux.txt", "COM1")
	}
	for _, name := range cases {
		if got, err := ws.SafeJoin(name); err == nil {
			t.Fatalf("SafeJoin(%q) = %q, want error", name, got)
		}
	}
}

func TestSafeJoinSymlinkEscape(t *testing.T) {
	ws := newTestWS(t)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Skip("cannot create outside dir")
	}
	link := filepath.Join(ws.Root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if got, err := ws.SafeJoin(filepath.Join("link", "evil.txt")); err == nil {
		t.Fatalf("SafeJoin through symlink = %q, want error", got)
	}
}

func TestCleanupTemp(t *testing.T) {
	ws := newTestWS(t)
	d1, err := ws.NewTempDir("job")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d1, "x.bin"), make([]byte, 1024), 0o644); err != nil {
		t.Fatal(err)
	}

	removed, bytes, err := ws.CleanupTemp(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || bytes != 1024 {
		t.Fatalf("dry run: removed=%v bytes=%d", removed, bytes)
	}
	if _, err := os.Stat(d1); err != nil {
		t.Fatal("dry run must not delete")
	}

	removed, bytes, err = ws.CleanupTemp(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || bytes != 1024 {
		t.Fatalf("real run: removed=%v bytes=%d", removed, bytes)
	}
	if _, err := os.Stat(d1); !os.IsNotExist(err) {
		t.Fatal("temp dir should be gone")
	}
}

func TestDiskFree(t *testing.T) {
	free, err := DiskFree(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if free == 0 {
		t.Fatal("free = 0")
	}
}
