package pipeline

import (
	"context"
	"fmt"
	"os"

	"github.com/xiabee/XCut/internal/job"
	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/subs"
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
	// ExportRestyleSubtitles and ExportStaleSubtitles are the two halves of the
	// answer a plain "already has subtitles" used to give: the file on disk was
	// laid out for some frame, the reel is some frame, and when those differ the
	// tap either fixes it or says that it cannot.
	ExportRestyleSubtitles = "re-transcribed for the reel's own frame: "
	ExportStaleSubtitles   = "burned as they stand, there is no sidecar to lay them out again: "
)

// subsState is what the tap knows about the caption file a project already has:
// where it is, which frame it declares, and which frame the reel renders onto.
// One type answers at ask time and in the body, because the two moments can see
// different things — a tap that has to build the reel first only learns the
// canvas after that build — and a rule each of them held separately is a rule
// that can disagree with itself.
type subsState struct {
	path    string
	styledW int
	styledH int
	reelW   int
	reelH   int
}

func (d Deps) subsState(projectID string) subsState {
	s := subsState{path: d.existingSubtitlesPath(projectID)}
	if s.path == "" {
		return s
	}
	if f, err := os.Open(s.path); err == nil {
		if w, h, ok := subs.ReadASSFrame(f); ok {
			s.styledW, s.styledH = w, h
		}
		f.Close()
	}
	// The same read the transcript stage does: the canvas is the timeline
	// document's, not the request's, so a hand-edited reel is respected.
	if tp, err := d.TimelinePath(projectID); err == nil {
		if tl, lerr := timeline.LoadFile(tp); lerr == nil {
			s.reelW, s.reelH = tl.Canvas.Width, tl.Canvas.Height
		}
	}
	return s
}

// mismatch is the case the tap used to call "reuse": the captions exist, but for
// a different frame than the reel now has. A file that claims nothing (an SRT, or
// an ASS with no PlayRes pair) and a project with no timeline yet both mean
// "cannot compare" — that is the absence of evidence, not a conflict, and the
// tap says what it knows rather than guessing a mismatch into existence.
func (s subsState) mismatch() bool {
	return s.path != "" && s.styledW > 0 && s.reelW > 0 &&
		(s.styledW != s.reelW || s.styledH != s.reelH)
}

func (s subsState) frames() string {
	return fmt.Sprintf("the captions are styled for %dx%d and this reel is %dx%d",
		s.styledW, s.styledH, s.reelW, s.reelH)
}

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
	case subsPath == "" && worker.ResolveAIBin(d.Cfg.Workers.AIBin) == "":
		steps[1] = ExportStep{Step: "subtitles", Action: "skip", Reason: ExportSkipNoSidecar}
	case subsPath != "":
		steps[1] = d.subtitlesReuseStep(project.ID)
	}
	id, err := d.Queue.RunAsync(d.Ctx, job.TypeExport, project.ID, job.ClassCPUHeavy,
		map[string]any{"style": req.Timeline.Style, "subs": req.Subs, "out": req.Out},
		d.exportBody(project, req))
	if err != nil {
		return "", nil, err
	}
	return id, steps, nil
}

// subtitlesReuseStep says *which* kind of "there is already a file" this is.
// Existence alone used to be the whole answer, which let a project that changed
// shape — horizontal reel to vertical, the default of the tap itself — report its
// old captions as done and burn a caption box sized for a frame nobody is going
// to watch.
func (d Deps) subtitlesReuseStep(projectID string) ExportStep {
	st := d.subsState(projectID)
	switch {
	case st.mismatch() && worker.ResolveAIBin(d.Cfg.Workers.AIBin) != "":
		return ExportStep{Step: "subtitles", Action: "create", Reason: ExportRestyleSubtitles + st.frames()}
	case st.mismatch():
		return ExportStep{Step: "subtitles", Action: "reuse", Reason: ExportStaleSubtitles + st.frames()}
	default:
		return ExportStep{Step: "subtitles", Action: "reuse", Reason: ExportReuseSubtitles}
	}
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
		if req.Subs {
			st := d.subsState(project.ID)
			sidecar := worker.ResolveAIBin(d.Cfg.Workers.AIBin) != ""
			switch {
			case (st.path == "" || st.mismatch()) && sidecar:
				// Nothing yet, or something styled for another frame: with a sidecar
				// the words can be laid out again, and a transcript that fails here
				// fails the tap rather than quietly shipping the ill-fitted file the
				// plan promised to replace.
				if err := d.subtitlesBody(project, "")(jctx, stage(0.5, 0.8)); err != nil {
					return err
				}
				subsPath = d.existingSubtitlesPath(project.ID)
			case st.mismatch():
				// No way to restyle. Burn what is there — an ill-fitted caption beats
				// no caption, and the plan line already said which of the two the user
				// is getting — but name both frames on the record for whoever reads the
				// log after the reel looks wrong.
				d.Log.Warn("captions are styled for another canvas than the reel",
					"project", project.ID,
					"styled", fmt.Sprintf("%dx%d", st.styledW, st.styledH),
					"reel", fmt.Sprintf("%dx%d", st.reelW, st.reelH))
			}
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
