package setup

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/xiabee/XCut/internal/xcerr"
)

// These cases drive runErr rather than Start on purpose: Start refuses to install
// anything off Windows (the pinned artifact is a Windows build, and that refusal has
// its own test), while the fetch/size/hash gates below are platform-independent and
// belong on every leg. runErr is the engine both paths share.

// servedPin builds a zip the size and shape the installer expects, serves it from a
// local HTTP source, and returns the URL along with a recorder of what was requested.
// Nothing here touches the network: the one source of bytes is this process.
type servedPin struct {
	mu    sync.Mutex
	paths []string
	srv   *httptest.Server
}

func (s *servedPin) handler(body []byte, status int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.paths = append(s.paths, r.URL.Path)
		s.mu.Unlock()
		if status != http.StatusOK {
			http.Error(w, "no", status)
			return
		}
		_, _ = w.Write(body)
	})
}

func (s *servedPin) requestPaths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.paths...)
}

// bigToolZip writes a zip whose on-disk size is at least wantBytes, so the byte
// counters actually cross their reporting interval instead of being skipped by a
// fixture that fits in one write. The filler is stored, not deflated: a zip of
// zeros would compress to nothing and measure the compressor, not the counter.
func bigToolZip(t *testing.T, wantBytes int64) []byte {
	t.Helper()
	entries := map[string][]byte{
		"ffmpeg-x/bin/ffmpeg.exe":  []byte("payload-of-ffmpeg"),
		"ffmpeg-x/bin/ffprobe.exe": []byte("payload-of-ffprobe"),
	}
	filler := bytes.Repeat([]byte{0x5a, 0xa5, 0x3c, 0xc3}, 1<<20) // 4 MiB, no run to collapse
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	for n := int64(0); buf.Len() < int(wantBytes); n++ {
		w, err := zw.CreateHeader(&zip.FileHeader{
			Name:   fmt.Sprintf("ffmpeg-x/share/filler-%d.bin", n),
			Method: zip.Store,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(filler); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if int64(buf.Len()) < wantBytes {
		t.Fatalf("the fixture zip is %d bytes, want at least %d", buf.Len(), wantBytes)
	}
	return buf.Bytes()
}

func pinOf(body []byte, url string) Pin {
	sum := sha256.Sum256(body)
	return Pin{URL: url, SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(body))}
}

func installerAt(root string, body []byte) (*Installer, *servedPin) {
	sp := &servedPin{}
	srv := httptest.NewServer(sp.handler(body, http.StatusOK))
	sp.srv = srv
	return &Installer{
		TargetDir:  filepath.Join(root, "bin"),
		ScratchDir: filepath.Join(root, "scratch"),
		Fetch:      nil, // the production path: fetch the pin's own URL
		Verify:     func(string, string) error { return nil },
		Artifact:   pinOf(body, srv.URL+"/ffmpeg-pinned.zip"),
	}, sp
}

// TestFetchDownloadsFromThePinInForce closes the gap where the installer reported one
// source to the UI (Status.Source comes from the resolved pin) while fetchPinned
// reached for the package-level constant. Overriding the artifact is how every test in
// this file describes its payload, and how a mirror would be described later; either
// way the bytes that get gated must be the bytes that were fetched.
func TestFetchDownloadsFromThePinInForce(t *testing.T) {
	body := bigToolZip(t, 1<<20)
	root := t.TempDir()
	in, sp := installerAt(root, body)
	defer sp.srv.Close()

	if err := in.runErr(context.Background()); err != nil {
		t.Fatalf("install from a local source failed: %v", err)
	}
	if got := sp.requestPaths(); len(got) != 1 || got[0] != "/ffmpeg-pinned.zip" {
		t.Errorf("the source was asked for %v, want exactly one GET of the pinned path", got)
	}
	st := in.Status()
	if st.FFprobePath == "" {
		t.Fatalf("no installed ffprobe in %+v", st)
	}
	// Status.Source is Start's bookkeeping (and the Windows-gated happy path asserts
	// it names the pin); what this case owns is that the bytes were fetched from
	// that same pin, which the recorded request above already proves.
	if _, err := os.Stat(filepath.Join(in.TargetDir, "ffprobe.exe")); err != nil {
		t.Errorf("ffprobe.exe did not land in the target dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(in.ScratchDir, "ffmpeg-pinned.zip")); !os.IsNotExist(err) {
		t.Errorf("the scratch zip survived a finished install")
	}
}

// TestFetchRefusesASourceThatDoesNotAnswer is the transport's own failure path: a
// source that answers 503 must reach the user as a refused download naming the
// status, not as a truncated file that then trips the size gate with a misleading
// byte count.
func TestFetchRefusesASourceThatDoesNotAnswer(t *testing.T) {
	sp := &servedPin{}
	srv := httptest.NewServer(sp.handler(nil, http.StatusServiceUnavailable))
	defer srv.Close()
	root := t.TempDir()
	in := &Installer{
		TargetDir:  filepath.Join(root, "bin"),
		ScratchDir: filepath.Join(root, "scratch"),
		Verify:     func(string, string) error { return nil },
		Artifact:   Pin{URL: srv.URL + "/ffmpeg-pinned.zip", SHA256: "0000", Bytes: 1},
	}
	err := in.runErr(context.Background())
	if err == nil {
		t.Fatal("a source answering 503 was accepted")
	}
	if code := xcerr.CodeOf(err); code != xcerr.CodeInternal {
		t.Errorf("code %q, want the internal download failure", code)
	}
	chain := err.Error()
	for _, want := range []string{"FFmpeg download failed", "answered 503"} {
		if !strings.Contains(chain, want) {
			t.Errorf("the chain lost %q: %s", want, chain)
		}
	}
}

// TestSizeGateFiresAfterTheProgressCeiling is the one case that runs the real
// transport across more than one progress interval, which is what makes the
// byte-ceiling claim and the progress clamp observable at all: a fixture that
// streams in a single write never reaches either. The source delivers 8 MiB of
// correct bytes and the pin claims 1 KiB more, so the run ends on the size gate
// *after* progress has been reported — and the reported figure is 99, because the
// last percent belongs to extract and verify, not to a download that never
// finished.
func TestSizeGateFiresAfterTheProgressCeiling(t *testing.T) {
	body := bigToolZip(t, 8<<20)
	pin := pinOf(body, "")
	pin.Bytes += 1024 // the artifact is short by one page
	sp := &servedPin{}
	srv := httptest.NewServer(sp.handler(body, http.StatusOK))
	defer srv.Close()
	pin.URL = srv.URL + "/ffmpeg-pinned.zip"

	root := t.TempDir()
	in := &Installer{
		TargetDir:  filepath.Join(root, "bin"),
		ScratchDir: filepath.Join(root, "scratch"),
		Verify:     func(string, string) error { return nil },
		Artifact:   pin,
	}
	err := in.runErr(context.Background())
	if err == nil {
		t.Fatalf("a download %d bytes short of its pin was accepted", 1024)
	}
	if code := xcerr.CodeOf(err); code != xcerr.CodeResourceLimit {
		t.Errorf("code %q, want the resource limit the size gate raises", code)
	}
	want := fmt.Sprintf("got %d bytes, want %d", len(body), pin.Bytes)
	if !strings.Contains(err.Error(), want) {
		t.Errorf("the refusal does not name both counts (%s): %v", want, err)
	}
	if st := in.Status(); st.ProgressPct != 99 {
		t.Errorf("progress after a crossed interval reads %v, want the clamped 99 (never 100 before verify)",
			st.ProgressPct)
	}
	if _, err := os.Stat(filepath.Join(in.TargetDir, "ffprobe.exe")); !os.IsNotExist(err) {
		t.Error("a refused artifact still installed tools")
	}
}

// TestCountingWriterReportsOncePerInterval pins the batching the doc claims: every
// write would thrash the mutex the UI polls, so a writer that fires on each chunk is a
// performance bug, and one that never fires means the progress bar is decoration.
func TestCountingWriterReportsOncePerInterval(t *testing.T) {
	var seen []float64
	c := &countingWriter{
		w:     io.Discard,
		total: 10 << 20,
		cb:    func(pct float64) { seen = append(seen, pct) },
	}
	if _, err := c.Write(make([]byte, 5<<20)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write(make([]byte, 1<<20)); err != nil { // below the interval
		t.Fatal(err)
	}
	if _, err := c.Write(make([]byte, 4<<20)); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 {
		t.Fatalf("progress fired %d times for 10 MiB at %d-byte intervals (%v), want 2",
			len(seen), progressInterval, seen)
	}
	if seen[0] != 50 {
		t.Errorf("first report was %v%%, want 50%% of a 10 MiB pin", seen[0])
	}
	if seen[1] != 99 {
		t.Errorf("the last report was %v%%, want the 99%% clamp that leaves a percent for verify", seen[1])
	}
}
