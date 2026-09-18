// Package api serves XCut's local HTTP API (versioned under /api/v1).
//
// Security posture (SECURITY.md): the API is loopback-only by construction —
// remote listening is rejected until authentication exists. Bodies are size-
// capped, responses are JSON, and xcerr codes map to stable HTTP statuses.
package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os/exec"
	"strings"
	"sync"

	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/pipeline"
	"github.com/xiabee/XCut/internal/setup"
	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/version"
	"github.com/xiabee/XCut/internal/xcerr"
)

// Server carries the API dependencies.
type Server struct {
	DB   *storage.DB
	Pipe pipeline.Deps

	// Log receives server-side request failure traces. The HTTP responses
	// carry only user-safe messages by design, so without this the cause of
	// a failed upload/trigger is invisible in serve.log. nil (tests) keeps
	// the old silent behavior.
	Log *slog.Logger

	// Setup drives the pinned-source component installer (FFmpeg). nil
	// (tests, or a serve built without one) makes the setup endpoints
	// answer honestly that the installer is unavailable.
	Setup *setup.Installer

	// TimelineMu serializes timeline document writes (revision-guarded
	// PUTs, backup restores, and regeneration via Pipe.TimelineWriteLock)
	// across concurrent request and job goroutines in this process; the
	// process itself owns the workspace writer lock.
	TimelineMu sync.Mutex

	// uploadLandMu serializes the unique-name-pick + rename critical
	// section of upload landing. Without it, two concurrent uploads with
	// the same filename can both Stat the same free slot and both Rename
	// onto it — one silently overwriting the other, breaking the
	// never-overwrite contract. The serve process is the only writer of
	// imports/, so an in-process mutex is the whole story.
	uploadLandMu sync.Mutex
}

// Shutdown waits for in-flight async jobs, bounded by ctx: the serve caller
// derives it from a generous timeout so a wedged job (one that ignored its
// own cancellation) cannot own the shutdown path. Whatever a timed-out drain
// leaves behind is reconciled by the next startup's orphan sweep.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.Pipe.Queue.WaitContext(ctx)
}

// statusFor maps xcerr codes to HTTP statuses.
func statusFor(code xcerr.Code) int {
	switch code {
	case xcerr.CodeValidation:
		return http.StatusBadRequest
	case xcerr.CodeNotFound:
		return http.StatusNotFound
	case xcerr.CodeConflict:
		return http.StatusConflict
	case xcerr.CodeUnsupportedMedia:
		return http.StatusUnsupportedMediaType
	case xcerr.CodeResourceLimit:
		return http.StatusTooManyRequests
	case xcerr.CodeCancelled:
		return http.StatusRequestTimeout
	default:
		return http.StatusInternalServerError
	}
}

// writeErr renders an error safely (no internal causes leak to the client)
// and traces the failure server-side: the technical cause only exists in
// the error chain, so without this log line a failed request is
// undiagnosable from serve.log.
func (s *Server) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	code := xcerr.CodeOf(err)
	if s.Log != nil {
		s.Log.Warn("request failed",
			"method", r.Method, "path", r.URL.Path,
			"code", string(code), "err", err)
	}
	writeJSON(w, statusFor(code), map[string]any{
		"error":   string(code),
		"message": xcerr.UserMessage(err),
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// Handler builds the full API route tree.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/projects", s.handleProjectsList)
	mux.HandleFunc("POST /api/v1/projects", s.handleProjectsCreate)
	mux.HandleFunc("GET /api/v1/projects/{id}", s.handleProjectGet)
	mux.HandleFunc("DELETE /api/v1/projects/{id}", s.handleProjectDelete)
	mux.HandleFunc("GET /api/v1/projects/{id}/jobs", s.handleProjectJobs)
	mux.HandleFunc("GET /api/v1/jobs", s.handleJobsList)
	mux.HandleFunc("GET /api/v1/jobs/{id}", s.handleJobGet)
	mux.HandleFunc("POST /api/v1/jobs/{id}/cancel", s.handleJobCancel)

	// Async job triggers (202 Accepted; poll /api/v1/jobs/{id}).
	mux.HandleFunc("POST /api/v1/projects/{id}/assets", s.handleAssetImport)
	mux.HandleFunc("POST /api/v1/projects/{id}/assets/upload", s.handleAssetUpload)
	mux.HandleFunc("POST /api/v1/projects/{id}/analyze", s.handleAnalyze)
	mux.HandleFunc("POST /api/v1/projects/{id}/timeline", s.handleTimeline)
	mux.HandleFunc("POST /api/v1/projects/{id}/timeline/restore-backup", s.handleTimelineRestore)
	mux.HandleFunc("POST /api/v1/projects/{id}/render", s.handleRender)
	mux.HandleFunc("POST /api/v1/projects/{id}/subtitles", s.handleSubtitlesTranscribe)

	// Timeline inspection and manual editing.
	mux.HandleFunc("GET /api/v1/projects/{id}/timeline", s.handleTimelineGet)
	mux.HandleFunc("PUT /api/v1/projects/{id}/timeline", s.handleTimelinePut)

	// Component setup (pinned-source FFmpeg auto-install).
	mux.HandleFunc("GET /api/v1/setup/ffmpeg", s.handleSetupFFmpegStatus)
	mux.HandleFunc("POST /api/v1/setup/ffmpeg", s.handleSetupFFmpegStart)

	s.RegisterExtensionEndpoints(mux)
	registerStatic(mux)

	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	// Cheap presence check (no process spawn): the double-clicked-exe
	// audience has no PATH set up, and the UI must be able to tell them
	// WHY analyze/render would fail.
	t := media.ResolveTools(s.Pipe.Cfg)
	_, ffmpegErr := exec.LookPath(t.FFmpeg)
	resp := map[string]any{
		"ok":      true,
		"version": version.Version,
		"commit":  version.Commit,
		"ffmpeg":  "ok",
	}
	if ffmpegErr != nil {
		resp["ffmpeg"] = "missing"
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleProjectsList(w http.ResponseWriter, r *http.Request) {
	ps, err := s.DB.ListProjects(r.Context())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if ps == nil {
		ps = []storage.Project{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": ps})
}

func (s *Server) handleProjectsCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(&body); err != nil {
		s.writeErr(w, r, xcerr.E(xcerr.CodeValidation, "invalid JSON body", err))
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" || len(body.Name) > 128 {
		s.writeErr(w, r, xcerr.E(xcerr.CodeValidation, "name must be 1..128 characters", nil))
		return
	}
	p, err := s.DB.CreateProject(r.Context(), body.Name)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"project": p})
}

func (s *Server) handleProjectGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, err := s.DB.GetProject(r.Context(), id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if p == nil {
		s.writeErr(w, r, xcerr.E(xcerr.CodeNotFound, "project not found", nil))
		return
	}
	assets, err := s.DB.ListAssets(r.Context(), p.ID)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if assets == nil {
		assets = []storage.Asset{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"project": p, "assets": assets})
}

func (s *Server) handleProjectDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, err := s.DB.GetProject(r.Context(), id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if p == nil {
		s.writeErr(w, r, xcerr.E(xcerr.CodeNotFound, "project not found", nil))
		return
	}
	// The active-jobs gate is enforced ATOMICALLY inside DeleteProject
	// (the gate and the delete are one statement): a trigger enqueueing a
	// job while the old check-then-act handler was deleting would have its
	// row cascade-deleted under a live runner. 0 rows here therefore means
	// "busy or gone" — the project row was just read, so it is busy.
	n, err := s.DB.DeleteProject(r.Context(), p.ID)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if n == 0 {
		s.writeErr(w, r, xcerr.E(xcerr.CodeConflict,
			"project has queued or running jobs — wait for them to finish before deleting", nil))
		return
	}
	// Disk artifacts (renders, uploaded copies) die with the project. A
	// cleanup failure must not resurrect the deletion in the response —
	// the deletion is reported with a warning field instead.
	resp := map[string]any{"deleted": p.ID}
	if err := s.Pipe.WS.RemoveProjectDirs(p.ID); err != nil {
		resp["cleanup_warning"] = xcerr.UserMessage(err)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleProjectJobs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, err := s.DB.GetProject(r.Context(), id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if p == nil {
		s.writeErr(w, r, xcerr.E(xcerr.CodeNotFound, "project not found", nil))
		return
	}
	jobs, err := s.DB.ListJobs(r.Context(), p.ID)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if jobs == nil {
		jobs = []storage.Job{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

func (s *Server) handleJobsList(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.DB.ListJobs(r.Context(), "")
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if jobs == nil {
		jobs = []storage.Job{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}
