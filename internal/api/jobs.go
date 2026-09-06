package api

import (
	"encoding/json"
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

// POST /api/v1/projects/{id}/render {"out": "D:/videos/out.mp4"} (out optional)
func (s *Server) handleRender(w http.ResponseWriter, r *http.Request) {
	p := s.requireProjectRow(w, r)
	if p == nil {
		return
	}
	var body struct {
		Out string `json:"out"`
	}
	_ = decodeBody(w, r, &body) // body optional
	out := body.Out
	if out == "" {
		defaultOut, err := s.Pipe.DefaultRenderPath(p.ID)
		if err != nil {
			writeErr(w, err)
			return
		}
		out = defaultOut
	}
	id, err := s.Pipe.RenderProjectAsync(p, out, nil)
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
