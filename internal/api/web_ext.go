package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/xiabee/XCut/internal/style"
	"github.com/xiabee/XCut/internal/xcerr"
)

// RegisterExtensionEndpoints adds read-only endpoints used by the web UI:
// style listing, rendered-video download/playback and per-clip source
// preview (range-request capable via http.ServeContent).
func (s *Server) RegisterExtensionEndpoints(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/styles", s.handleStyles)
	mux.HandleFunc("GET /api/v1/projects/{id}/render", s.handleRenderDownload)
	mux.HandleFunc("GET /api/v1/projects/{id}/assets/{assetID}/file", s.handleAssetFile)
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
		writeErr(w, err)
		return
	}
	fi, err := os.Stat(out)
	if err != nil {
		writeErr(w, xcerr.E(xcerr.CodeNotFound, "no render output for project (render first)", nil))
		return
	}
	f, err := os.Open(out)
	if err != nil {
		writeErr(w, xcerr.E(xcerr.CodeInternal, "cannot open render output", err))
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
	if err != nil || a == nil || a.ProjectID != p.ID {
		writeErr(w, xcerr.E(xcerr.CodeNotFound, "unknown asset for this project", nil))
		return
	}
	fi, err := os.Stat(a.Path)
	if err != nil {
		writeErr(w, xcerr.E(xcerr.CodeNotFound, "asset media file is missing on disk", err))
		return
	}
	f, err := os.Open(a.Path)
	if err != nil {
		writeErr(w, xcerr.E(xcerr.CodeInternal, "cannot open asset media file", err))
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Content-Disposition",
		"inline; filename=\""+sanitizeHeaderFilename(a.Filename)+"\"")
	http.ServeContent(w, r, filepath.Base(a.Filename), fi.ModTime(), f)
}
