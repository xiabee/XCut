package workspace

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestRetryableReplaceThroughSharedHolder pins the publish-vs-playback
// invariant: a reader holding the destination with full sharing modes (what
// the serve download handler does via OpenReadable) must not fail a publish.
// The old reader keeps streaming the previous bytes until EOF; new readers
// see the new content.
func TestRetryableReplaceThroughSharedHolder(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "out.mp4")
	old := bytes.Repeat([]byte("A"), 4096)
	if err := os.WriteFile(dst, old, 0o644); err != nil {
		t.Fatal(err)
	}

	holder, err := OpenReadable(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()

	src := filepath.Join(dir, "new.mp4")
	fresh := bytes.Repeat([]byte("B"), 2048)
	if err := os.WriteFile(src, fresh, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RetryableReplace(src, dst); err != nil {
		t.Fatalf("publish through a shared holder failed: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, fresh) {
		t.Fatal("published content is not the new file")
	}

	// The pre-existing holder must still read the old bytes (Windows keeps
	// the delete-pending data readable until the handle closes; on POSIX the
	// unlinked inode serves the same guarantee).
	buf := make([]byte, len(old))
	if _, err := holder.ReadAt(buf, 0); err != nil {
		t.Fatalf("old holder cannot read its data after replace: %v", err)
	}
	if !bytes.Equal(buf, old) {
		t.Fatal("old holder sees new content — playback would corrupt")
	}
}

// TestRetryableRenameWaitsOutABriefHolder pins the reason the budget is two
// seconds rather than the ~420 ms of escalating sleeps it had: a holder that
// releases later than that window (an antivirus scan on a busy machine, which is
// what the one win-devops "unexpected PUT status 500" looked like) must still
// end in a published file, not in a user-visible failure. The holder is
// os.Open, i.e. no delete-share — the shape the API's own reads take.
func TestRetryableRenameWaitsOutABriefHolder(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only semantics")
	}
	dir := t.TempDir()
	dst := filepath.Join(dir, "doc.json")
	src := filepath.Join(dir, "tmp.json")
	for _, p := range []string{dst, src} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h, err := os.Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		time.Sleep(900 * time.Millisecond)
		_ = h.Close()
		done <- nil
	}()
	start := time.Now()
	if err := RetryableRename(src, dst); err != nil {
		t.Fatalf("rename gave up while a holder that releases after 900ms was still in the budget window: %v (waited %s)", err, time.Since(start))
	}
	if b, err := os.ReadFile(dst); err != nil || string(b) != "x" {
		t.Fatalf("destination after publish: %q (%v)", b, err)
	}
}

// TestRetryableRenameGivesUpBoundedly is the other half: a holder that never
// releases must produce an error, and must not turn the save into a hang. The
// window is measured, not assumed — under 4 s here, so a stuck rename still
// fails a request rather than blocking the request handler.
func TestRetryableRenameGivesUpBoundedly(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only semantics")
	}
	dir := t.TempDir()
	dst := filepath.Join(dir, "doc.json")
	src := filepath.Join(dir, "tmp.json")
	for _, p := range []string{dst, src} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h, err := os.Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()

	start := time.Now()
	err = RetryableRename(src, dst)
	waited := time.Since(start)
	if err == nil {
		t.Fatal("rename succeeded while the destination handle was still open")
	}
	if waited < transientIOWaitBudget {
		t.Errorf("gave up after %s, before the %s budget", waited, transientIOWaitBudget)
	}
	if waited > 4*time.Second {
		t.Errorf("gave up after %s; the give-up itself must stay quick enough to fail a request", waited)
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("the source must survive a failed publish so the caller can retry: %v", err)
	}
}

// TestOpenReadableReads documents that OpenReadable is a plain readable
// handle on every platform.
func TestOpenReadableReads(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := OpenReadable(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	buf := make([]byte, 5)
	if _, err := f.Read(buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "hello" {
		t.Fatalf("read %q", buf)
	}
}

// TestRetryableReplaceFailsWhenDeleteShareMissing: a holder without
// FILE_SHARE_DELETE (an external player) still blocks the publish — the
// rename error surfaces instead of a silent success. Windows-only question;
// on POSIX a rename over any open file just succeeds, so the replace path
// never engages.
func TestRetryableReplaceFailsWhenDeleteShareMissing(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only semantics")
	}
	dir := t.TempDir()
	dst := filepath.Join(dir, "out.mp4")
	if err := os.WriteFile(dst, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	h, err := os.Open(dst) // os.Open shares READ|WRITE only — no delete
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	src := filepath.Join(dir, "new.mp4")
	if err := os.WriteFile(src, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RetryableReplace(src, dst); err == nil {
		t.Fatal("publish should fail while a no-delete-share holder pins the name")
	}
}
