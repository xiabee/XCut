package api

import (
	"encoding/json"
	"math"
	"net/http"
	"os"
	"path/filepath"

	"github.com/xiabee/XCut/internal/pipeline"
	"github.com/xiabee/XCut/internal/style"
	"github.com/xiabee/XCut/internal/xcerr"
)

// Court ROI: a per-preset normalized region of interest (0..1) the style
// engine feeds to the frame_diff_roi analyzer. Persisted as a workspace
// preset override (<workspace>/styles/<name>.json) — the mechanism presets
// already use; the UI draws the rect on a reference frame.

func (s *Server) handleStyleROIGet(w http.ResponseWriter, r *http.Request) {
	preset, err := style.Load(r.PathValue("name"), s.stylesDir()) // gates name validity + existence
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"roi": preset.MotionROI})
}

func (s *Server) handleStyleROIPut(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	preset, err := style.Load(name, s.stylesDir()) // override file if present, else embedded
	if err != nil {
		writeErr(w, err)
		return
	}
	var body struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
		W float64 `json:"w"`
		H float64 `json:"h"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(&body); err != nil {
		writeErr(w, xcerr.E(xcerr.CodeValidation, "invalid JSON body", err))
		return
	}
	if !finiteRect(body.X, body.Y, body.W, body.H) {
		writeErr(w, xcerr.E(xcerr.CodeValidation,
			"roi must satisfy 0<=x,y and 0<w,h and x+w,y+h<=1 (normalized to the frame)", nil))
		return
	}
	preset.MotionROI = &style.MotionROI{X: body.X, Y: body.Y, W: body.W, H: body.H}
	if err := preset.Validate(); err != nil {
		writeErr(w, xcerr.E(xcerr.CodeValidation, "preset with roi invalid: "+xcerr.UserMessage(err), nil))
		return
	}
	b, err := json.MarshalIndent(preset, "", "  ")
	if err != nil {
		writeErr(w, xcerr.E(xcerr.CodeInternal, "cannot serialize preset", err))
		return
	}
	// name passed style.Load's validName gate ([a-z0-9_]), so the join below
	// cannot traverse; stylesDir is the operator's own workspace.
	path := filepath.Join(s.stylesDir(), name+".json")
	if err := pipeline.WriteAtomic(path, b); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"roi": preset.MotionROI})
}

func (s *Server) handleStyleROIDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	path := filepath.Join(s.stylesDir(), name+".json")
	if _, err := os.Stat(path); err != nil { // #nosec G703 -- path is <workspace>/styles/<name>.json with name gated by style.Load below
		if os.IsNotExist(err) {
			writeErr(w, xcerr.E(xcerr.CodeNotFound,
				"no workspace override for this style (the embedded preset has no roi to clear)", nil))
			return
		}
		writeErr(w, xcerr.E(xcerr.CodeInternal, "cannot inspect style override", err))
		return
	}
	preset, err := style.Load(name, s.stylesDir()) // validName gate for the join above
	if err != nil {
		writeErr(w, err)
		return
	}
	preset.MotionROI = nil
	b, err := json.MarshalIndent(preset, "", "  ")
	if err != nil {
		writeErr(w, xcerr.E(xcerr.CodeInternal, "cannot serialize preset", err))
		return
	}
	if err := pipeline.WriteAtomic(path, b); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"roi": nil})
}

// finiteRect mirrors style.Validate's motion_roi rule.
func finiteRect(x, y, w, h float64) bool {
	for _, v := range []float64{x, y, w, h} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return x >= 0 && y >= 0 && w > 0 && h > 0 && x+w <= 1 && y+h <= 1
}
