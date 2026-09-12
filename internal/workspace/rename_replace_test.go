package workspace

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
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
