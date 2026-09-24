package setup

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/xiabee/XCut/internal/xcerr"
)

// fakeFetch serves arbitrary bytes through the Fetcher seam with a
// deterministic pace so tests can observe progress and single-flight.
func fakeFetch(payload []byte) Fetcher {
	return func(_ context.Context, dst io.Writer, progress func(fetched int64)) error {
		if _, err := dst.Write(payload); err != nil {
			return err
		}
		if progress != nil {
			progress(int64(len(payload)))
		}
		return nil
	}
}

// buildZip produces a zip whose entries mimic the pinned FFmpeg archive:
// one top-level build dir with bin/ffmpeg.exe and bin/ffprobe.exe, plus
// whatever extra entries are requested (used for slip/duplicate cases).
func buildZip(t *testing.T, dir string, extra ...string) string {
	t.Helper()
	entries := append([]string{
		"ffmpeg-x/bin/ffmpeg.exe",
		"ffmpeg-x/bin/ffprobe.exe",
		"ffmpeg-x/bin/ffplay.exe", // not installed — only the two tools are
		"ffmpeg-x/README.txt",
	}, extra...)
	return zipEntries(t, dir, "fake-ffmpeg.zip", entries)
}

// zipEntries writes a zip holding `entries`, which are full paths inside the
// archive: the root folder a real artifact carries is part of what a test means
// to check, so it lives in the entries rather than in a constant here.
func zipEntries(t *testing.T, dir, name string, entries []string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, ent := range entries {
		w, err := zw.Create(ent)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte("payload-of-" + ent)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

// pinFor describes arbitrary bytes as the artifact identity, so the
// installer's size+hash gates accept test payloads.
func pinFor(t *testing.T, b []byte) Pin {
	t.Helper()
	sum := sha256.Sum256(b)
	return Pin{URL: "https://fake.local/ffmpeg.zip", SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(b))}
}

// installerFor wires an Installer against a pre-built zip: the fetch seam
// copies the file's bytes and the verify seam succeeds.
func installerFor(t *testing.T, zipPath, target, scratch string) *Installer {
	t.Helper()
	payload, err := os.ReadFile(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	return &Installer{
		TargetDir:  target,
		ScratchDir: scratch,
		Fetch:      fakeFetch(payload),
		Verify:     func(string) error { return nil },
		Artifact:   pinFor(t, payload),
	}
}

func waitPhase(t *testing.T, in *Installer, want ...Phase) Status {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		st := in.Status()
		for _, p := range want {
			if st.Phase == p {
				return st
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("phase never reached %v (last: %s %s)", want, in.Status().Phase, in.Status().Error)
	return Status{}
}

// requireWindowsInstaller gates the tests that drive Installer.Start's real
// install path. Start refuses off Windows by design (the pinned Gyan.dev build
// is a Windows artifact; every other platform is told to use its package
// manager), so those tests are only meaningful where the path exists — and
// TestStartRefusedOffWindows pins the refusal itself.
func requireWindowsInstaller(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("automatic FFmpeg install is a Windows-only path; refusal tested by TestStartRefusedOffWindows")
	}
}

func TestStartRefusedOffWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the refusal is the non-Windows behavior")
	}
	root := t.TempDir()
	in := installerFor(t, buildZip(t, root), filepath.Join(root, "bin"), filepath.Join(root, "scratch"))
	err := in.Start(context.Background())
	if err == nil {
		t.Fatal("Start accepted on a platform with no pinned artifact")
	}
	if code := xcerr.CodeOf(err); code != xcerr.CodeUnsupportedMedia {
		t.Fatalf("refusal code %q, want unsupported_media", code)
	}
	// The honest message must name the alternative, not just say "no".
	if msg := xcerr.UserMessage(err); !strings.Contains(msg, "package manager") {
		t.Fatalf("refusal tells the user nothing actionable: %q", msg)
	}
	if st := in.Status(); st.Phase != PhaseIdle {
		t.Fatalf("a refused install must not leave a phase behind: %q", st.Phase)
	}
}

func TestInstallHappyPath(t *testing.T) {
	requireWindowsInstaller(t)
	root := t.TempDir()
	in := installerFor(t, buildZip(t, root), filepath.Join(root, "bin"), filepath.Join(root, "scratch"))
	if err := in.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := waitPhase(t, in, PhaseDone)
	if st.FFmpegPath == "" || st.FFprobePath == "" {
		t.Fatalf("done status must name both tools: %+v", st)
	}
	for _, p := range []string{st.FFmpegPath, st.FFprobePath} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "payload-of") {
			t.Fatalf("%s has wrong content", p)
		}
	}
	// Only the two tools land: no ffplay, no README.
	if _, err := os.Stat(filepath.Join(root, "bin", "ffplay.exe")); !os.IsNotExist(err) {
		t.Fatal("ffplay.exe must not be installed")
	}
	// The scratch zip is cleaned up.
	if _, err := os.Stat(filepath.Join(root, "scratch", "ffmpeg-pinned.zip")); !os.IsNotExist(err) {
		t.Fatal("scratch zip must be removed after the install")
	}
	if st.ProgressPct != 100 {
		t.Fatalf("done progress = %v", st.ProgressPct)
	}
	// The UI reads this as "where did the binary on my machine come from", so it has
	// to be the source the gates ran against — the same URL fetchPinned is given.
	if st.Source != in.pin().URL {
		t.Errorf("Status.Source = %q, want the pin's own URL %q", st.Source, in.pin().URL)
	}
}

func TestInstallSingleFlight(t *testing.T) {
	requireWindowsInstaller(t)
	root := t.TempDir()
	payload := []byte("stalled")
	release := make(chan struct{})
	in := &Installer{
		TargetDir:  filepath.Join(root, "bin"),
		ScratchDir: filepath.Join(root, "scratch"),
		Fetch: func(_ context.Context, dst io.Writer, _ func(fetched int64)) error {
			<-release
			_, err := dst.Write(payload)
			return err
		},
		Verify:   func(string) error { return nil },
		Artifact: pinFor(t, payload),
	}
	if err := in.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	err := in.Start(context.Background())
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("second Start must be ErrBusy, got %v", err)
	}
	close(release)
	waitPhase(t, in, PhaseError) // "stalled" is not a valid zip — error is the honest end
}

func TestInstallBadZipFailsHonest(t *testing.T) {
	requireWindowsInstaller(t)
	root := t.TempDir()
	payload := []byte("definitely not a zip")
	in := &Installer{
		TargetDir:  filepath.Join(root, "bin"),
		ScratchDir: filepath.Join(root, "scratch"),
		Fetch:      fakeFetch(payload),
		Verify:     func(string) error { return nil },
		Artifact:   pinFor(t, payload),
	}
	if err := in.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := waitPhase(t, in, PhaseError)
	if st.Error == "" {
		t.Fatal("error phase must carry a user-safe message")
	}
}

func TestExtractRefusesZipSlip(t *testing.T) {
	root := t.TempDir()
	// An archive with a traversal entry: extraction must refuse it (and
	// must not create anything outside the target dir).
	p := filepath.Join(root, "slip.zip")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, name := range []string{
		"ffmpeg-x/bin/ffprobe.exe",
		"../outside.exe",
		"..\\outside2.exe",
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(root, "bin")
	_, _, err = extractTools(p, target)
	if err == nil {
		t.Fatal("slip entries must fail extraction")
	}
	if _, err := os.Stat(filepath.Join(root, "outside.exe")); !os.IsNotExist(err) {
		t.Fatal("no file may land outside the target dir")
	}
	if _, err := os.Stat(filepath.Join(root, "outside2.exe")); !os.IsNotExist(err) {
		t.Fatal("no file may land outside the target dir (backslash form)")
	}
}

func TestExtractMissingToolsFails(t *testing.T) {
	root := t.TempDir()
	// A zip with neither tool: extraction must refuse rather than report done.
	p := filepath.Join(root, "empty.zip")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("ffmpeg-x/bin/ffplay.exe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := extractTools(p, filepath.Join(root, "bin")); err == nil {
		t.Fatal("an archive without ffmpeg/ffprobe must be refused")
	}
}

// TestPinnedArchiveLayoutExtracts is the layout check that runs on every machine.
// What existed before it depended on a hand-downloaded copy of the 110 MB artifact
// sitting in .gotmp under one exact filename: it ran on the laptop that fetched it
// and skipped on win-devops (seen in that node's own log), and bumping the pin would
// have left it skipping quietly forever. The root folder inside the archive is
// derived here from the same constant that decides what gets downloaded, so the
// fixture moves with the pin and the assumption stays tested.
func TestPinnedArchiveLayoutExtracts(t *testing.T) {
	root := strings.TrimSuffix(path.Base(FFmpegPin.URL), ".zip")
	if !strings.HasPrefix(root, "ffmpeg-") || len(root) < 8 {
		t.Fatalf("the pinned URL %q yields no plausible archive root folder %q", FFmpegPin.URL, root)
	}
	zipPath := zipEntries(t, t.TempDir(), "pinned-layout.zip", []string{
		root + "/bin/ffmpeg.exe",
		root + "/bin/ffprobe.exe",
		root + "/bin/ffplay.exe", // shipped by the vendor, never installed
		root + "/doc/ffmpeg.txt",
		root + "/presets/ffmpeg.spec",
	})
	target := t.TempDir()
	ffmpeg, ffprobe, err := extractTools(zipPath, target)
	if err != nil {
		t.Fatalf("extractTools refused the layout the pin describes: %v", err)
	}
	if ffmpeg != filepath.Join(target, "ffmpeg.exe") || ffprobe != filepath.Join(target, "ffprobe.exe") {
		t.Errorf("extracted to %q / %q, want the two tools under the target root", ffmpeg, ffprobe)
	}
	// Content, not existence: the bytes that landed are the entry the vendor ships
	// under that name, and a zero-length or mis-slotted file would pass a Stat.
	for path0, want := range map[string]string{
		ffmpeg:  "payload-of-" + root + "/bin/ffmpeg.exe",
		ffprobe: "payload-of-" + root + "/bin/ffprobe.exe",
	} {
		b, rerr := os.ReadFile(path0)
		if rerr != nil {
			t.Fatalf("cannot read %s: %v", filepath.Base(path0), rerr)
		}
		if string(b) != want {
			t.Errorf("%s holds %q, want the archive's own bytes %q", filepath.Base(path0), b, want)
		}
	}
	// ffplay is in the vendor's bin/ and is not one of the two tools XCut drives;
	// a selection rule that grew it would show up here as a third file in the
	// install directory. (The first version of this assertion was vacuous — it
	// checked for an absent ffplay.exe while the code's duplicate guard had
	// already skipped it for an unrelated reason, and a mutation adding ffplay to
	// the selection map stayed green. The directory listing is the observable.)
	if fi, lerr := os.ReadDir(target); lerr != nil {
		t.Fatal(lerr)
	} else if len(fi) != 2 {
		names := make([]string, 0, len(fi))
		for _, e := range fi {
			names = append(names, e.Name())
		}
		t.Errorf("the install directory holds %d entries %v, want exactly ffmpeg.exe and ffprobe.exe", len(fi), names)
	}
}

// TestDuplicateToolEntriesKeepTheFirstOne: the vendor archive has exactly one
// ffmpeg.exe, but a malformed or tampered one with two must not silently install
// the second — the code skips a duplicate because the slot is already filled, so
// the *first* entry's bytes are what land. Pinning which one wins is the point.
func TestDuplicateToolEntriesKeepTheFirstOne(t *testing.T) {
	zipPath := zipEntries(t, t.TempDir(), "dupes.zip", []string{
		"ffmpeg-a/bin/ffmpeg.exe",
		"ffmpeg-b/bin/ffmpeg.exe", // a second copy under a different root
		"ffmpeg-a/bin/ffprobe.exe",
	})
	target := t.TempDir()
	ffmpeg, _, err := extractTools(zipPath, target)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(ffmpeg)
	if err != nil {
		t.Fatal(err)
	}
	if want := "payload-of-ffmpeg-a/bin/ffmpeg.exe"; string(b) != want {
		t.Errorf("the installed ffmpeg.exe holds %q, want the first entry's bytes %q", b, want)
	}
}

// TestRealZipLayoutExtracts is the fidelity check against the genuine artifact, and
// it only runs where someone has fetched it. The cache is matched against the pin by
// name: an archive left over from a previous version would prove the layout of that
// version, not this one, so it is reported as a skip naming what was found rather
// than quietly passing as the pinned artifact.
func TestRealZipLayoutExtracts(t *testing.T) {
	root := strings.TrimSuffix(path.Base(FFmpegPin.URL), ".zip")
	want := root + ".zip"
	cached, err := filepath.Glob("../../.gotmp/ffmpeg-*-essentials_build.zip")
	if err != nil {
		t.Fatal(err)
	}
	var used string
	for _, c := range cached {
		if filepath.Base(c) == want {
			used = c
		}
	}
	if used == "" {
		t.Skipf("the pinned archive %s is not cached under .gotmp (present: %v) — one-time download; the derived layout case covers the entry selection", want, cached)
	}
	t.Logf("extracting the genuine artifact: %s", used)
	target := t.TempDir()
	if _, _, err := extractTools(used, target); err != nil {
		t.Fatalf("the real archive does not match the layout the extractor assumes: %v", err)
	}
	for _, tool := range []string{"ffmpeg.exe", "ffprobe.exe"} {
		if fi, serr := os.Stat(filepath.Join(target, tool)); serr != nil || fi.Size() < 1<<20 {
			t.Fatalf("%s missing or implausibly small (%v)", tool, serr)
		}
	}
}
