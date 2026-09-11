package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xiabee/XCut/internal/job"
	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/subs"
	"github.com/xiabee/XCut/internal/worker"
	"github.com/xiabee/XCut/internal/xcerr"
)

// Speech-to-text subtitles are an AI capability (DECISIONS D3): the core
// never ships or downloads models, so transcription runs through the
// configured AI sidecar and lands as project artifacts — subtitles.srt plus
// subtitles.ass when the sidecar provides word timings. Everything here is
// honest about a missing capability: it is a setup gap with a hint, never a
// silent empty result.

// SubtitlesPath is where a project's subtitles live (ext: "srt" or "ass").
func (d Deps) SubtitlesPath(projectID, ext string) (string, error) {
	return d.WS.SafeJoin(filepath.Join("projects", projectID, "subtitles."+ext))
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

// ResolveSubtitlesPath returns the project's subtitle file for burn-in:
// karaoke ASS when present, plain SRT otherwise. Not-found when the project
// has none yet.
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
		assPath, aerr := d.SubtitlesPath(project.ID, "ass")
		if aerr != nil {
			return aerr
		}
		if t.HasWordTimings() {
			var ass strings.Builder
			if err := subs.WriteKaraokeASS(t, subs.KaraokeStyle{}, &ass); err != nil {
				return err
			}
			if err := WriteAtomic(assPath, []byte(ass.String())); err != nil {
				return err
			}
		} else {
			// Stale karaoke file must not outlive its data: resolution
			// prefers .ass, so a scanner-blocked remove gets a short retry.
			for i := 0; i < 4; i++ {
				if os.Remove(assPath) == nil {
					break
				}
				if _, statErr := os.Stat(assPath); statErr != nil {
					break // already gone
				}
				time.Sleep(50 * time.Millisecond)
			}
		}
		d.Log.Info("subtitles written", "project", project.ID, "segments", len(t.Segments), "language", t.Language, "karaoke", t.HasWordTimings())
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
