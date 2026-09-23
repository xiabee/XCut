// Package media wraps external FFmpeg/ffprobe execution.
//
// Security contract for this package and all callers:
//   - binaries are invoked via exec.CommandContext with an explicit argument
//     vector — never through a shell, never via string concatenation;
//   - every invocation carries a context timeout;
//   - user-controlled text may only ever appear as a single argument value.
package media

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/xiabee/XCut/internal/config"
	"github.com/xiabee/XCut/internal/xcerr"
)

// Tools bundles resolved external binary paths.
type Tools struct {
	FFmpeg  string // path or bare name (PATH lookup at exec time)
	FFprobe string
	Threads int // per-process -threads cap; <=0 means "let ffmpeg decide"
}

// ResolveTools applies config to environment lookups. It does not verify the
// binaries run; use ProbeVersion for that. When nothing is configured and
// PATH has nothing either, the exe-neighbor locations are re-probed live —
// tools can appear mid-session (the component installer lands them in
// <exe>/bin), and resolution must not stay frozen at process start.
func ResolveTools(cfg *config.Config) Tools {
	t := Tools{
		FFmpeg:  cfg.FFmpeg.Bin,
		FFprobe: cfg.FFmpeg.ProbeBin,
		Threads: cfg.Resource.FFmpegThreads,
	}
	if t.FFmpeg == "" {
		t.FFmpeg = "ffmpeg"
	}
	if t.FFprobe == "" {
		t.FFprobe = "ffprobe"
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		if _, err := exec.LookPath(t.FFmpeg); err != nil {
			if p, ok := config.NeighborBin(dir, "ffmpeg"); ok {
				t.FFmpeg = p
			}
		}
		if _, err := exec.LookPath(t.FFprobe); err != nil {
			if p, ok := config.NeighborBin(dir, "ffprobe"); ok {
				t.FFprobe = p
			}
		}
	}
	return t
}

var versionRe = regexp.MustCompile(`^(?:ffmpeg|ffprobe) version (\S+)`)

// Version runs `<bin> -version` and returns the parsed version string.
func Version(ctx context.Context, bin string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	wbin, wargs := wrapChild(bin, "-version")
	cmd := exec.CommandContext(ctx, wbin, wargs...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		return "", xcerr.E(xcerr.CodeFFmpegFailure, "cannot execute "+bin, err)
	}
	attachJob(cmd.Process)
	if err := cmd.Wait(); err != nil {
		return "", xcerr.E(xcerr.CodeFFmpegFailure, "cannot execute "+bin, err)
	}
	m := versionRe.FindStringSubmatch(strings.TrimSpace(out.String()))
	if m == nil {
		return "", xcerr.E(xcerr.CodeFFmpegFailure, "unrecognized "+bin+" version output", nil)
	}
	return m[1], nil
}

// maxCapturedOutput caps how much child stdout/stderr is retained (last
// bytes win — ffmpeg prints its error summary last). Diagnostics only ever
// reach users through tail-limited excerpts, so the cap costs nothing and
// stops a chatty stderr (per-packet decode errors from corrupt media) from
// growing host memory for the child's whole runtime.
const maxCapturedOutput = 1 << 20

// Tail keeps the last n bytes of a child's captured output — the half that
// carries the diagnosis, because FFmpeg prints its error summary last. It is the
// one implementation of that rule: render, analysis and worker each kept a
// private copy, so a fix to where the excerpt is taken from would have applied
// to one call site and silently not the others.
func Tail(b []byte, n int) []byte {
	if len(b) > n {
		return b[len(b)-n:]
	}
	return b
}

// stdoutCaptureCap is the stdout budget for Run. A caller whose tool writes
// parseable data to stdout must either stay under it or use StreamStdout —
// silently keeping only the last N bytes of a data stream once produced
// feature tracks that quietly covered only the tail of long media. Var so
// tests can exercise the overflow path cheaply.
var stdoutCaptureCap = maxCapturedOutput

// cappedBuffer retains the LAST max bytes written to it and reports whether
// anything was dropped (overflowed — the retained bytes are a tail, not the
// full stream).
type cappedBuffer struct {
	b          []byte
	max        int
	total      int64
	overflowed bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	c.total += int64(len(p))
	if c.total > int64(c.max) {
		c.overflowed = true
	}
	if len(p) >= c.max {
		c.b = append(c.b[:0], p[len(p)-c.max:]...)
		return len(p), nil
	}
	c.b = append(c.b, p...)
	if over := len(c.b) - c.max; over > 0 {
		c.b = append(c.b[:0], c.b[over:]...)
	}
	return len(p), nil
}

// Run executes an ffmpeg/ffprobe-style tool with args under ctx, capturing
// capped stdout/stderr. It enforces the package security contract and runs
// under the global process limiter (resource.max_ffmpeg_processes).
// Stderr truncation is fine (diagnostics, tail-kept); stdout truncation is
// NOT — Run fails loudly instead of handing back a silently halved stream.
func Run(ctx context.Context, bin string, args ...string) (stdout, stderr []byte, err error) {
	if bin == "" {
		return nil, nil, xcerr.E(xcerr.CodeInternal, "empty binary path", nil)
	}
	release, err := acquire(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer release()
	// The user-facing name stays the tool's, never the sandbox wrapper's, so every
	// message below reads "cannot execute ffprobe" and not "cannot execute systemd-run".
	wbin, wargs := wrapChild(bin, args...)
	cmd := exec.CommandContext(ctx, wbin, wargs...)
	outBuf := &cappedBuffer{max: stdoutCaptureCap}
	errBuf := &cappedBuffer{max: maxCapturedOutput}
	cmd.Stdout = outBuf
	cmd.Stderr = errBuf
	// Explicit Start/Wait (not Run) so the process is alive when it joins
	// the kill-on-close job object — see attachJob.
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	attachJob(cmd.Process)
	if err := cmd.Wait(); err != nil {
		return outBuf.b, errBuf.b, err
	}
	if outBuf.overflowed {
		return nil, errBuf.b, xcerr.E(xcerr.CodeResourceLimit,
			"tool stdout exceeded the capture budget — parse it with StreamStdout instead", nil)
	}
	return outBuf.b, errBuf.b, nil
}
