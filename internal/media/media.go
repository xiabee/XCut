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
	"os/exec"
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
// binaries run; use ProbeVersion for that.
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
	return t
}

var versionRe = regexp.MustCompile(`^(?:ffmpeg|ffprobe) version (\S+)`)

// Version runs `<bin> -version` and returns the parsed version string.
func Version(ctx context.Context, bin string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, "-version")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
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

// cappedBuffer retains the LAST max bytes written to it.
type cappedBuffer struct {
	b   []byte
	max int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
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
func Run(ctx context.Context, bin string, args ...string) (stdout, stderr []byte, err error) {
	if bin == "" {
		return nil, nil, xcerr.E(xcerr.CodeInternal, "empty binary path", nil)
	}
	release, err := acquire(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer release()
	cmd := exec.CommandContext(ctx, bin, args...)
	outBuf := &cappedBuffer{max: maxCapturedOutput}
	errBuf := &cappedBuffer{max: maxCapturedOutput}
	cmd.Stdout = outBuf
	cmd.Stderr = errBuf
	if err := cmd.Run(); err != nil {
		return outBuf.b, errBuf.b, err
	}
	return outBuf.b, errBuf.b, nil
}
