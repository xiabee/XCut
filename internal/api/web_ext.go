package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/xiabee/XCut/internal/style"
	"github.com/xiabee/XCut/internal/workspace"
	"github.com/xiabee/XCut/internal/xcerr"
)

// RegisterExtensionEndpoints adds read-only endpoints used by the web UI:
// style listing, rendered-video download/playback, per-clip source
// preview (range-request capable via http.ServeContent) and subtitle
// status/download.
func (s *Server) RegisterExtensionEndpoints(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/styles", s.handleStyles)
	mux.HandleFunc("GET /api/v1/styles/{name}/roi", s.handleStyleROIGet)
	mux.HandleFunc("PUT /api/v1/styles/{name}/roi", s.handleStyleROIPut)
	mux.HandleFunc("DELETE /api/v1/styles/{name}/roi", s.handleStyleROIDelete)
	mux.HandleFunc("GET /api/v1/projects/{id}/render", s.handleRenderDownload)
	mux.HandleFunc("GET /api/v1/projects/{id}/assets/{assetID}/file", s.handleAssetFile)
	mux.HandleFunc("GET /api/v1/projects/{id}/assets/{assetID}/roi", s.handleAssetROIGet)
	mux.HandleFunc("PUT /api/v1/projects/{id}/assets/{assetID}/roi", s.handleAssetROIPut)
	mux.HandleFunc("DELETE /api/v1/projects/{id}/assets/{assetID}/roi", s.handleAssetROIDelete)
	mux.HandleFunc("GET /api/v1/projects/{id}/subtitles", s.handleSubtitlesStatus)
	mux.HandleFunc("GET /api/v1/projects/{id}/subtitles/file", s.handleSubtitlesFile)
}

// handleStyles lists style presets visible to the server (embedded +
// workspace overrides).
func (s *Server) handleStyles(w http.ResponseWriter, _ *http.Request) {
	names := style.Names(s.stylesDir())
	if names == nil {
		names = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"styles": names})
}

func (s *Server) stylesDir() string {
	return filepath.Join(s.Pipe.WS.Root, "styles")
}

// handleRenderDownload streams a project's rendered MP4 for playback or
// download. 404 until a render exists.
func (s *Server) handleRenderDownload(w http.ResponseWriter, r *http.Request) {
	p := s.requireProjectRow(w, r)
	if p == nil {
		return
	}
	out, err := s.Pipe.DefaultRenderPath(p.ID)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	fi, err := os.Stat(out)
	if err != nil {
		s.writeErr(w, r, xcerr.E(xcerr.CodeNotFound, "no render output for project (render first)", nil))
		return
	}
	f, err := workspace.OpenReadable(out) // share-all: a re-render may replace this file mid-playback
	if err != nil {
		s.writeErr(w, r, xcerr.E(xcerr.CodeInternal, "cannot open render output", err))
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Content-Disposition",
		"inline; filename=\""+sanitizeHeaderFilename(p.Name)+".mp4\"")
	http.ServeContent(w, r, "render.mp4", fi.ModTime(), f)
}

func sanitizeHeaderFilename(name string) string {
	keep := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, name)
	if keep == "" {
		return "render"
	}
	return keep
}

// handleAssetFile streams one project asset's source media for clip preview
// playback. The path comes exclusively from the DB asset row (never from the
// client), ownership is enforced (asset must belong to the project), and the
// response is range-capable so the browser can seek to a clip's source
// offset.
func (s *Server) handleAssetFile(w http.ResponseWriter, r *http.Request) {
	p := s.requireProjectRow(w, r)
	if p == nil {
		return
	}
	assetID := r.PathValue("assetID")
	a, err := s.DB.GetAsset(r.Context(), assetID)
	if err != nil {
		// A storage failure is not "unknown asset": the client must be able
		// to tell a retryable backend problem from a genuinely missing row.
		s.writeErr(w, r, err)
		return
	}
	if a == nil || a.ProjectID != p.ID {
		s.writeErr(w, r, xcerr.E(xcerr.CodeNotFound, "unknown asset for this project", nil))
		return
	}
	fi, err := os.Stat(a.Path) // #nosec G703 -- a.Path is the DB asset row written only by the import API (loopback-only server by design); serving the operator's own imported media is the feature
	if err != nil {
		s.writeErr(w, r, xcerr.E(xcerr.CodeNotFound, "asset media file is missing on disk", err))
		return
	}
	f, err := os.Open(a.Path) // #nosec G703 -- same DB-asset-row path as above; local-first product model (no auth needed for what the operator imported)
	if err != nil {
		s.writeErr(w, r, xcerr.E(xcerr.CodeInternal, "cannot open asset media file", err))
		return
	}
	defer f.Close()

	// Content-Type comes from the asset's real extension via ServeContent
	// (imports may be .mov/.webm/... — hardcoding video/mp4 mislabels them).
	w.Header().Set("Content-Disposition",
		"inline; filename=\""+sanitizeHeaderFilename(a.Filename)+"\"")
	http.ServeContent(w, r, a.Filename, fi.ModTime(), f)
}

// handleSubtitlesStatus reports which subtitle artifacts exist for the
// project (404-flavored empty state, not an error: no transcription yet is
// the normal before-first-run state).
func (s *Server) handleSubtitlesStatus(w http.ResponseWriter, r *http.Request) {
	p := s.requireProjectRow(w, r)
	if p == nil {
		return
	}
	status := map[string]any{"srt": false, "ass": false}
	for _, ext := range []string{"srt", "ass"} {
		path, err := s.Pipe.SubtitlesPath(p.ID, ext)
		if err != nil {
			s.writeErr(w, r, err)
			return
		}
		if _, err := os.Stat(path); err == nil {
			status[ext] = true
		}
	}
	writeJSON(w, http.StatusOK, status)
}

// handleSubtitlesFile downloads one subtitle artifact (?format=ass|srt,
// ass preferred when unspecified but only when it exists).
func (s *Server) handleSubtitlesFile(w http.ResponseWriter, r *http.Request) {
	p := s.requireProjectRow(w, r)
	if p == nil {
		return
	}
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "ass"
	}
	if format != "ass" && format != "srt" {
		s.writeErr(w, r, xcerr.E(xcerr.CodeValidation, "format must be ass or srt", nil))
		return
	}
	path, err := s.Pipe.SubtitlesPath(p.ID, format)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	fi, err := os.Stat(path) // #nosec G703 -- format is whitelist-validated to "ass"|"srt" above and path is built through WS.SafeJoin, so no client-controlled traversal is possible
	if err != nil {
		s.writeErr(w, r, xcerr.E(xcerr.CodeNotFound, "no "+format+" subtitles for this project (transcribe first)", nil))
		return
	}
	f, err := workspace.OpenReadable(path) // share-all: a re-transcribe may replace this file mid-download // #nosec G703 -- same whitelist + SafeJoin-constructed path as the Stat above
	if err != nil {
		s.writeErr(w, r, xcerr.E(xcerr.CodeInternal, "cannot open subtitle file", err))
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition",
		"attachment; filename=\""+sanitizeHeaderFilename(p.Name)+"."+format+"\"")
	http.ServeContent(w, r, "subtitles."+format, fi.ModTime(), f)
}
