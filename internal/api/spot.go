package api

import (
	"encoding/json"
	"net/http"

	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/xcerr"
)

// Per-source player spot: where the subject stands in one frame of THIS
// source, stored on the asset row (assets.player_spot). The analyze pass
// measures the color signature from the rect exactly as it would from a
// CLI-seeded spot, and the person filter (min_player_presence) reads the
// resulting track. The scoreboard crop got the same per-asset surface.

func (s *Server) handleAssetSpotGet(w http.ResponseWriter, r *http.Request) {
	asset, err := s.assetROIHandlerScope(w, r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"spot": asset.PlayerSpot})
}

func (s *Server) handleAssetSpotPut(w http.ResponseWriter, r *http.Request) {
	asset, err := s.assetROIHandlerScope(w, r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	var body struct {
		Rect []float64 `json:"rect"`
		At   float64   `json:"at"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(&body); err != nil {
		s.writeErr(w, r, xcerr.E(xcerr.CodeValidation, "invalid JSON body", err))
		return
	}
	spot := &storage.PlayerSpot{Rect: body.Rect, At: body.At}
	// Validity is the storage layer's own rule, so the API cannot drift from
	// what the CLI and the analyze pass accept.
	if err := s.DB.SetAssetPlayerSpot(r.Context(), asset.ID, spot); err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"spot": spot})
}

func (s *Server) handleAssetSpotDelete(w http.ResponseWriter, r *http.Request) {
	asset, err := s.assetROIHandlerScope(w, r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if asset.PlayerSpot == nil {
		s.writeErr(w, r, xcerr.E(xcerr.CodeNotFound, "this asset has no player spot to clear", nil))
		return
	}
	if err := s.DB.SetAssetPlayerSpot(r.Context(), asset.ID, nil); err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"spot": nil})
}
