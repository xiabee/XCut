package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"

	"github.com/xiabee/XCut/internal/pipeline"
	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/xcerr"
)

// handleJobGet returns one job's status (poll target for async triggers).
func (s *Server) handleJobGet(w http.ResponseWriter, r *http.Request) {
	j, err := s.DB.GetJob(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if j == nil {
		s.writeErr(w, r, xcerr.E(xcerr.CodeNotFound, "job not found", nil))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": j})
}

// handleJobCancel requests cancellation of an active job. The response is
// 202 (request accepted; poll the job row for the terminal state), 404 for
// an unknown id, and 409 for a job that already finished or one whose row is
// active but has no live runner in this process (left by a crashed previous
// instance — those are reconciled at startup).
func (s *Server) handleJobCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	j, err := s.DB.GetJob(r.Context(), id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if j == nil {
		s.writeErr(w, r, xcerr.E(xcerr.CodeNotFound, "job not found", nil))
		return
	}
	if j.Status != storage.StatusQueued && j.Status != storage.StatusRunning {
		s.writeErr(w, r, xcerr.E(xcerr.CodeConflict,
			"job already "+j.Status+" — nothing to cancel", nil))
		return
	}
	if !s.Pipe.Queue.Cancel(id) {
		// A false return also covers the benign race where the job finished
		// between the status check above and the Cancel call. Re-read so the
		// message reports the real state instead of asserting crash debris.
		if fresh, gerr := s.DB.GetJob(r.Context(), id); gerr == nil && fresh != nil &&
			fresh.Status != storage.StatusQueued && fresh.Status != storage.StatusRunning {
			s.writeErr(w, r, xcerr.E(xcerr.CodeConflict,
				"job already "+fresh.Status+" — nothing to cancel", nil))
			return
		}
		s.writeErr(w, r, xcerr.E(xcerr.CodeConflict,
			"job row is active but no live runner holds it in this process (left by a previous crashed instance) — it is reconciled on next startup", nil))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"cancelling": true,
		"job_id":     id,
	})
}

// requireProjectRow resolves the {id} path value to a project (404 when
// unknown). Returns nil after writing the error response.
func (s *Server) requireProjectRow(w http.ResponseWriter, r *http.Request) *storage.Project {
	p, err := s.DB.GetProject(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeErr(w, r, err)
		return nil
	}
	if p == nil {
		s.writeErr(w, r, xcerr.E(xcerr.CodeNotFound, "project not found", nil))
		return nil
	}
	return p
}

// decodeBody parses a small JSON body.
func (s *Server) decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		s.writeErr(w, r, xcerr.E(xcerr.CodeValidation, "invalid JSON body", err))
		return false
	}
	return true
}

// writeJobAccepted confirms the project row survived the enqueue race
// against DELETE /projects/{id}: a delete committing between
// requireProjectRow and CreateJob would leave the job running against a
// ghost project no endpoint can address again. Gone → cancel the job
// (queued jobs cancel before their body runs) and report 404.
func (s *Server) writeJobAccepted(w http.ResponseWriter, r *http.Request, projectID, jobID string, extra ...map[string]any) {
	if p, err := s.DB.GetProject(r.Context(), projectID); err != nil || p == nil {
		s.Pipe.Queue.Cancel(jobID)
		s.writeErr(w, r, xcerr.E(xcerr.CodeNotFound, "project not found", nil))
		return
	}
	fields := map[string]any{"queued": true, "job_id": jobID}
	for _, m := range extra {
		for k, v := range m {
			fields[k] = v
		}
	}
	writeJSON(w, http.StatusAccepted, fields)
}

// decodeOptionalBody parses a small JSON body that may be omitted entirely
// (POST with no body). An empty body leaves v untouched; a non-empty but
// invalid body is a 400 and the caller must stop — continuing after a failed
// decode would queue work the client was told failed.
func (s *Server) decodeOptionalBody(w http.ResponseWriter, r *http.Request, v any) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		s.writeErr(w, r, xcerr.E(xcerr.CodeValidation, "cannot read request body", err))
		return false
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return true
	}
	if err := json.Unmarshal(body, v); err != nil {
		s.writeErr(w, r, xcerr.E(xcerr.CodeValidation, "invalid JSON body", err))
		return false
	}
	return true
}

// POST /api/v1/projects/{id}/assets {"path": "..."}
func (s *Server) handleAssetImport(w http.ResponseWriter, r *http.Request) {
	p := s.requireProjectRow(w, r)
	if p == nil {
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	if body.Path == "" {
		s.writeErr(w, r, xcerr.E(xcerr.CodeValidation, "path is required", nil))
		return
	}
	id, err := s.Pipe.ImportAssetAsync(p, body.Path)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.writeJobAccepted(w, r, p.ID, id)
}

// POST /api/v1/projects/{id}/analyze
func (s *Server) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	p := s.requireProjectRow(w, r)
	if p == nil {
		return
	}
	id, err := s.Pipe.AnalyzeProjectAsync(p, nil)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.writeJobAccepted(w, r, p.ID, id)
}

// POST /api/v1/projects/{id}/timeline {"style": "generic_highlight",
// "duration": 120, "beat_snap": 0.12, "music": "bed.mp3"}
func (s *Server) handleTimeline(w http.ResponseWriter, r *http.Request) {
	p := s.requireProjectRow(w, r)
	if p == nil {
		return
	}
	var body struct {
		Style    string  `json:"style"`
		Duration float64 `json:"duration"`
		// Pointer so "absent" and "0" differ: absent keeps the style's own
		// tolerance, 0 means the same thing through the pipeline, and -1
		// (pipeline.BeatSnapOff) is how a client forces it off.
		BeatSnap *float64 `json:"beat_snap"`
		// Music names a track to lay under the reel and cut to. Empty or absent
		// is no bed; the timeline document then records it, so the render mixes
		// without the client repeating the choice.
		Music string `json:"music"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	if body.Style == "" {
		body.Style = "generic_highlight"
	}
	req := pipeline.TimelineRequest{Style: body.Style, Duration: body.Duration, Music: body.Music}
	if body.BeatSnap != nil {
		req.BeatSnap = *body.BeatSnap
	}
	id, err := s.Pipe.BuildTimelineAsync(p, req)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.writeJobAccepted(w, r, p.ID, id)
}

// POST /api/v1/projects/{id}/subtitles {"asset": "<id>"} (asset optional,
// first asset by default) — transcribes through the AI sidecar and writes
// subtitles.{srt,ass} into the project directory.
func (s *Server) handleSubtitlesTranscribe(w http.ResponseWriter, r *http.Request) {
	p := s.requireProjectRow(w, r)
	if p == nil {
		return
	}
	var body struct {
		Asset string `json:"asset"`
	}
	if !s.decodeOptionalBody(w, r, &body) {
		return
	}
	id, err := s.Pipe.TranscribeProjectAsync(p, body.Asset)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.writeJobAccepted(w, r, p.ID, id)
}

// resolveRenderOut anchors an API-supplied render output. The server's working
// directory is an implementation detail the caller cannot see — a relative out
// used to land wherever serve happened to be started — so a relative path
// belongs to the workspace, where every other artifact this API names lives.
// Absolute paths are the documented shape and pass through untouched; anything
// that would escape the workspace is refused by SafeJoin.
func (s *Server) resolveRenderOut(w http.ResponseWriter, r *http.Request, out string) (string, bool) {
	if out == "" || filepath.IsAbs(out) {
		return out, true
	}
	anchored, err := s.Pipe.WS.SafeJoin(out)
	if err != nil {
		s.writeErr(w, r, err)
		return "", false
	}
	return anchored, true
}

// POST /api/v1/projects/{id}/render {"out": "D:/videos/out.mp4", "subs": true}
// (out optional, absolute or workspace-relative; subs burns the project's
// subtitles into the output. The acceptance echoes the resolved "out" — the
// caller should not have to guess where the workspace root is.)
func (s *Server) handleRender(w http.ResponseWriter, r *http.Request) {
	p := s.requireProjectRow(w, r)
	if p == nil {
		return
	}
	var body struct {
		Out  string `json:"out"`
		Subs bool   `json:"subs"`
	}
	if !s.decodeOptionalBody(w, r, &body) {
		return
	}
	out := body.Out
	if out == "" {
		defaultOut, err := s.Pipe.DefaultRenderPath(p.ID)
		if err != nil {
			s.writeErr(w, r, err)
			return
		}
		out = defaultOut
	} else {
		anchored, ok := s.resolveRenderOut(w, r, out)
		if !ok {
			return
		}
		out = anchored
	}
	subsPath := ""
	if body.Subs {
		var serr error
		subsPath, serr = s.Pipe.ResolveSubtitlesPath(p.ID)
		if serr != nil {
			s.writeErr(w, r, serr)
			return
		}
	}
	id, err := s.Pipe.RenderProjectAsync(p, out, subsPath, nil)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	// The resolved destination rides the acceptance: a caller that asked for
	// "reel.mp4" should not have to guess where the workspace root is.
	s.writeJobAccepted(w, r, p.ID, id, map[string]any{"out": out})
}

// POST /api/v1/projects/{id}/export {"style":"beat_shortform","duration":30,
// "beat_snap":0.12,"music":"bed.mp3","subs":true,"out":"D:/videos/reel.mp4"}
// (every field optional — the tap defaults to the short-form shape)
//
// One tap for a post-ready reel: it builds the timeline if the project has none,
// transcribes if subtitles were asked for and none exist, then queues the
// render. The response carries the plan as "steps" — including the stages that
// will not happen and the reason — because a reel that silently lost its
// captions is the failure this endpoint exists to avoid.
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	p := s.requireProjectRow(w, r)
	if p == nil {
		return
	}
	var body struct {
		Style    string   `json:"style"`
		Duration float64  `json:"duration"`
		BeatSnap *float64 `json:"beat_snap"`
		Music    string   `json:"music"`
		Subs     *bool    `json:"subs"`
		Out      string   `json:"out"`
	}
	if !s.decodeOptionalBody(w, r, &body) {
		return
	}
	if body.Style == "" {
		body.Style = pipeline.DefaultExportStyle
	}
	anchoredOut, ok := s.resolveRenderOut(w, r, body.Out)
	if !ok {
		return
	}
	req := pipeline.ExportRequest{
		Timeline: pipeline.TimelineRequest{Style: body.Style, Duration: body.Duration, Music: body.Music},
		Out:      anchoredOut,
	}
	if body.BeatSnap != nil {
		req.Timeline.BeatSnap = *body.BeatSnap
	}
	// Absent means yes: the tap's whole point is the reel a platform takes, and
	// captions are part of that shape, not an extra the user has to know about.
	req.Subs = body.Subs == nil || *body.Subs
	id, steps, err := s.Pipe.ExportProjectAsync(p, req)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	extra := map[string]any{"steps": steps}
	if anchoredOut != "" {
		// The caller chose a destination; echo where it will land. An omitted
		// out stays omitted — the tap's default is its own decision.
		extra["out"] = anchoredOut
	}
	s.writeJobAccepted(w, r, p.ID, id, extra)
}
