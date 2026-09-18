package setup

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	p := filepath.Join(dir, "fake-ffmpeg.zip")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	entries := append([]string{
		"ffmpeg-x/bin/ffmpeg.exe",
		"ffmpeg-x/bin/ffprobe.exe",
		"ffmpeg-x/bin/ffplay.exe", // not installed — only the two tools are
		"ffmpeg-x/README.txt",
	}, extra...)
	for _, name := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte("payload-of-" + name)); err != nil {
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

func TestInstallHappyPath(t *testing.T) {
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
}

func TestInstallSingleFlight(t *testing.T) {
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

func TestRealZipLayoutExtracts(t *testing.T) {
	if _, err := os.Stat("../../.gotmp/ffmpeg-9.0.1-essentials_build.zip"); err != nil {
		t.Skip("pinned-archive copy not present on this machine (one-time download)")
	}
	// This is the only test that opens the real 110 MB artifact; it proves
	// the entry-selection logic against the genuine layout (build dir name,
	// bin/ prefix, ffplay present). Skipped on machines without the file —
	// CI does not fetch third-party artifacts.
	target := t.TempDir()
	_, _, err := extractTools("../../.gotmp/ffmpeg-9.0.1-essentials_build.zip", target)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"ffmpeg.exe", "ffprobe.exe"} {
		if fi, err := os.Stat(filepath.Join(target, tool)); err != nil || fi.Size() < 1<<20 {
			t.Fatalf("%s missing or implausibly small (%v)", tool, err)
		}
	}
}
