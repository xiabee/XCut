package api

import (
	"net/http"

	"github.com/xiabee/XCut/internal/pipeline"
	"github.com/xiabee/XCut/internal/style"
	"github.com/xiabee/XCut/internal/xcerr"
)

// POST /api/v1/projects/{id}/motion/plan {"mode":"roi","asset":"<id>","ordinal":2}
// (zoom optional — absent means the picker's default window)
//
// The arithmetic behind the per-clip motion picker lives here rather than in the
// page, because the reel builder and the picker must not hold two versions of what
// "drift" means (docs/ROADMAP.md, B6c). The asset's region is read from its row and
// not handed up by the client, for the same reason: it is the region the builder
// would have aimed at, so a hand pick and a generated reel agree by construction.
//
// Nothing is stored. The client puts what comes back onto the clip and saves it
// through the ordinary timeline write, so the revision check and the
// pre-regeneration backup stay exactly where they are.
func (s *Server) handleMotionPlan(w http.ResponseWriter, r *http.Request) {
	p := s.requireProjectRow(w, r)
	if p == nil {
		return
	}
	var body struct {
		Mode    string  `json:"mode"`
		Zoom    float64 `json:"zoom"`
		Ordinal int     `json:"ordinal"`
		Asset   string  `json:"asset"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	asset, err := s.DB.GetAsset(r.Context(), body.Asset)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if asset == nil || asset.ProjectID != p.ID {
		s.writeErr(w, r, xcerr.E(xcerr.CodeNotFound, "asset not found in this project", nil))
		return
	}
	zoom := body.Zoom
	if zoom == 0 {
		zoom = style.DefaultMotionZoom // absent means the picker's default window
	}
	motion, err := style.MotionFor(body.Mode, zoom, pipeline.AssetMotionROI(asset), body.Ordinal)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	framing := body.Mode
	if motion == nil {
		// "none" claims no framing, because it has none — a clip that carries the
		// word while carrying no window is the lie the style tests already guard.
		framing = ""
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"mode": body.Mode, "framing": framing, "motion": motion,
	})
}
