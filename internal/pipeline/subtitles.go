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

// transcriptRecord is what transcript.json holds. The payload alone cannot answer the
// question that decides whether re-laying it out is a repair or a caption over the wrong
// speech — whether the project's media is still the media that was heard — so the asset it
// came from is stored beside it. A record with no binding claims nothing, and nothing is
// answered with.
type transcriptRecord struct {
	AssetID    string           `json:"asset_id,omitempty"`
	Transcript *subs.Transcript `json:"transcript"`
}

// storedTranscript is the transcript an earlier transcription left behind, read back
// through the same validation the sidecar's answer went through. A file that cannot be
// laid out again reads as "there is none" to the export — which has a plain .srt to fall
// back to and should use it — but the error is returned too, because "no transcript" and
// "a transcript that fails validation" are different answers to give whoever asks
// directly, and only one of them can be fixed by transcribing again.
func (d Deps) storedTranscript(projectID string) (*subs.Transcript, error) {
	t, _, err := d.storedTranscriptRecord(projectID)
	return t, err
}

// storedTranscriptRecord returns the payload and the asset it was heard from.
func (d Deps) storedTranscriptRecord(projectID string) (*subs.Transcript, string, error) {
	p, err := d.transcriptPath(projectID)
	if err != nil {
		return nil, "", err
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, "", xcerr.E(xcerr.CodeNotFound,
			"no stored transcript for this project (transcribe first)", err)
	}
	var rec transcriptRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, "", xcerr.E(xcerr.CodeValidation,
			"the stored transcript cannot be read (re-transcribe)", err)
	}
	if rec.Transcript == nil {
		return nil, "", xcerr.E(xcerr.CodeValidation,
			"the stored transcript holds no payload (re-transcribe)", nil)
	}
	// The validator speaks bytes (it is the sidecar's wire contract), and the envelope
	// decoder has only just turned those bytes into this struct. Re-encoding is one
	// round trip over a caption file; the alternative is a second validation path for
	// stored payloads, which is how a stored transcript and a sidecar answer start
	// disagreeing about what is acceptable.
	payload, err := json.Marshal(rec.Transcript)
	if err != nil {
		return nil, "", xcerr.E(xcerr.CodeInternal, "cannot read the stored transcript", err)
	}
	t, err := subs.Parse(payload)
	if err != nil {
		return nil, "", err
	}
	return t, rec.AssetID, nil
}

// HasStoredTranscript is the question the plan, the panel and the endpoint ask: can this
// project's captions be laid out again *now*. It is not "does a file exist", and not only
// "does it parse" — a payload heard from a different asset is somebody else's speech, so
// the reel cannot re-lay it, and the callers then reach for a sidecar, which is the one
// component able to answer for audio that is actually here.
func (d Deps) HasStoredTranscript(projectID string) bool {
	t, boundID, err := d.storedTranscriptRecord(projectID)
	if err != nil || t == nil || boundID == "" {
		return false
	}
	p, err := d.DB.GetProject(d.Ctx, projectID)
	if err != nil || p == nil {
		return false
	}
	// The same resolution the transcription itself uses, so "the asset this reel would
	// speak" and "the asset the words came from" are compared by one rule, not two.
	asset, err := d.subtitleAsset(p, "")
	if err != nil {
		return false
	}
	return asset.ID == boundID
}

// CaptionsPredateCurrentMedia reports that the stored transcript names an asset the project
// no longer resolves to: the captions on disk were heard from other media.
//
// It answers only when the binding is known. A project with no transcript file, or one
// written before bindings existed, says nothing here — absence of a record is not evidence
// of a change, and treating it as one would have the tap re-transcribe work it has no
// reason to doubt.
func (d Deps) CaptionsPredateCurrentMedia(projectID string) bool {
	_, boundID, err := d.storedTranscriptRecord(projectID)
	if err != nil || boundID == "" {
		return false
	}
	p, err := d.DB.GetProject(d.Ctx, projectID)
	if err != nil || p == nil {
		return false
	}
	asset, err := d.subtitleAsset(p, "")
	if err != nil {
		return false
	}
	return asset.ID != boundID
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

// RestyleSubtitles lays the project's stored transcript out again for the canvas its
// timeline now declares — the caption fix a sidecar would have made, without the
// sidecar or the minutes of transcription it costs.
func (d Deps) RestyleSubtitles(projectID string) error {
	t, err := d.storedTranscript(projectID)
	if err != nil {
		return err
	}
	_, err = d.writeStyledSubtitles(projectID, t)
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

// RestyleProjectAsync queues a caption re-lay as a **subtitles** job rather than its own
// type. That is the point: `subtitles` is exclusive per project so no second transcription
// can start beside a running one, and a re-lay writes the same `.ass` a transcription
// writes — as its own type it would sit outside that lock, and the two writers would take
// turns on one file (the transcription's stale-cleanup can even remove the frame the re-lay
// just wrote). The payload says which of the two the row was.
func (d Deps) RestyleProjectAsync(project *storage.Project) (string, error) {
	return d.Queue.RunAsync(d.Ctx, job.TypeSubtitles, project.ID, job.ClassCPULight,
		map[string]any{"restyle": true}, d.restyleBody(project))
}

func (d Deps) restyleBody(project *storage.Project) job.Runner {
	return func(jctx context.Context, progress func(float64)) error {
		if err := d.RestyleSubtitles(project.ID); err != nil {
			return err
		}
		progress(1.0)
		return nil
	}
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
		payload, err := json.Marshal(transcriptRecord{AssetID: asset.ID, Transcript: t})
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
