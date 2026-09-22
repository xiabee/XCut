package render

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/workspace"
	"github.com/xiabee/XCut/internal/xcerr"
)

// The two levels of a music bed's mix, used when the timeline document does not
// name them. A montage convention: the bed carries the rhythm, the location sound
// stays present under it. This is a starting point, not a measured aesthetic —
// both levels are recorded in the document, so a style or a hand edit can set
// others, and the tests below measure what the mix does rather than whether it
// sounds good.
const (
	DefaultMusicGain  = 0.9
	DefaultSourceGain = 0.35
)

// musicGain reads one recorded level, falling back to the default when the key is
// absent or unusable. An out-of-range value is refused rather than clamped: a
// document saying 12 is a mistake someone should see.
func musicGain(metadata map[string]string, key string, def float64) (float64, error) {
	raw, ok := metadata[key]
	if !ok || raw == "" {
		return def, nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, xcerr.E(xcerr.CodeValidation,
			fmt.Sprintf("timeline %s %q is not a number", key, raw), err)
	}
	if v <= 0 || v > 1 {
		return 0, xcerr.E(xcerr.CodeValidation,
			fmt.Sprintf("timeline %s %g out of (0,1]", key, v), nil)
	}
	return v, nil
}

// mixMusic lays the bed named by the timeline under the rendered reel: the bed
// loops, both sides are scaled to their recorded levels, and the pair is mixed to
// the length of the video's own audio. The video stream is copied, so the mix
// costs a decode of the reel's audio and nothing else.
//
// A bed the document names but the disk no longer has is an error, not a silent
// downgrade: the reel the user approved has music in it, and shipping a version
// without one would be a different cut than the one that was accepted.
func mixMusic(ctx context.Context, opts Options, partial, bedPath string, musicLevel, sourceLevel float64, dur float64) error {
	if _, err := os.Stat(bedPath); err != nil {
		return xcerr.E(xcerr.CodeNotFound,
			"the timeline's music file is missing — re-point it or clear the reel's music to render without one", err)
	}
	// The suffix keeps a recognized extension: ffmpeg guesses the muxer from the
	// name, and ".music" is not one it knows.
	mixed := partial + ".music.mp4"
	_ = os.Remove(mixed)
	args := []string{
		"-hide_banner", "-nostdin", "-v", "error", "-y",
		"-i", partial,
		"-stream_loop", "-1", "-i", bedPath,
		"-filter_complex", fmt.Sprintf(
			"[0:a]volume=%s[va];[1:a]volume=%s,asetpts=PTS-STARTPTS[mb];[va][mb]amix=inputs=2:duration=first:dropout_transition=0:normalize=0[a]",
			strconv.FormatFloat(sourceLevel, 'f', 4, 64), strconv.FormatFloat(musicLevel, 'f', 4, 64)),
		"-map", "0:v:0", "-map", "[a]",
		"-c:v", "copy", "-c:a", "aac", "-b:a", "192k",
		"-t", strconv.FormatFloat(dur, 'f', 3, 64),
		mixed,
	}
	callCtx, cancel := context.WithTimeout(ctx, renderBudget(dur))
	defer cancel()
	out, err := media.RunCombined(callCtx, opts.Tools.FFmpeg, args...)
	if err != nil {
		_ = os.Remove(mixed)
		if callCtx.Err() != nil {
			return xcerr.E(xcerr.CodeCancelled, "music mix timed out", callCtx.Err())
		}
		return xcerr.E(xcerr.CodeRenderFailure, ffmpegFailureMessage(out),
			fmt.Errorf("%v: %s", err, media.Tail(out, 500)))
	}
	// The same retry the final publish uses: an indexer holding the file for a
	// moment is not a failed render.
	if err := workspace.RetryableReplace(mixed, partial); err != nil {
		_ = os.Remove(mixed)
		return xcerr.E(xcerr.CodeRenderFailure, "cannot replace the reel with the mixed version", err)
	}
	return nil
}
