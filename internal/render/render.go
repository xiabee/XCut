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
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/timeline"
	"github.com/xiabee/XCut/internal/workspace"
	"github.com/xiabee/XCut/internal/xcerr"
)

// Options controls a render run.
type Options struct {
	Tools      media.Tools
	TempDir    string // scratch dir for normalized clips (caller owns lifecycle)
	CRF        int    // x264 quality; 0 → default 20
	OnProgress func(done, total int)
	// TempBudgetBytes caps this render's own scratch (normalized clips).
	// Checked after every clip; 0 disables the check. The caller derives it
	// from resource.max_temp_gb minus current temp/ usage.
	TempBudgetBytes int64
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

	// Validate transitions up front: "cut" joins hard, "fade" dissolves
	// through black (fade-out on this clip + fade-in on the next), "xfade"
	// blends the two clips inside their shared window (needs the xfade
	// combine stage). Anything else is refused rather than silently dropped.
	for _, c := range clips {
		if c.Transition != nil && c.Transition.Type != "cut" && c.Transition.Type != "fade" && c.Transition.Type != "xfade" {
			return xcerr.E(xcerr.CodeRenderFailure,
				fmt.Sprintf("transition %q not supported by the renderer yet", c.Transition.Type), nil)
		}
		if len(c.Effects) > 0 {
			return xcerr.E(xcerr.CodeRenderFailure,
				fmt.Sprintf("effect %q not supported by the renderer yet — refusing to render a timeline that would silently drop it", c.Effects[0]), nil)
		}
	}
	// The renderer joins clips back-to-back from a single ordered stream:
	// audio-kind tracks and multi-track placement are IR constructs it does
	// not honor yet, so refuse them instead of mis-rendering.
	for _, tr := range tl.Tracks {
		if tr.Kind != "video" {
			return xcerr.E(xcerr.CodeRenderFailure,
				fmt.Sprintf("track %q has kind %q — audio tracks are not supported by the renderer yet", tr.ID, tr.Kind), nil)
		}
	}
	if len(tl.Tracks) > 1 {
		return xcerr.E(xcerr.CodeRenderFailure,
			fmt.Sprintf("timeline has %d tracks — multi-track timelines are not supported by the renderer yet", len(tl.Tracks)), nil)
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
	fades := fadePlan(clips)
	parts := make([]string, len(clips))
	for i, c := range clips {
		if err := ctx.Err(); err != nil {
			return xcerr.E(xcerr.CodeCancelled, "render cancelled", err)
		}
		probe, err := media.ProbeFile(ctx, opts.Tools, c.SourcePath)
		if err != nil {
			return xcerr.E(xcerr.CodeRenderFailure, "cannot probe source for clip "+c.ID, err)
		}
		part, err := normalizeClip(ctx, tl, c, i, fades[i], probe.HasAudio, opts)
		if err != nil {
			return err
		}
		parts[i] = part
		if opts.TempBudgetBytes > 0 {
			if used := dirBytes(opts.TempDir); used > opts.TempBudgetBytes {
				return xcerr.E(xcerr.CodeResourceLimit,
					fmt.Sprintf("render scratch exceeded its budget (%s in use, budget %s) — raise resource.max_temp_gb or use a shorter timeline",
						humanBytes(used), humanBytes(opts.TempBudgetBytes)), nil)
			}
		}
		opts.OnProgress(i+1, len(clips)+1)
	}

	// 2. Combine. Timelines containing xfade transitions need a filtergraph
	// (xfade + acrossfade chains); plain timelines use the concat demuxer
	// (stream copy). Both write directly to <out>.partial so the final
	// rename stays on one volume.
	if hasXfade(clips) {
		if err := xfadeCombine(ctx, clips, parts, opts, partial); err != nil {
			return err
		}
	} else if err := concat(ctx, parts, opts, partial); err != nil {
		return err
	}
	opts.OnProgress(len(clips)+1, len(clips)+1)

	// 3. Verify the partial output — exit code 0 is not success (ACCEPTANCE M7).
	if err := verify(ctx, tl, partial, opts.Tools); err != nil {
		return err
	}

	// 4. Atomic publish (retrying through Windows scanner holds — a player
	// or indexer briefly holding the previous output must not fail a render;
	// a long playback is survived via POSIX delete + rename, see
	// RetryableReplace).
	if err := workspace.RetryableReplace(partial, outPath); err != nil {
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

// fadePlan computes per-clip [fadeIn, fadeOut] durations: a "fade" transition
// on clip i dissolves through black — fade-out on clip i, fade-in on clip i+1,
// each half the transition duration, clamped to the clip's own length.
func fadePlan(clips []timeline.Clip) [][2]float64 {
	fades := make([][2]float64, len(clips))
	for i := range clips {
		c := clips[i]
		if c.Transition == nil || c.Transition.Type != "fade" || c.Transition.Duration <= 0 {
			continue
		}
		d := c.Transition.Duration / 2
		if max := c.Duration(); d > max {
			d = max
		}
		fades[i][1] = d
		if i+1 < len(clips) {
			fades[i+1][0] = d
		}
	}
	return fades
}

// normalizeClip re-encodes one clip onto the timeline canvas. sourceHasAudio
// false injects a silent stereo track so every part concatenates uniformly.
// fade = [fadeIn, fadeOut] seconds (0 disables). Clip speed is applied for
// real: setpts compresses/expands video, an atempo chain (each factor kept
// inside atempo's portable [0.5,2] range) does the same for audio — the clip
// shows its FULL source range at the requested pace, never a truncation.
func normalizeClip(ctx context.Context, tl *timeline.Timeline, c timeline.Clip, idx int, fade [2]float64, sourceHasAudio bool, opts Options) (string, error) {
	out := filepath.Join(opts.TempDir, fmt.Sprintf("clip-%04d.mp4", idx))
	dur := c.Duration()

	vf := fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,fps=%s,format=yuv420p",
		tl.Canvas.Width, tl.Canvas.Height, tl.Canvas.Width, tl.Canvas.Height,
		strconv.FormatFloat(tl.Canvas.FPS, 'f', -1, 64))
	af := "aresample=48000,volume=" + strconv.FormatFloat(c.Volume, 'f', 4, 64)
	if c.Speed != 1 {
		// Speed applies before the fps resample so the canvas rate is
		// sampled from the already-time-mapped stream.
		vf = "setpts=PTS/" + strconv.FormatFloat(c.Speed, 'f', 6, 64) + "," + vf
		af = atempoChain(c.Speed) + "," + af
	}

	// "fade" transition halves: dissolve through black at the joined edges.
	if fade[0] > 0 {
		vf += ",fade=t=in:st=0:d=" + strconv.FormatFloat(fade[0], 'f', 3, 64)
		af += ",afade=t=in:st=0:d=" + strconv.FormatFloat(fade[0], 'f', 3, 64)
	}
	if fade[1] > 0 {
		outStart := dur - fade[1]
		if outStart < 0 {
			outStart = 0
		}
		vf += ",fade=t=out:st=" + strconv.FormatFloat(outStart, 'f', 3, 64) +
			":d=" + strconv.FormatFloat(fade[1], 'f', 3, 64)
		af += ",afade=t=out:st=" + strconv.FormatFloat(outStart, 'f', 3, 64) +
			":d=" + strconv.FormatFloat(fade[1], 'f', 3, 64)
	}

	args := []string{
		"-hide_banner", "-nostdin", "-v", "error", "-y",
		"-threads", strconv.Itoa(threadCap(opts.Tools.Threads)),
		// Fast input seek to the clip start; the input -t bounds decode to
		// the exact source range so speeding never reads beyond it.
		"-ss", strconv.FormatFloat(c.SourceStart, 'f', 6, 64),
		"-t", strconv.FormatFloat(c.SourceEnd-c.SourceStart, 'f', 6, 64),
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
		"-af", af,
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
	if diff := absF(probe.DurationSec - want); diff > durationTolerance(want, tl.Canvas.FPS) {
		return xcerr.E(xcerr.CodeRenderFailure,
			fmt.Sprintf("render duration %.2fs too far from timeline %.2fs", probe.DurationSec, want), nil)
	}
	return nil
}

// durationTolerance is the verify() acceptance window: two frames of
// container/encoder jitter plus 5% relative for long content. The old flat
// 0.5s floor let a 2s timeline pass verify at 1.5s — a truncated render
// published as "verified".
func durationTolerance(want, fps float64) float64 {
	return want*0.05 + 2/fps
}

func runFFmpeg(ctx context.Context, bin string, args []string) error {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	out, err := media.RunCombined(cctx, bin, args...)
	if err != nil {
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

// dirBytes sums file sizes under dir (best-effort; 0 when absent). Only used
// for budget accounting, so walk errors are ignored.
func dirBytes(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, ierr := d.Info(); ierr == nil {
				total += fi.Size()
			}
		}
		return nil
	})
	return total
}

// humanBytes renders a byte count for user-facing messages.
func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
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

// hasXfade reports whether any clip joins its successor with an xfade.
func hasXfade(clips []timeline.Clip) bool {
	for i, c := range clips {
		if i < len(clips)-1 && c.Transition != nil && c.Transition.Type == "xfade" && c.Transition.Duration > 0 {
			return true
		}
	}
	return false
}

// buildJoinGraph constructs the filter_complex for joining parts with
// xfade (transition joins) and concat (hard joins), given probed part
// durations. Returns the filter string and the labels of the final video
// and audio streams. Offsets accumulate actual output duration: an xfade
// join consumes d seconds (acc += durs[i] − d), a hard join consumes none
// (acc += durs[i]) — encoder round-off therefore cannot accumulate.
func buildJoinGraph(clips []timeline.Clip, durs []float64) (filter, lastV, lastA string) {
	var v, a []string
	lastV, lastA = "[0:v]", "[0:a]"
	acc := durs[0] // output duration accumulated through join i-1 → i
	for i := 1; i < len(durs); i++ {
		inV, inA := fmt.Sprintf("[%d:v]", i), fmt.Sprintf("[%d:a]", i)
		outV, outA := fmt.Sprintf("[j%d]", i), fmt.Sprintf("[ja%d]", i)
		if d := transitionBetween(clips, i-1); d > 0 {
			// xfade join: offset relative to the chained result is its
			// duration minus the transition window itself.
			offset := acc - d
			if offset < 0 {
				offset = 0
			}
			v = append(v, fmt.Sprintf("%s%sxfade=transition=fade:duration=%s:offset=%s%s",
				lastV, inV, f3(d), f3(offset), outV))
			a = append(a, fmt.Sprintf("%s%sacrossfade=d=%s%s", lastA, inA, f3(d), outA))
			acc = acc + durs[i] - d
		} else {
			// Hard join ("cut" or pre-baked "fade"): the concat filter joins
			// back-to-back without overlap, re-encoding through the shared
			// graph (the concat demuxer cannot run inside a filtergraph).
			// Its pins interleave per segment: v0 a0 v1 a1 → one video + one
			// audio out.
			v = append(v, fmt.Sprintf("%s%s%s%sconcat=n=2:v=1:a=1%s%s",
				lastV, lastA, inV, inA, outV, outA))
			acc = acc + durs[i]
		}
		lastV, lastA = outV, outA
	}
	return strings.Join(append(v, a...), ";"), lastV, lastA
}

// xfadeCombine renders the parts through the join graph (buildJoinGraph) to
// outPath.
func xfadeCombine(ctx context.Context, clips []timeline.Clip, parts []string, opts Options, outPath string) error {
	durs := make([]float64, len(parts))
	for i, p := range parts {
		probe, err := media.ProbeFile(ctx, opts.Tools, p)
		if err != nil {
			return xcerr.E(xcerr.CodeRenderFailure, fmt.Sprintf("cannot probe normalized clip %d", i), err)
		}
		durs[i] = probe.DurationSec
	}

	filter, lastV, lastA := buildJoinGraph(clips, durs)
	args := []string{
		"-hide_banner", "-nostdin", "-v", "error", "-y",
		"-threads", strconv.Itoa(threadCap(opts.Tools.Threads)),
	}
	for _, p := range parts {
		args = append(args, "-i", p)
	}
	args = append(args,
		"-filter_complex", filter,
		"-map", lastV, "-map", lastA,
		"-c:v", "libx264", "-preset", "veryfast", "-crf", strconv.Itoa(opts.CRF),
		"-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", "128k",
		"-movflags", "+faststart",
		"-f", "mp4",
		outPath,
	)
	return runFFmpeg(ctx, opts.Tools.FFmpeg, args)
}

// transitionBetween returns the xfade duration between clips i and i+1 (0 if
// none).
func transitionBetween(clips []timeline.Clip, i int) float64 {
	if i < len(clips) && clips[i].Transition != nil && clips[i].Transition.Type == "xfade" {
		return clips[i].Transition.Duration
	}
	return 0
}

func f3(f float64) string { return strconv.FormatFloat(f, 'f', 3, 64) }

// atempoChain renders a speed factor as a comma-prefixed atempo chain with
// every individual factor inside atempo's portable [0.5, 2] range (older
// ffmpeg builds only accept that window). The chain multiplies out to the
// requested speed within float rounding.
func atempoChain(speed float64) string {
	if speed <= 0 {
		// Validation forbids it; fall back to no-op rather than a broken filter.
		return "atempo=1.000"
	}
	var parts []string
	s := speed
	for s > 2 {
		parts = append(parts, "atempo=2.000")
		s /= 2
	}
	for s < 0.5 {
		parts = append(parts, "atempo=0.500")
		s *= 2
	}
	parts = append(parts, "atempo="+f3(s))
	return strings.Join(parts, ",")
}
