package api

import (
	"encoding/json"
	"net/http"

	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/xcerr"
)

// Scoreboard region: the rectangle the burned-in score overlay occupies on ONE
// asset. Writing it is the whole of what the UI can do — the measurement itself
// is the analyze stage's job (docs/DECISIONS.md D15), because it runs a sidecar
// and an ffmpeg child and belongs to a job with progress, cancellation and a
// worker budget, not to a request handler.

func (s *Server) handleAssetScoreGet(w http.ResponseWriter, r *http.Request) {
	asset, err := s.assetROIHandlerScope(w, r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, scoreRegionResponse(asset))
}

// handleAssetScorePut stores the region. It answers with the state, not with
// "scanned": the scan happens later, in the analyze job, and a client that was
// told otherwise would poll for something that is not coming.
func (s *Server) handleAssetScorePut(w http.ResponseWriter, r *http.Request) {
	asset, err := s.assetROIHandlerScope(w, r)
	if err != nil {
		s.writeErr(w, r, err)
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
		s.writeErr(w, r, xcerr.E(xcerr.CodeValidation, "invalid JSON body", err))
		return
	}
	crop := []float64{body.X, body.Y, body.W, body.H}
	if err := s.DB.SetAssetScoreCrop(r.Context(), asset.ID, crop); err != nil {
		s.writeErr(w, r, err)
		return
	}
	fresh, err := s.DB.GetAsset(r.Context(), asset.ID)
	if err != nil || fresh == nil {
		s.writeErr(w, r, xcerr.E(xcerr.CodeStorageFailure, "cannot read asset back", err))
		return
	}
	writeJSON(w, http.StatusOK, scoreRegionResponse(fresh))
}

func (s *Server) handleAssetScoreDelete(w http.ResponseWriter, r *http.Request) {
	asset, err := s.assetROIHandlerScope(w, r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if asset.ScoreCrop == nil {
		s.writeErr(w, r, xcerr.E(xcerr.CodeNotFound, "this asset has no scoreboard region to clear", nil))
		return
	}
	if err := s.DB.SetAssetScoreCrop(r.Context(), asset.ID, nil); err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"crop": nil, "marks": 0, "stale": false})
}

// scoreRegionResponse reports the region, how many boundaries were measured
// against it, and whether those boundaries are older than the region — the UI
// needs the last one to say "re-analyze" instead of looking silently satisfied.
func scoreRegionResponse(a *storage.Asset) map[string]any {
	out := map[string]any{"crop": a.ScoreCrop, "marks": 0, "stale": false}
	if m := a.ScoreMarks; m != nil {
		out["marks"] = len(m.Times)
		out["scanned_at"] = m.At
		out["stale"] = !storage.SameCrop(m.Crop, a.ScoreCrop)
	}
	return out
}
