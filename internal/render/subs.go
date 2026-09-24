package render

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/workspace"
	"github.com/xiabee/XCut/internal/xcerr"
)

// BurnSubtitles renders inputPath's video through libass with the given
// subtitle file (.ass or .srt — libass reads both) and writes outPath.
// Audio is stream-copied; the video is re-encoded once, with the same
// encoder the base render used (encoder == "" is libx264). The output is
// ffprobe-verified against the input duration and published atomically
// (input == output is fine: the burn lands on a sibling partial first).
func BurnSubtitles(ctx context.Context, tools media.Tools, encoder, inputPath, subsPath, outPath string) error {
	if _, err := os.Stat(inputPath); err != nil {
		return xcerr.E(xcerr.CodeNotFound, "cannot read the rendered video", err)
	}
	if _, err := os.Stat(subsPath); err != nil {
		return xcerr.E(xcerr.CodeNotFound, "subtitle file does not exist: "+subsPath, err)
	}

	inProbe, err := media.ProbeFile(ctx, tools, inputPath)
	if err != nil {
		return xcerr.E(xcerr.CodeRenderFailure, "cannot probe the rendered video", err)
	}

	partial := outPath + ".subs.partial"
	_ = os.Remove(partial)
	args := []string{
		"-hide_banner", "-nostdin", "-v", "error", "-y",
		"-threads", strconv.Itoa(threadCap(tools.Threads)),
		"-i", inputPath,
		"-vf", "subtitles=filename='" + escapeSubsPath(subsPath) + "'",
	}
	args = append(args, encoderVideoArgs(encoder, 20)...)
	args = append(args,
		"-pix_fmt", "yuv420p",
		"-c:a", "copy",
		"-movflags", "+faststart",
		"-f", "mp4", // <out>.subs.partial hides the extension from muxer inference
		partial,
	)
	if _, errOut, err := media.Run(ctx, tools.FFmpeg, args...); err != nil {
		if ctx.Err() != nil {
			return xcerr.E(xcerr.CodeCancelled, "subtitle burn cancelled", ctx.Err())
		}
		return xcerr.E(xcerr.CodeRenderFailure, "subtitle burn failed", fmt.Errorf("%v: %s", err, media.Tail(errOut, 500)))
	}

	// Verify: a burn that silently truncated or dropped streams is not success.
	outProbe, err := media.ProbeFile(ctx, tools, partial)
	if err != nil {
		_ = os.Remove(partial)
		return xcerr.E(xcerr.CodeRenderFailure, "burned output is unreadable", err)
	}
	if d := outProbe.DurationSec - inProbe.DurationSec; d > 1.0 || d < -1.0 {
		_ = os.Remove(partial)
		return xcerr.E(xcerr.CodeRenderFailure,
			fmt.Sprintf("burned output duration drifted (%.2fs vs %.2fs input)", outProbe.DurationSec, inProbe.DurationSec), nil)
	}

	if err := workspace.RetryableReplace(partial, outPath); err != nil {
		_ = os.Remove(partial)
		return xcerr.E(xcerr.CodeRenderFailure, "cannot finalize burned output", err)
	}
	return nil
}

// escapeSubsPath makes a filesystem path safe inside the subtitles filter's
// quoted filename. The filtergraph is parsed twice (graph separator, then
// option separator), so colons must be backslash-escaped even inside single
// quotes — the documented form for Windows drive letters. Backslashes become
// forward slashes (Windows-safe) and quotes are backslash-escaped.
func escapeSubsPath(p string) string {
	p = filepath.ToSlash(p)
	p = strings.ReplaceAll(p, `:`, `\:`)
	p = strings.ReplaceAll(p, `'`, `\'`)
	return p
}
