package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

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
	Action string `json:"action"` // reuse | create | restyle | skip
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
	// ExportRelaidSubtitles is the fix the tap used to be only able to offer the
	// expensive way: the words are already on disk next to the captions, so the
	// layout can be redone for the reel's own frame without a sidecar, and without
	// the minutes of transcription a sidecar would cost.
	ExportRelaidSubtitles = "re-laid out for the reel's own frame from the stored transcript: "
	// ExportFallbackSubtitles is the third answer to a mismatch, and the one that used to
	// be missing: a plain .srt carries no PlayRes pair and no pre-computed wrap, so
	// libass lays it out against the canvas it is burned onto. Captioned plain beats
	// captioned wrong — a box sized for 1280×720 on a 1080×1920 reel is unreadable, and
	// the styled look is not worth that.
	ExportFallbackSubtitles = "burned from the plain .srt beside them, which declares no frame: "
)

// subsState is what the tap knows about the caption file a project already has:
// where it is, which frame it declares, and which frame the reel renders onto.
// One type answers at ask time and in the body, because the two moments can see
// different things — a tap that has to build the reel first only learns the
// canvas after that build — and a rule each of them held separately is a rule
// that can disagree with itself.
type subsState struct {
	path    string
	srtPath string // the canvas-agnostic sibling, when the project has one
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
	// Resolved separately rather than inferred by rewriting the extension, so the
	// path in the log is the one SubtitlesPath would hand the writer too.
	if p, err := d.SubtitlesPath(projectID, "srt"); err == nil {
		if _, serr := os.Stat(p); serr == nil {
			s.srtPath = p
		}
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

// plainFallback returns the file to burn instead of a mismatched .ass when nothing can
// re-lay it out — an empty string means "there is no better option on disk". It is one
// method shared by the plan and the body on purpose: two decisions that each look at the
// same facts and can disagree is the bug this whole type exists to avoid.
func (s subsState) plainFallback() string {
	if s.mismatch() && s.srtPath != "" && s.srtPath != s.path {
		return s.srtPath
	}
	return ""
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
	case st.mismatch() && d.storedTranscript(projectID) != nil:
		// The words are already on disk, so the fix costs a layout pass instead of a
		// transcription: this is the arm that used to be the sidecar's alone.
		return ExportStep{Step: "subtitles", Action: "restyle", Reason: ExportRelaidSubtitles + st.frames()}
	case st.mismatch() && worker.ResolveAIBin(d.Cfg.Workers.AIBin) != "":
		return ExportStep{Step: "subtitles", Action: "create", Reason: ExportRestyleSubtitles + st.frames()}
	case st.mismatch():
		// No sidecar to lay the words out again. A plain .srt of the same transcript
		// carries no frame to be wrong about, so say which of the two will burn rather
		// than calling both of them "as they stand".
		if st.plainFallback() != "" {
			return ExportStep{Step: "subtitles", Action: "reuse", Reason: ExportFallbackSubtitles + st.frames()}
		}
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
			case st.mismatch() && d.storedTranscript(project.ID) != nil:
				// The plan said it could lay the same words out for this frame, and the
				// words are still there: do it here rather than trusting the plan's
				// reading of a moment ago. A restyle that fails fails the tap — the
				// alternative is burning the ill-fitted file the plan just promised to
				// replace.
				if err := d.restyleSubtitles(project.ID); err != nil {
					return err
				}
				subsPath = d.existingSubtitlesPath(project.ID)
				d.Log.Info("captions re-laid out for the reel's canvas from the stored transcript",
					"project", project.ID,
					"styled", fmt.Sprintf("%dx%d", st.styledW, st.styledH),
					"reel", fmt.Sprintf("%dx%d", st.reelW, st.reelH),
					"subs", subsPath)
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
				// No way to restyle. A plain .srt of the same transcript is laid out by
				// libass against the reel it lands on, so when one exists it burns
				// instead of a box sized for another frame; either way the record names
				// both frames, for whoever reads the log after the reel looks wrong.
				if alt := st.plainFallback(); alt != "" {
					subsPath = alt
					d.Log.Warn("burning the plain transcript instead of captions styled for another canvas",
						"project", project.ID,
						"styled", fmt.Sprintf("%dx%d", st.styledW, st.styledH),
						"reel", fmt.Sprintf("%dx%d", st.reelW, st.reelH),
						"using", filepath.Base(alt))
				} else {
					d.Log.Warn("captions are styled for another canvas than the reel",
						"project", project.ID,
						"styled", fmt.Sprintf("%dx%d", st.styledW, st.styledH),
						"reel", fmt.Sprintf("%dx%d", st.reelW, st.reelH))
				}
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
