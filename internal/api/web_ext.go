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
// style listing and rendered-video download/playback (range-request capable
// via http.ServeContent).
func (s *Server) RegisterExtensionEndpoints(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/styles", s.handleStyles)
	mux.HandleFunc("GET /api/v1/projects/{id}/render", s.handleRenderDownload)
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
