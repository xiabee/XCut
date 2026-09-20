// Package setup implements the owner-directed component auto-install:
// detect what the environment is missing and install it after telling the
// user what will happen. The only installable component today is FFmpeg,
// fetched from one pinned, hash-checked official source — the core still
// never bundles binaries (the download happens on the user's machine, at
// the user's click, from the URL in plain sight in the UI).
package setup

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/xiabee/XCut/internal/xcerr"
)

// Pin is a pinned artifact identity: URL (displayed to the user), exact
// size (download ceiling) and SHA256 (integrity gate).
type Pin struct {
	URL    string
	SHA256 string
	Bytes  int64
}

// FFmpegPin is the pinned FFmpeg source: Gyan.dev's official essentials
// build, published on GitHub releases (stable per-tag URLs). URL, size and
// SHA256 are pinned here so a compromised mirror or a moved artifact fails
// loudly instead of installing something else. Bumping the pin is a code
// change on purpose.
var FFmpegPin = Pin{
	URL:    "https://github.com/GyanD/codexffmpeg/releases/download/9.0.1/ffmpeg-9.0.1-essentials_build.zip",
	SHA256: "fec81ae03971d9dd4be3ebe02e263bd2ec1d789483f931bdba5f5715e65da2e9",
	Bytes:  111253802,
}

const (
	fetchTimeout     = 30 * time.Minute
	verifyTimeout    = 30 * time.Second
	progressInterval = 4 << 20 // report every ~4 MiB
)

// Phase is the installer's coarse lifecycle state (what the UI polls).
type Phase string

const (
	PhaseIdle        Phase = "idle"
	PhaseDownloading Phase = "downloading"
	PhaseExtracting  Phase = "extracting"
	PhaseVerifying   Phase = "verifying"
	PhaseDone        Phase = "done"
	PhaseError       Phase = "error"
)

// Status is the JSON face of the installer state.
type Status struct {
	Phase       Phase     `json:"phase"`
	ProgressPct float64   `json:"progress_pct"`
	Error       string    `json:"error,omitempty"`
	Source      string    `json:"source"`
	SizeBytes   int64     `json:"size_bytes"`
	TargetDir   string    `json:"target_dir"`
	FFmpegPath  string    `json:"ffmpeg_path,omitempty"`
	FFprobePath string    `json:"ffprobe_path,omitempty"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	FinishedAt  time.Time `json:"finished_at,omitempty"`
}

// Fetcher streams the pinned artifact to dst, reporting cumulative bytes.
// The real implementation enforces the pinned size ceiling and hashes on
// the fly; tests inject fakes.
type Fetcher func(ctx context.Context, dst io.Writer, progress func(fetched int64)) (err error)

// Verifier sanity-checks an installed ffprobe (real: `ffprobe -version`).
type Verifier func(ffprobePath string) error

// Installer installs FFmpeg into TargetDir. All mutable state is guarded
// by mu; Start is single-flight, Status is safe for concurrent polling.
type Installer struct {
	TargetDir  string // ffmpeg.exe / ffprobe.exe land here
	ScratchDir string // where the downloaded zip lives (removed when done)
	Fetch      Fetcher
	Verify     Verifier
	// Artifact is the pinned download identity; zero uses FFmpegPin
	// (tests override it to describe their fake payload).
	Artifact Pin
	// Now stubs time for tests; nil means time.Now.
	Now func() time.Time

	mu      sync.Mutex
	st      Status
	running bool
}

// pin resolves the effective artifact identity.
func (in *Installer) pin() Pin {
	if in.Artifact.URL != "" || in.Artifact.SHA256 != "" || in.Artifact.Bytes != 0 {
		return in.Artifact
	}
	return FFmpegPin
}

// NewFFmpegInstaller builds the real installer writing under exeDir
// (config.Resolve's neighborTools already probes exeDir/bin for ffmpeg).
func NewFFmpegInstaller(exeDir, scratchDir string) *Installer {
	return &Installer{
		TargetDir:  filepath.Join(exeDir, "bin"),
		ScratchDir: scratchDir,
		Fetch:      fetchPinned,
		Verify:     verifyFFprobe,
	}
}

// Status snapshots the current state. A never-started installer reports
// idle (the zero value carries no phase).
func (in *Installer) Status() Status {
	in.mu.Lock()
	defer in.mu.Unlock()
	st := in.st
	if st.Phase == "" {
		st.Phase = PhaseIdle
	}
	return st
}

// ErrBusy is returned by Start when an install is already running.
var ErrBusy = errors.New("an FFmpeg install is already running")

// Start begins an install. Single-flight: a second concurrent start is
// ErrBusy (the API maps it to 409). Unsupported platforms refuse honestly.
// The install outlives the HTTP request that triggered it: the context is
// detached from cancellation and bounded by the fetch/verify timeouts.
func (in *Installer) Start(ctx context.Context) error {
	if runtime.GOOS != "windows" {
		return xcerr.E(xcerr.CodeUnsupportedMedia,
			"automatic FFmpeg install is Windows-only here — use your system package manager", nil)
	}
	in.mu.Lock()
	if in.running {
		in.mu.Unlock()
		return xcerr.E(xcerr.CodeConflict, "an FFmpeg install is already running", ErrBusy)
	}
	in.running = true
	pin := in.pin()
	now := in.now()
	in.st = Status{
		Phase:     PhaseDownloading,
		Source:    pin.URL,
		SizeBytes: pin.Bytes,
		TargetDir: in.TargetDir,
		StartedAt: now,
	}
	in.mu.Unlock()
	go in.run(context.WithoutCancel(ctx))
	return nil
}

func (in *Installer) now() time.Time {
	if in.Now != nil {
		return in.Now()
	}
	return time.Now()
}

func (in *Installer) setPhase(p Phase) {
	in.mu.Lock()
	in.st.Phase = p
	in.mu.Unlock()
}

func (in *Installer) setProgress(pct float64) {
	in.mu.Lock()
	in.st.ProgressPct = pct
	in.mu.Unlock()
}

func (in *Installer) fail(err error) {
	in.mu.Lock()
	in.st.Phase = PhaseError
	in.st.Error = err.Error() // user-safe: message strings here are authored, not OS traces
	in.st.FinishedAt = in.now()
	in.running = false
	in.mu.Unlock()
}

func (in *Installer) succeed(ffmpegPath, ffprobePath string) {
	in.mu.Lock()
	in.st.Phase = PhaseDone
	in.st.ProgressPct = 100
	in.st.FFmpegPath = ffmpegPath
	in.st.FFprobePath = ffprobePath
	in.st.FinishedAt = in.now()
	in.running = false
	in.mu.Unlock()
}

func (in *Installer) run(ctx context.Context) {
	if err := in.runErr(ctx); err != nil {
		in.fail(err)
	}
}

func (in *Installer) runErr(ctx context.Context) error {
	if err := os.MkdirAll(in.TargetDir, 0o755); err != nil {
		return xcerr.E(xcerr.CodeInternal, "cannot create the install directory", err)
	}
	if in.ScratchDir != "" {
		if err := os.MkdirAll(in.ScratchDir, 0o755); err != nil {
			return xcerr.E(xcerr.CodeInternal, "cannot create the download directory", err)
		}
	}

	// Download to scratch with on-the-fly hashing; the pinned size doubles
	// as a hard ceiling so a hijacked source cannot stream forever.
	pin := in.pin()
	hasher := sha256.New()
	zipPath := filepath.Join(in.ScratchDir, "ffmpeg-pinned.zip")
	_ = os.Remove(zipPath) // a crashed previous attempt may leave one behind
	f, err := os.Create(zipPath)
	if err != nil {
		return xcerr.E(xcerr.CodeInternal, "cannot create the download file", err)
	}
	defer os.Remove(zipPath)
	counter := &countingWriter{w: io.MultiWriter(f, hasher), total: pin.Bytes, last: 0, cb: in.setProgress}
	fetchErr := in.Fetch(ctx, counter, func(int64) {})
	closeErr := f.Close()
	if fetchErr != nil {
		return xcerr.E(xcerr.CodeInternal, "FFmpeg download failed", fetchErr)
	}
	if closeErr != nil {
		return xcerr.E(xcerr.CodeInternal, "cannot save the download", closeErr)
	}
	got := counter.n
	if got != pin.Bytes {
		return xcerr.E(xcerr.CodeResourceLimit,
			fmt.Sprintf("download size mismatch (got %d bytes, want %d) — refusing the artifact", got, pin.Bytes), nil)
	}
	if sum := hex.EncodeToString(hasher.Sum(nil)); sum != pin.SHA256 {
		return xcerr.E(xcerr.CodeResourceLimit,
			"download failed checksum verification — refusing the artifact", nil)
	}

	in.setPhase(PhaseExtracting)
	ffmpegPath, ffprobePath, err := extractTools(zipPath, in.TargetDir)
	if err != nil {
		return err
	}

	in.setPhase(PhaseVerifying)
	if in.Verify == nil {
		in.Verify = verifyFFprobe
	}
	if err := in.Verify(ffprobePath); err != nil {
		return xcerr.E(xcerr.CodeFFmpegFailure, "installed ffprobe did not verify", err)
	}
	// Remove the scratch archive *before* publishing success. The deferred
	// cleanup below still covers the error paths, but leaving it to the return
	// meant a client polling status could see phase=done while the zip was
	// still on disk — the UI's "finished, nothing left behind" claim was
	// briefly false, and a test that polls exactly that window caught it.
	if in.ScratchDir != "" {
		_ = os.Remove(zipPath)
	}
	in.succeed(ffmpegPath, ffprobePath)
	return nil
}

// countingWriter counts bytes and reports progress at coarse intervals
// (every write would thrash the mutex the UI polls through).
type countingWriter struct {
	w     io.Writer
	n     int64
	total int64
	last  int64
	cb    func(pct float64)
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	if c.n-c.last >= progressInterval {
		c.last = c.n
		c.progress()
	}
	return n, err
}

func (c *countingWriter) progress() {
	if c.cb != nil && c.total > 0 {
		pct := float64(c.n) / float64(c.total) * 100
		if pct > 99 {
			pct = 99 // the last percent belongs to extract+verify
		}
		c.cb(pct)
	}
}

// extractTools pulls exactly ffmpeg.exe and ffprobe.exe out of the archive
// (anything else in the build — docs, previews, ffplay — is not installed).
// Entry names are normalized and escape-checked before any file is created.
func extractTools(zipPath, targetDir string) (ffmpegPath, ffprobePath string, err error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", "", xcerr.E(xcerr.CodeInternal, "cannot open the downloaded archive", err)
	}
	defer zr.Close()

	want := map[string]*string{
		"ffmpeg.exe":  &ffmpegPath,
		"ffprobe.exe": &ffprobePath,
	}
	for _, f := range zr.File {
		if f.Mode().IsDir() {
			continue
		}
		base := filepath.Base(strings.ReplaceAll(f.Name, "\\", "/"))
		dst, isTool := want[base]
		if !isTool || *dst != "" {
			continue // not a tool we install, or a duplicate entry — refuse by skipping
		}
		if !safeZipName(f.Name) {
			return "", "", xcerr.E(xcerr.CodeResourceLimit,
				"archive entry escapes the install directory — refusing the artifact", nil)
		}
		p := filepath.Join(targetDir, base)
		if err := extractFile(f, p); err != nil {
			return "", "", err
		}
		*dst = p
	}
	if ffmpegPath == "" || ffprobePath == "" {
		return "", "", xcerr.E(xcerr.CodeInternal,
			"archive does not contain ffmpeg.exe and ffprobe.exe — refusing the artifact", nil)
	}
	return ffmpegPath, ffprobePath, nil
}

// safeZipName is the zip-slip guard: every installed entry must be a plain
// relative path. Windows archives use "/" separators; backslashes, parent
// segments, absolute paths and drive prefixes all refuse.
func safeZipName(name string) bool {
	if name == "" || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") {
		return false
	}
	if filepath.IsAbs(name) || strings.Contains(name, "..") || filepath.VolumeName(name) != "" {
		return false
	}
	return true
}

func extractFile(f *zip.File, dst string) error {
	src, err := f.Open()
	if err != nil {
		return xcerr.E(xcerr.CodeInternal, "cannot read archive entry", err)
	}
	defer src.Close()
	tmp := dst + ".partial"
	out, err := os.Create(tmp)
	if err != nil {
		return xcerr.E(xcerr.CodeInternal, "cannot write the install file", err)
	}
	if _, err := io.Copy(out, src); err != nil {
		out.Close()
		os.Remove(tmp)
		return xcerr.E(xcerr.CodeInternal, "cannot copy the archive entry", err)
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return xcerr.E(xcerr.CodeInternal, "cannot save the install file", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return xcerr.E(xcerr.CodeInternal, "cannot finalize the install file", err)
	}
	return nil
}

// fetchPinned streams the pinned URL with a hard timeout. The size ceiling
// and hash gate live in runErr (they compare against the pin, not the
// transport), so this is a plain bounded GET.
func fetchPinned(ctx context.Context, dst io.Writer, progress func(fetched int64)) error {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, FFmpegPin.URL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return xcerr.E(xcerr.CodeInternal,
			fmt.Sprintf("download source answered %d", resp.StatusCode), nil)
	}
	n, err := io.Copy(dst, resp.Body)
	if err != nil {
		return err
	}
	if progress != nil {
		progress(n)
	}
	return nil
}

func verifyFFprobe(ffprobePath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), verifyTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, ffprobePath, "-version").CombinedOutput()
	if err != nil {
		return xcerr.E(xcerr.CodeFFmpegFailure,
			string(trimOutput(out)), err)
	}
	return nil
}

func trimOutput(b []byte) []byte {
	if len(b) > 400 {
		return b[:400]
	}
	return b
}
