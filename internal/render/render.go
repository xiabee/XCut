// Package render turns a validated Timeline into a real MP4 via FFmpeg.
//
// Strategy (DECISIONS D10): normalize every clip to the timeline canvas
// (H.264 yuv420p, constant fps, AAC 48 kHz stereo), concatenate with the
// concat demuxer (stream copy), then verify the result with ffprobe before
// an atomic rename. "FFmpeg exited 0" is never accepted as success by itself.
//
// Security: all invocations are arg-vector exec with timeouts and thread caps
// (SECURITY.md). Output goes to <out>.partial first; a failed render can
// never leave a file that looks finished.
package render

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/timeline"
	"github.com/xiabee/XCut/internal/xcerr"
)

// Options controls a render run.
type Options struct {
	Tools    media.Tools
	TempDir  string // scratch dir for normalized clips (caller owns lifecycle)
	CRF      int    // x264 quality; 0 → default 20
	OnProgress func(done, total int)
}

// Render executes the timeline to outPath. The output appears atomically
// only after ffprobe verification passes. On error, <outPath>.partial may
// remain for debugging; no final file is ever created on failure.
func Render(ctx context.Context, tl *timeline.Timeline, opts Options, outPath string) error {
	if tl == nil {
		return xcerr.E(xcerr.CodeValidation, "nil timeline", nil)
	}
	if err := tl.Validate(nil); err != nil {
		return err
	}
	clips := collectClips(tl)
	if len(clips) == 0 {
		return xcerr.E(xcerr.CodeValidation, "timeline has no clips", nil)
	}
	if opts.CRF <= 0 {
		opts.CRF = 20
	}
	if opts.OnProgress == nil {
		opts.OnProgress = func(int, int) {}
	}

	// Refuse unsupported transitions instead of silently dropping them.
	for _, c := range clips {
		if c.Transition != nil && c.Transition.Type != "cut" {
			return xcerr.E(xcerr.CodeRenderFailure,
				fmt.Sprintf("transition %q not supported by renderer yet", c.Transition.Type), nil)
		}
	}

	// Pre-check all sources exist before starting any work.
	for _, c := range clips {
		if c.SourcePath == "" {
			return xcerr.E(xcerr.CodeValidation, fmt.Sprintf("clip %s has no source path", c.ID), nil)
		}
		if _, err := os.Stat(c.SourcePath); err != nil {
			return xcerr.E(xcerr.CodeNotFound, "source missing for clip "+c.ID, err)
		}
	}

	partial := outPath + ".partial"
	_ = os.Remove(partial)
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return xcerr.E(xcerr.CodeRenderFailure, "cannot create output directory", err)
	}

	// 1. Normalize each clip. Sources are probed once so clips without an
	// audio stream still produce a (silent) audio track — concat requires
	// uniform stream layouts across parts.
	parts := make([]string, len(clips))
	for i, c := range clips {
		if err := ctx.Err(); err != nil {
			return xcerr.E(xcerr.CodeCancelled, "render cancelled", err)
		}
		probe, err := media.ProbeFile(ctx, opts.Tools, c.SourcePath)
		if err != nil {
			return xcerr.E(xcerr.CodeRenderFailure, "cannot probe source for clip "+c.ID, err)
		}
		part, err := normalizeClip(ctx, tl, c, i, probe.HasAudio, opts)
		if err != nil {
			return err
		}
		parts[i] = part
		opts.OnProgress(i+1, len(clips)+1)
	}

	// 2. Concat (stream copy) directly to <out>.partial so the final rename
	// stays on one volume.
	if err := concat(ctx, parts, opts, partial); err != nil {
		return err
	}
	opts.OnProgress(len(clips)+1, len(clips)+1)

	// 3. Verify the partial output — exit code 0 is not success (ACCEPTANCE M7).
	if err := verify(ctx, tl, partial, opts.Tools); err != nil {
		return err
	}

	// 4. Atomic publish.
	if err := os.Rename(partial, outPath); err != nil {
		return xcerr.E(xcerr.CodeRenderFailure, "cannot finalize output file", err)
	}
	return nil
}

func collectClips(tl *timeline.Timeline) []timeline.Clip {
	var out []timeline.Clip
	for _, tr := range tl.Tracks {
		out = append(out, tr.Clips...)
	}
	return out
}

// normalizeClip re-encodes one clip onto the timeline canvas. sourceHasAudio
// false injects a silent stereo track so every part concatenates uniformly.
func normalizeClip(ctx context.Context, tl *timeline.Timeline, c timeline.Clip, idx int, sourceHasAudio bool, opts Options) (string, error) {
	out := filepath.Join(opts.TempDir, fmt.Sprintf("clip-%04d.mp4", idx))
	dur := c.Duration()

	vf := fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,fps=%s,format=yuv420p",
		tl.Canvas.Width, tl.Canvas.Height, tl.Canvas.Width, tl.Canvas.Height,
		strconv.FormatFloat(tl.Canvas.FPS, 'f', -1, 64))

	args := []string{
		"-hide_banner", "-nostdin", "-v", "error", "-y",
		"-threads", strconv.Itoa(threadCap(opts.Tools.Threads)),
		// Fast input seek to the clip start; -t bounds the output length.
		"-ss", strconv.FormatFloat(c.SourceStart, 'f', 6, 64),
		"-i", c.SourcePath,
	}
	audioArgs := []string{}
	if !sourceHasAudio {
		args = append(args, "-f", "lavfi", "-i", "anullsrc=channel_layout=stereo:sample_rate=48000")
		audioArgs = append(audioArgs, "-map", "0:v", "-map", "1:a", "-shortest")
	}
	args = append(args,
		"-t", strconv.FormatFloat(dur, 'f', 6, 64),
		"-vf", vf,
	)
	// Volume 0 keeps the audio track (a muted clip stays uniform for concat).
	args = append(args,
		"-af", "aresample=48000,volume="+strconv.FormatFloat(c.Volume, 'f', 4, 64),
		"-c:a", "aac", "-b:a", "128k", "-ac", "2", "-ar", "48000",
	)
	args = append(args, audioArgs...)
	args = append(args,
		"-c:v", "libx264", "-preset", "veryfast", "-crf", strconv.Itoa(opts.CRF),
		"-pix_fmt", "yuv420p",
		"-video_track_timescale", "90000",
		out,
	)

	runErr := runFFmpeg(ctx, opts.Tools.FFmpeg, args)
	if runErr != nil {
		_ = os.Remove(out)
		return "", runErr
	}
	return out, nil
}

// concat joins normalized clips with the concat demuxer (stream copy).
func concat(ctx context.Context, parts []string, opts Options, outPath string) error {
	listPath := filepath.Join(opts.TempDir, "concat.txt")
	var b strings.Builder
	for _, p := range parts {
		b.WriteString("file '")
		b.WriteString(escapeConcatPath(p))
		b.WriteString("'\n")
	}
	if err := os.WriteFile(listPath, []byte(b.String()), 0o644); err != nil {
		return xcerr.E(xcerr.CodeRenderFailure, "cannot write concat list", err)
	}

	err := runFFmpeg(ctx, opts.Tools.FFmpeg, []string{
		"-hide_banner", "-nostdin", "-v", "error", "-y",
		"-f", "concat", "-safe", "0", "-i", listPath,
		"-c", "copy",
		"-movflags", "+faststart",
		"-f", "mp4", // <out>.partial hides the extension from muxer inference
		outPath,
	})
	if err != nil {
		return err
	}
	return nil
}

// escapeConcatPath makes a path safe inside the concat demuxer list format:
// forward slashes (accepted by ffmpeg on Windows) and quoted single quotes.
func escapeConcatPath(p string) string {
	p = strings.ReplaceAll(p, `\`, `/`)
	return strings.ReplaceAll(p, `'`, `'\''`)
}

// verify probes the rendered file and enforces output expectations.
func verify(ctx context.Context, tl *timeline.Timeline, path string, tools media.Tools) error {
	probe, err := media.ProbeFile(ctx, tools, path)
	if err != nil {
		return xcerr.E(xcerr.CodeRenderFailure, "render output cannot be probed", err)
	}
	if probe.VideoCodec == "" {
		return xcerr.E(xcerr.CodeRenderFailure, "render output has no video stream", nil)
	}
	if probe.Width != tl.Canvas.Width || probe.Height != tl.Canvas.Height {
		return xcerr.E(xcerr.CodeRenderFailure,
			fmt.Sprintf("render size %dx%d does not match canvas %dx%d",
				probe.Width, probe.Height, tl.Canvas.Width, tl.Canvas.Height), nil)
	}
	want := tl.Duration()
	tol := 0.5 + want*0.05
	if diff := absF(probe.DurationSec - want); diff > tol {
		return xcerr.E(xcerr.CodeRenderFailure,
			fmt.Sprintf("render duration %.2fs too far from timeline %.2fs", probe.DurationSec, want), nil)
	}
	return nil
}

func runFFmpeg(ctx context.Context, bin string, args []string) error {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		if cctx.Err() != nil {
			return xcerr.E(xcerr.CodeRenderFailure, "render timed out or was cancelled", cctx.Err())
		}
		return xcerr.E(xcerr.CodeRenderFailure, "ffmpeg failed",
			fmt.Errorf("%v: %s", err, tail(out, 500)))
	}
	return nil
}

func threadCap(n int) int {
	if n <= 0 {
		return 2
	}
	return n
}

func absF(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func tail(b []byte, n int) string {
	if len(b) > n {
		return string(b[len(b)-n:])
	}
	return string(b)
}
