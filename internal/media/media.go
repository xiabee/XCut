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

// Run executes an ffmpeg/ffprobe-style tool with args under ctx, capturing
// combined output. It enforces the package security contract.
func Run(ctx context.Context, bin string, args ...string) (stdout, stderr []byte, err error) {
	if bin == "" {
		return nil, nil, xcerr.E(xcerr.CodeInternal, "empty binary path", nil)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return outBuf.Bytes(), errBuf.Bytes(), err
	}
	return outBuf.Bytes(), errBuf.Bytes(), nil
}
