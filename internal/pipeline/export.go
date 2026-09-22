package pipeline

import (
	"context"
	"os"

	"github.com/xiabee/XCut/internal/job"
	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/timeline"
	"github.com/xiabee/XCut/internal/worker"
)

// The export tap is the sequence a user would otherwise click through: a reel,
// its subtitles, the render. It is one job that does the first two inline and
// queues the third, never one job waiting for another — a waiting parent holds
// a slot in a queue two slots deep, and two taps would then deadlock each other.

// ExportRequest is one tap's worth of intent: the reel to build when the project
// has none, whether the output carries subtitles, and where to write it.
type ExportRequest struct {
	Timeline TimelineRequest
	Subs     bool
	Out      string
}

// DefaultExportStyle is what the tap reaches for when the caller names nothing:
// the vertical, hook-first, beat-snapped shape a short-form platform takes. A
// caller that wants the horizontal reel asks for it — the point of the tap is
// that the posting shape is the default, not an extra page of options.
const DefaultExportStyle = "beat_shortform"

// ExportStep is one line of the readout handed back with the job id: what the tap
// decided about a stage and why. It is a plan, computed from the state as the
// request arrived; the body re-reads the state it actually finds. Its reason for
// existing at all is the third kind of answer — a stage that will not happen says
// so, instead of the reel quietly having no captions.
type ExportStep struct {
	Step   string `json:"step"`   // timeline | subtitles | render
	Action string `json:"action"` // reuse | create | skip
	Reason string `json:"reason,omitempty"`
}

// Subtitle skip reasons, named so the api test asserts the wire text rather than
// a substring someone could rewrite.
const (
	ExportSkipNotAsked   = "not asked for"
	ExportSkipNoSidecar  = "no AI sidecar configured — set workers.ai_bin (or install xcut-ai) and transcribe"
	ExportRenderQueued   = "queued as its own job, so it waits for the render slot like any other render"
	ExportReuseTimeline  = "the project already has a timeline"
	ExportReuseSubtitles = "the project already has subtitles"
)

// ExportProjectAsync starts the tap. It returns the job id and the plan the state
// supported at the moment of asking.
func (d Deps) ExportProjectAsync(project *storage.Project, req ExportRequest) (string, []ExportStep, error) {
	if err := req.Timeline.validate(); err != nil {
		return "", nil, err
	}
	subsPath := d.existingSubtitlesPath(project.ID)
	steps := []ExportStep{
		{Step: "timeline", Action: "create", Reason: "the reel the render will cut"},
		{Step: "subtitles", Action: "create", Reason: "transcribed through the AI sidecar"},
		{Step: "render", Action: "create", Reason: ExportRenderQueued},
	}
	if d.hasTimeline(project.ID) {
		steps[0] = ExportStep{Step: "timeline", Action: "reuse", Reason: ExportReuseTimeline}
	}
	switch {
	case !req.Subs:
		steps[1] = ExportStep{Step: "subtitles", Action: "skip", Reason: ExportSkipNotAsked}
	case subsPath != "":
		steps[1] = ExportStep{Step: "subtitles", Action: "reuse", Reason: ExportReuseSubtitles}
	case worker.ResolveAIBin(d.Cfg.Workers.AIBin) == "":
		steps[1] = ExportStep{Step: "subtitles", Action: "skip", Reason: ExportSkipNoSidecar}
	}
	id, err := d.Queue.RunAsync(d.Ctx, job.TypeExport, project.ID, job.ClassCPUHeavy,
		map[string]any{"style": req.Timeline.Style, "subs": req.Subs, "out": req.Out},
		d.exportBody(project, req))
	if err != nil {
		return "", nil, err
	}
	return id, steps, nil
}

func (d Deps) exportBody(project *storage.Project, req ExportRequest) job.Runner {
	return func(jctx context.Context, progress func(float64)) error {
		// stage maps a sub-step's own 0..1 onto the slice of the bar it owns, so
		// the progress the user watches rises instead of jumping.
		stage := func(from, to float64) func(float64) {
			return func(p float64) { progress(from + (to-from)*p) }
		}
		if !d.hasTimeline(project.ID) {
			if err := d.timelineBody(project, req.Timeline, nil, &timeline.Timeline{})(jctx, stage(0, 0.5)); err != nil {
				return err
			}
		} else {
			progress(0.5)
		}

		// Re-read what is on disk rather than trusting the plan: the timeline built
		// a moment ago may have changed which canvas the captions are styled for, and
		// a transcript written since the request is a transcript worth reusing.
		subsPath := d.existingSubtitlesPath(project.ID)
		if subsPath == "" && req.Subs && worker.ResolveAIBin(d.Cfg.Workers.AIBin) != "" {
			if err := d.subtitlesBody(project, "")(jctx, stage(0.5, 0.8)); err != nil {
				return err
			}
			subsPath = d.existingSubtitlesPath(project.ID)
		}
		progress(0.8)

		out := req.Out
		if out == "" {
			p, err := d.DefaultRenderPath(project.ID)
			if err != nil {
				return err
			}
			out = p
		}
		// d.Ctx, not jctx: the parent's context is cancelled the instant this body
		// returns, and a child born from it would die with the tap that queued it.
		if _, err := d.Queue.RunAsync(d.Ctx, job.TypeRender, project.ID, job.ClassCPUHeavy,
			map[string]any{"out": out, "subs": subsPath}, d.renderBody(project, out, subsPath, nil)); err != nil {
			return err
		}
		d.Log.Info("export queued the render", "project", project.ID, "subs", subsPath != "", "out", out)
		progress(1.0)
		return nil
	}
}

func (d Deps) hasTimeline(projectID string) bool {
	p, err := d.TimelinePath(projectID)
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// existingSubtitlesPath is ResolveSubtitlesPath without the error: "nothing yet"
// is a state the export branches on, not a failure to report.
func (d Deps) existingSubtitlesPath(projectID string) string {
	p, err := d.ResolveSubtitlesPath(projectID)
	if err != nil {
		return ""
	}
	return p
}
