package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/xiabee/XCut/internal/analysis"
	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/render"
	"github.com/xiabee/XCut/internal/style"
	"github.com/xiabee/XCut/internal/timeline"
	"github.com/xiabee/XCut/internal/xcerr"
)

// DefaultBeatSnap is how far a cut may travel to reach the beat of a bed the user
// named: well inside half the shortest common shot in a montage and below a
// clip's own minimum, so a snap adjusts a cut instead of re-timing it.
const DefaultBeatSnap = 0.12

// musicBed is the track a run lays under its reel, together with what the
// pipeline learned from it: the grid the cuts snap to and the two mix levels the
// render will use.
type musicBed struct {
	path       string
	beats      []float64
	bpm        float64
	musicGain  float64
	sourceGain float64
}

// prepareBed resolves --music. The bed is analyzed with the shipped onset
// analyzer through the same cache as any asset, so re-cutting a project does not
// re-decode it.
//
// Two outcomes are deliberately different. A file with no audio stream is
// refused — the request cannot be honored at all. A file with audio but no
// believable grid is not: the music still plays and the cuts stay where the length
// rules put them, because a grid is something the estimator has to earn
// (analysis.EstimateBeatGrid), and inventing one would move cuts to a pulse the
// track never had.
func (d Deps) prepareBed(ctx context.Context, req TimelineRequest, preset *style.Preset,
	store *analysis.Store, opts analysis.Options) (*musicBed, error) {
	if strings.TrimSpace(req.Music) == "" {
		return nil, nil
	}
	abs, err := filepath.Abs(req.Music)
	if err != nil {
		return nil, xcerr.E(xcerr.CodeValidation, "cannot resolve path: "+req.Music, err)
	}
	// Existence is refused at the request boundary (TimelineRequest.validate),
	// before a job is queued; what is left here is the reading of the file.
	dur, err := probeBed(ctx, d.tools(), abs)
	if err != nil {
		return nil, err
	}
	fp, err := media.Fingerprint(abs)
	if err != nil {
		return nil, err
	}
	res, err := analysis.Run(ctx, store, opts, []analysis.Analyzer{analysis.AudioOnsetAnalyzer{}},
		abs, fp, dur, true, d.Log)
	if err != nil {
		return nil, err
	}
	var onsets []float64
	for _, tr := range res.Tracks {
		if tr.Kind == "audio_onset" {
			for _, s := range tr.Samples {
				onsets = append(onsets, s.T)
			}
		}
	}
	bed := &musicBed{
		path:       abs,
		musicGain:  pickGain(preset.Audio.MusicGain, render.DefaultMusicGain),
		sourceGain: pickGain(preset.Audio.SourceGain, render.DefaultSourceGain),
	}
	grid, ok := analysis.EstimateBeatGrid(onsets, dur)
	if !ok {
		d.Log.Info("the music bed carries no beat grid the estimator will believe; cuts stay where the length rules put them",
			"music", filepath.Base(abs), "onsets", len(onsets))
		return bed, nil
	}
	bed.beats = grid.Beats
	bed.bpm = grid.BPM
	// Asking for a bed asks for the cut to follow it, so a run that named a track
	// and no tolerance gets the product's default snap. req.BeatSnap has already
	// been applied to the preset by the time this runs, so "nothing set" here means
	// neither the caller nor the style named one; `--beat-snap off` stays the
	// explicit "music, but leave my cuts where the length rules put them".
	if preset.BeatSnapTolerance <= 0 && req.BeatSnap == 0 {
		if DefaultBeatSnap < preset.MinClipDuration {
			preset.BeatSnapTolerance = DefaultBeatSnap
			d.Log.Info("cutting to the music bed", "tolerance", DefaultBeatSnap, "bpm", grid.BPM)
		} else {
			d.Log.Info("the music bed's grid is not being snapped to: the tolerance would reach a whole minimum clip",
				"min_clip_duration", preset.MinClipDuration)
		}
	}
	d.Log.Info("music bed beat grid estimated", "music", filepath.Base(abs), "bpm", grid.BPM,
		"coverage", grid.Coverage, "beats", len(grid.Beats), "onsets", len(onsets))
	return bed, nil
}

// probeBed asks ffprobe for the two things a bed needs: a duration, and an audio
// stream to hear. media.ProbeFile is deliberately not reused — it refuses a file
// with no video, which is right for an imported asset and wrong for a music track.
func probeBed(ctx context.Context, tools media.Tools, path string) (float64, error) {
	out, err := media.RunCombined(ctx, tools.FFprobe, "-v", "error",
		"-show_entries", "stream=codec_type", "-show_entries", "format=duration",
		"-of", "json", path)
	if err != nil {
		return 0, xcerr.E(xcerr.CodeUnsupportedMedia, "cannot read the music file",
			fmt.Errorf("%v: %s", err, string(media.Tail(out, 300))))
	}
	var parsed struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return 0, xcerr.E(xcerr.CodeUnsupportedMedia, "cannot read the music file", err)
	}
	audio := false
	for _, st := range parsed.Streams {
		if st.CodecType == "audio" {
			audio = true
		}
	}
	if !audio {
		return 0, xcerr.E(xcerr.CodeValidation,
			"the music file has no audio stream to cut to or mix in", nil)
	}
	d, cerr := strconv.ParseFloat(strings.TrimSpace(parsed.Format.Duration), 64)
	if cerr != nil || d <= 0 {
		return 0, xcerr.E(xcerr.CodeUnsupportedMedia,
			"the music file reports no playable duration", cerr)
	}
	return d, nil
}

// pickGain keeps the preset's level when it named one and the renderer's starting
// mix when it did not.
func pickGain(set *float64, def float64) float64 {
	if set != nil {
		return *set
	}
	return def
}

// stampBed records the bed on the document so the render mixes it without being
// told twice, and so a reader can see what the cut was made against.
func stampBed(tl *timeline.Timeline, bed *musicBed) {
	if bed == nil {
		return
	}
	if tl.Metadata == nil {
		tl.Metadata = map[string]string{}
	}
	tl.Metadata[timeline.MetaMusic] = bed.path
	tl.Metadata[timeline.MetaMusicGain] = strconv.FormatFloat(bed.musicGain, 'f', 4, 64)
	tl.Metadata[timeline.MetaSourceGain] = strconv.FormatFloat(bed.sourceGain, 'f', 4, 64)
	if bed.bpm > 0 {
		tl.Metadata[timeline.MetaMusicBPM] = strconv.FormatFloat(bed.bpm, 'f', 2, 64)
	}
}

// bedWins reports the beats a clip should snap to: the reel is cut to the music
// laid under it, so a bed's grid outranks the location audio's own — which is the
// measured reason the bed exists at all (docs/EVAL.md, "Cutting on the beat": a
// match's crowd carries no grid worth believing, so the pulse has to come from the
// track).
func bedWins(bed *musicBed, assetBeats []float64) []float64 {
	if bed != nil && len(bed.beats) > 0 {
		return bed.beats
	}
	return assetBeats
}
