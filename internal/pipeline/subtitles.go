package pipeline

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xiabee/XCut/internal/job"
	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/subs"
	"github.com/xiabee/XCut/internal/timeline"
	"github.com/xiabee/XCut/internal/worker"
	"github.com/xiabee/XCut/internal/xcerr"
)

// Speech-to-text subtitles are an AI capability (DECISIONS D3): the core
// never ships or downloads models, so transcription runs through the
// configured AI sidecar and lands as project artifacts — subtitles.srt plus
// subtitles.ass whenever the transcript has text to show (karaoke when the
// sidecar timed the words, styled captions when it timed only the lines).
// Everything here is honest about a missing capability: it is a setup gap with
// a hint, never a silent empty result.

// SubtitlesPath is where a project's subtitles live (ext: "srt" or "ass").
func (d Deps) SubtitlesPath(projectID, ext string) (string, error) {
	return d.WS.SafeJoin(filepath.Join("projects", projectID, "subtitles."+ext))
}

// transcriptPath is where the project's stored transcript lives. It is deliberately
// not a subtitles.* name: ResolveSubtitlesPath walks caption extensions, and a JSON
// blob must never be a candidate to burn into the picture.
func (d Deps) transcriptPath(projectID string) (string, error) {
	return d.WS.SafeJoin(filepath.Join("projects", projectID, "transcript.json"))
}

// storedTranscript is the transcript an earlier transcription left behind, read back
// through the same validation the sidecar's answer went through. Anything that cannot
// be laid out again — missing, unreadable, invalid — reads as "there is none", which is
// what the export then reports it has, rather than a tap failing over a file it never
// needed in the first place.
func (d Deps) storedTranscript(projectID string) *subs.Transcript {
	p, err := d.transcriptPath(projectID)
	if err != nil {
		return nil
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	t, err := subs.Parse(raw)
	if err != nil {
		return nil
	}
	return t
}

// writeStyledSubtitles lays a transcript out as the project's styled caption file, and
// reports whether one was written. One function holds the rule because two callers must
// not disagree about it: transcription writes this file, and a reel that changed shape
// re-lays the same words out — a restyle that derived the style differently would leave
// the export reading its own output as a mismatch.
//
// The caption box is laid out against the reel's own canvas, not against a reference the
// file invents: libass scales the whole script by PlayRes, so a 9:16 reel needs a 9:16
// style or the text lands at the size and position meant for a different shape. With no
// timeline yet there is no canvas to match, and the writer's shipped 1280×720 reference
// stands.
func (d Deps) writeStyledSubtitles(projectID string, t *subs.Transcript) (bool, error) {
	assPath, err := d.SubtitlesPath(projectID, "ass")
	if err != nil {
		return false, err
	}
	style := subs.KaraokeStyle{}
	if tp, terr := d.TimelinePath(projectID); terr == nil {
		if tl, lerr := timeline.LoadFile(tp); lerr == nil {
			style.Width, style.Height = tl.Canvas.Width, tl.Canvas.Height
		}
	}
	var ass strings.Builder
	switch {
	case t.HasWordTimings():
		if err := subs.WriteKaraokeASS(t, style, &ass); err != nil {
			return false, err
		}
	case len(t.Segments) == 0:
		// Nothing to lay out. An empty [Events] section would tell a later
		// burn to draw nothing at all over the picture.
	default:
		// Missing word timings is not "no captions", only "no karaoke": a
		// sidecar that knows where each line falls gets the same frame, the
		// same wrap and the same dwell as one that knows each syllable.
		if err := subs.WriteCaptionASS(t, style, &ass); err != nil {
			return false, err
		}
	}
	if ass.Len() > 0 {
		if err := WriteAtomic(assPath, []byte(ass.String())); err != nil {
			return false, err
		}
		return true, nil
	}
	// Stale karaoke file must not outlive its data: resolution
	// prefers .ass, so a scanner-blocked remove gets a short retry.
	// If the file survives the retries it MUST be loud: any later
	// subs-burn would publish the OLD karaoke content over the new
	// transcript ("never a silent empty result" — this is the
	// silent-wrong variant).
	for i := 0; i < 4; i++ {
		if os.Remove(assPath) == nil {
			return false, nil
		}
		if _, statErr := os.Stat(assPath); statErr != nil {
			return false, nil // already gone
		}
		time.Sleep(50 * time.Millisecond)
	}
	d.Log.Warn("stale karaoke .ass survived removal — it still shadows the fresh .srt on burn; remove it manually or re-run transcription",
		"path", assPath)
	return false, nil
}

// restyleSubtitles lays the project's stored transcript out again for the canvas its
// timeline now declares — the caption fix a sidecar would have made, without the
// sidecar or the minutes of transcription it costs.
func (d Deps) restyleSubtitles(projectID string) error {
	t := d.storedTranscript(projectID)
	if t == nil {
		return xcerr.E(xcerr.CodeNotFound,
			"no stored transcript to lay out again (transcribe first)", nil)
	}
	_, err := d.writeStyledSubtitles(projectID, t)
	return err
}

// TranscribeProjectAsync runs speech-to-text over one of the project's
// assets through the AI sidecar (recorded job, non-blocking). assetID ""
// selects the project's first asset.
func (d Deps) TranscribeProjectAsync(project *storage.Project, assetID string) (string, error) {
	if assetID != "" {
		a, err := d.DB.GetAsset(d.Ctx, assetID)
		if err != nil {
			return "", err
		}
		if a == nil || a.ProjectID != project.ID {
			return "", xcerr.E(xcerr.CodeNotFound, "unknown asset for this project", nil)
		}
	}
	return d.Queue.RunAsync(d.Ctx, job.TypeSubtitles, project.ID, job.ClassCPULight,
		map[string]any{"asset": assetID}, d.subtitlesBody(project, assetID))
}

// TranscribeProject is the blocking variant, for the one-shot CLI path where the
// captions are a step of the command and not a job the user watches. It records the
// same job row, so `xcut jobs` tells the same story either way.
func (d Deps) TranscribeProject(project *storage.Project, assetID string) error {
	_, err := d.Queue.RunInline(d.Ctx, job.TypeSubtitles, project.ID, job.ClassCPULight,
		map[string]any{"asset": assetID}, d.subtitlesBody(project, assetID))
	return err
}

// ResolveSubtitlesPath returns the project's subtitle file for burn-in:
// styled ASS (karaoke or captions) when present, plain SRT otherwise.
// Not-found when the project has none yet.
func (d Deps) ResolveSubtitlesPath(projectID string) (string, error) {
	for _, ext := range []string{"ass", "srt"} {
		p, err := d.SubtitlesPath(projectID, ext)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", xcerr.E(xcerr.CodeNotFound,
		"no subtitles for this project (transcribe first)", nil)
}

func (d Deps) subtitlesBody(project *storage.Project, assetID string) job.Runner {
	return func(jctx context.Context, progress func(float64)) error {
		bin := worker.ResolveAIBin(d.Cfg.Workers.AIBin)
		if bin == "" {
			return xcerr.E(xcerr.CodeNotFound,
				"no AI sidecar available — subtitles need a transcript sidecar (xcut-ai) or workers.ai_bin", nil)
		}
		caps, err := worker.Capabilities(jctx, bin)
		if err != nil {
			return err
		}
		if m := transcriptCapability(caps); m != nil && !m.Available {
			return xcerr.E(xcerr.CodeNotFound,
				"transcription model is not installed ("+m.Detail+") — install a backend and re-run", nil)
		}
		if transcriptCapability(caps) == nil {
			return xcerr.E(xcerr.CodeNotFound,
				"the AI sidecar does not offer speech transcription — install a Whisper backend it can use", nil)
		}
		progress(0.3)

		asset, err := d.subtitleAsset(project, assetID)
		if err != nil {
			return err
		}
		raw, err := worker.AIAnalyze(jctx, bin, "analyze", asset.Path,
			map[string]any{"analyzer": "transcript"}, worker.DefaultAIAnalyzeTimeout)
		if err != nil {
			return err
		}
		progress(0.8)
		t, err := subs.Parse(raw)
		if err != nil {
			return err
		}

		srtPath, err := d.SubtitlesPath(project.ID, "srt")
		if err != nil {
			return err
		}
		var srt strings.Builder
		if err := subs.WriteSRT(t, &srt); err != nil {
			return err
		}
		if err := WriteAtomic(srtPath, []byte(srt.String())); err != nil {
			return err
		}
		// The transcript is kept beside its two renderings. It is the only copy of
		// the words the sidecar produced, and a reel that later changes shape can be
		// laid out again from it instead of paying for another transcription.
		payload, err := json.Marshal(t)
		if err != nil {
			return err
		}
		tp, terr := d.transcriptPath(project.ID)
		if terr != nil {
			return terr
		}
		if err := WriteAtomic(tp, payload); err != nil {
			return err
		}
		styled, err := d.writeStyledSubtitles(project.ID, t)
		if err != nil {
			return err
		}
		d.Log.Info("subtitles written", "project", project.ID, "segments", len(t.Segments), "language", t.Language, "karaoke", t.HasWordTimings(), "ass", styled)
		progress(1.0)
		return nil
	}
}

// subtitleAsset resolves the media to transcribe: the named asset, or the
// project's first (oldest) one.
func (d Deps) subtitleAsset(project *storage.Project, assetID string) (*storage.Asset, error) {
	if assetID != "" {
		a, err := d.DB.GetAsset(d.Ctx, assetID)
		if err != nil {
			return nil, err
		}
		if a == nil || a.ProjectID != project.ID {
			return nil, xcerr.E(xcerr.CodeNotFound, "unknown asset for this project", nil)
		}
		return a, nil
	}
	assets, err := d.DB.ListAssets(d.Ctx, project.ID)
	if err != nil {
		return nil, err
	}
	if len(assets) == 0 {
		return nil, xcerr.E(xcerr.CodeValidation, "project has no assets (import first)", nil)
	}
	return &assets[0], nil
}

// transcriptCapability finds the sidecar's transcript model entry.
func transcriptCapability(c *worker.AICapabilities) *worker.AIModel {
	for i := range c.Models {
		if strings.HasPrefix(c.Models[i].Name, "transcript") {
			return &c.Models[i]
		}
	}
	return nil
}
