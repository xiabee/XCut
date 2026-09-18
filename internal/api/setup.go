package api

import (
	"errors"
	"net/http"

	"github.com/xiabee/XCut/internal/setup"
	"github.com/xiabee/XCut/internal/xcerr"
)

// handleSetupFFmpegStatus reports the installer state (idle/downloading/
// extracting/verifying/done/error + progress). The UI polls this while an
// install runs and hides the offer once health reports FFmpeg present.
func (s *Server) handleSetupFFmpegStatus(w http.ResponseWriter, _ *http.Request) {
	if s.Setup == nil {
		writeJSON(w, http.StatusOK, map[string]any{"phase": "unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, s.Setup.Status())
}

// handleSetupFFmpegStart starts the pinned-source install. Single-flight
// (409 while one runs); the request returns immediately — progress comes
// from the status endpoint, and the install outlives this request.
func (s *Server) handleSetupFFmpegStart(w http.ResponseWriter, r *http.Request) {
	if s.Setup == nil {
		s.writeErr(w, r, xcerr.E(xcerr.CodeUnsupportedMedia,
			"the component installer is not available in this build", nil))
		return
	}
	if err := s.Setup.Start(r.Context()); err != nil {
		if errors.Is(err, setup.ErrBusy) {
			s.writeErr(w, r, err) // CodeConflict → 409
			return
		}
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, s.Setup.Status())
}
