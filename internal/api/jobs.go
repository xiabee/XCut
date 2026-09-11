package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/xcerr"
)

// handleJobGet returns one job's status (poll target for async triggers).
func (s *Server) handleJobGet(w http.ResponseWriter, r *http.Request) {
	j, err := s.DB.GetJob(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if j == nil {
		writeErr(w, xcerr.E(xcerr.CodeNotFound, "job not found", nil))
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
		writeErr(w, err)
		return
	}
	if j == nil {
		writeErr(w, xcerr.E(xcerr.CodeNotFound, "job not found", nil))
		return
	}
	if j.Status != storage.StatusQueued && j.Status != storage.StatusRunning {
		writeErr(w, xcerr.E(xcerr.CodeConflict,
			"job already "+j.Status+" — nothing to cancel", nil))
		return
	}
	if !s.Pipe.Queue.Cancel(id) {
		writeErr(w, xcerr.E(xcerr.CodeConflict,
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
		writeErr(w, err)
		return nil
	}
	if p == nil {
		writeErr(w, xcerr.E(xcerr.CodeNotFound, "project not found", nil))
		return nil
	}
	return p
}

// decodeBody parses a small JSON body.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		writeErr(w, xcerr.E(xcerr.CodeValidation, "invalid JSON body", err))
		return false
	}
	return true
}

// decodeOptionalBody parses a small JSON body that may be omitted entirely
// (POST with no body). An empty body leaves v untouched; a non-empty but
// invalid body is a 400 and the caller must stop — continuing after a failed
// decode would queue work the client was told failed.
func decodeOptionalBody(w http.ResponseWriter, r *http.Request, v any) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeErr(w, xcerr.E(xcerr.CodeValidation, "cannot read request body", err))
		return false
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return true
	}
	if err := json.Unmarshal(body, v); err != nil {
		writeErr(w, xcerr.E(xcerr.CodeValidation, "invalid JSON body", err))
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
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Path == "" {
		writeErr(w, xcerr.E(xcerr.CodeValidation, "path is required", nil))
		return
	}
	id, err := s.Pipe.ImportAssetAsync(p, body.Path)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeAccepted(w, id)
}

// POST /api/v1/projects/{id}/analyze
func (s *Server) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	p := s.requireProjectRow(w, r)
	if p == nil {
		return
	}
	id, err := s.Pipe.AnalyzeProjectAsync(p, nil)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeAccepted(w, id)
}

// POST /api/v1/projects/{id}/timeline {"style": "generic_highlight"}
func (s *Server) handleTimeline(w http.ResponseWriter, r *http.Request) {
	p := s.requireProjectRow(w, r)
	if p == nil {
		return
	}
	var body struct {
		Style string `json:"style"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Style == "" {
		body.Style = "generic_highlight"
	}
	id, err := s.Pipe.BuildTimelineAsync(p, body.Style)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeAccepted(w, id)
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
	if !decodeOptionalBody(w, r, &body) {
		return
	}
	id, err := s.Pipe.TranscribeProjectAsync(p, body.Asset)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeAccepted(w, id)
}

// POST /api/v1/projects/{id}/render {"out": "D:/videos/out.mp4", "subs": true}
// (out optional; subs burns the project's subtitles into the output)
func (s *Server) handleRender(w http.ResponseWriter, r *http.Request) {
	p := s.requireProjectRow(w, r)
	if p == nil {
		return
	}
	var body struct {
		Out  string `json:"out"`
		Subs bool   `json:"subs"`
	}
	if !decodeOptionalBody(w, r, &body) {
		return
	}
	out := body.Out
	if out == "" {
		defaultOut, err := s.Pipe.DefaultRenderPath(p.ID)
		if err != nil {
			writeErr(w, err)
			return
		}
		out = defaultOut
	}
	subsPath := ""
	if body.Subs {
		var serr error
		subsPath, serr = s.Pipe.ResolveSubtitlesPath(p.ID)
		if serr != nil {
			writeErr(w, serr)
			return
		}
	}
	id, err := s.Pipe.RenderProjectAsync(p, out, subsPath, nil)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeAccepted(w, id)
}

func writeAccepted(w http.ResponseWriter, jobID string) {
	writeJSON(w, http.StatusAccepted, map[string]any{
		"queued": true,
		"job_id": jobID,
	})
}
