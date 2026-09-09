package workspace

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/xcerr"
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

func TestCleanupPartials(t *testing.T) {
	w := New(t.TempDir())
	if err := w.Ensure(); err != nil {
		t.Fatal(err)
	}
	proj := filepath.Join(w.ProjectsDir(), "prj_x")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(proj, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("render.mp4.partial", "crash debris")
	write(".tmp-123456", "atomic-write debris")
	write("render.mp4", "VERIFIED OUTPUT")
	write("timeline.json", "{}")

	// Dry run reports but keeps.
	count, bytes, err := w.CleanupPartials(true)
	if err != nil || count != 2 || bytes == 0 {
		t.Fatalf("dry run: count=%d bytes=%d err=%v", count, bytes, err)
	}
	for _, keep := range []string{"render.mp4.partial", ".tmp-123456"} {
		if _, err := os.Stat(filepath.Join(proj, keep)); err != nil {
			t.Fatalf("dry run must keep %s: %v", keep, err)
		}
	}

	count, _, err = w.CleanupPartials(false)
	if err != nil || count != 2 {
		t.Fatalf("cleanup: count=%d err=%v", count, err)
	}
	for _, gone := range []string{"render.mp4.partial", ".tmp-123456"} {
		if _, err := os.Stat(filepath.Join(proj, gone)); !os.IsNotExist(err) {
			t.Fatalf("%s must be removed", gone)
		}
	}
	// Real output and timeline survive; a second run is a no-op.
	for _, keep := range []string{"render.mp4", "timeline.json"} {
		if _, err := os.Stat(filepath.Join(proj, keep)); err != nil {
			t.Fatalf("%s must survive cleanup: %v", keep, err)
		}
	}
	if count, _, err := w.CleanupPartials(false); err != nil || count != 0 {
		t.Fatalf("second cleanup: count=%d err=%v", count, err)
	}
}

func TestNewTempDirBudget(t *testing.T) {
	ws := newTestWS(t)
	// Stuff temp/ beyond a tiny budget (simulates failed-run debris).
	junk := filepath.Join(ws.TempDir(), "render-dead")
	if err := os.MkdirAll(junk, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(junk, "clip.mp4"), make([]byte, 200), 0o644); err != nil {
		t.Fatal(err)
	}

	ws.MaxTempBytes = 100
	_, err := ws.NewTempDir("render")
	if !xcerr.IsCode(err, xcerr.CodeResourceLimit) {
		t.Fatalf("err = %v, want resource_limit", err)
	}
	if msg := xcerr.UserMessage(err); !strings.Contains(msg, "xcut cleanup") {
		t.Fatalf("error message %q must point at the remedy", msg)
	}

	// Raising the budget admits new scratch.
	ws.MaxTempBytes = 1000
	d, err := ws.NewTempDir("render")
	if err != nil {
		t.Fatalf("NewTempDir under budget: %v", err)
	}
	if d == "" {
		t.Fatal("empty temp dir path")
	}

	// 0 disables the gate entirely.
	ws.MaxTempBytes = 0
	if _, err := ws.NewTempDir("render"); err != nil {
		t.Fatalf("NewTempDir with budget off: %v", err)
	}
	if got := ws.TempUsage(); got < 200 {
		t.Fatalf("TempUsage = %d, want >= 200", got)
	}
}
