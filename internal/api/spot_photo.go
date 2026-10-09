package api

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/player"
	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/xcerr"
)

// Photo-reference seeding over the wire (PERSON_FILTER_ROADMAP Phase 3's web
// half, beside the CLI's --photo): the browser sends the photo CONTENT plus
// the rect the user drew ON THE PHOTO, the server measures both signature
// models from that one image, and the spot row lands complete — the analyze
// pass finds Bins and Bands already on it and never re-measures from the
// video (a photo rect describes the photo, not the source). The staged copy
// lives in a request-scoped temp dir and is always removed.

// maxSpotPhotoBytes bounds one reference photo. A photo that cannot fit in
// 64 MiB has no business feeding a color histogram; the cap keeps the body
// read, the staged copy and the temp budget all bounded.
const maxSpotPhotoBytes = 64 << 20

// parseSpotRectQuery parses the "x,y,w,h" query spelling into a validated
// normalized rect. The validity rule is the storage layer's own, so the
// photo endpoint cannot accept a rect the CLI, the picker PUT and the
// analyze pass would all refuse.
func parseSpotRectQuery(v string) ([]float64, bool) {
	parts := strings.Split(v, ",")
	if len(parts) != 4 {
		return nil, false
	}
	rect := make([]float64, 4)
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return nil, false
		}
		rect[i] = f
	}
	if !storage.ValidSpotRect(rect) {
		return nil, false
	}
	return rect, true
}

func (s *Server) handleAssetSpotPhotoPost(w http.ResponseWriter, r *http.Request) {
	// The measurement runs one ffmpeg decode after the whole body copy —
	// past the server's absolute WriteTimeout on a large photo — so the
	// response gets the same write-idle treatment the upload import gets.
	w = &writeIdleWriter{ResponseWriter: w, rc: http.NewResponseController(w), window: streamIdleWindow}

	asset, err := s.assetROIHandlerScope(w, r)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	rect, ok := parseSpotRectQuery(r.URL.Query().Get("rect"))
	if !ok {
		s.writeErr(w, r, xcerr.E(xcerr.CodeValidation,
			"rect query parameter must be x,y,w,h (normalized 0..1 fractions, inside the photo)", nil))
		return
	}
	name, ok := sanitizeImportName(r.URL.Query().Get("filename"))
	if !ok {
		s.writeErr(w, r, xcerr.E(xcerr.CodeValidation,
			"filename query parameter is required (a bare file name)", nil))
		return
	}
	if r.ContentLength > maxSpotPhotoBytes {
		s.writeErr(w, r, xcerr.E(xcerr.CodeResourceLimit,
			"the photo exceeds the 64 MiB reference-photo bound", nil))
		return
	}

	// The photo is a measurement reference, not an import: it stages in a
	// request-scoped temp dir (so the temp budget sees it) and the dir dies
	// with the request whatever the outcome.
	dir, err := s.Pipe.WS.NewTempDir("spot-photo")
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	defer func() { _ = os.RemoveAll(dir) }()
	photoPath := filepath.Join(dir, name)

	tmp, err := os.Create(photoPath) // #nosec G703 -- Join(NewTempDir output, sanitizeImportName(name)); the name carries no separators
	if err != nil {
		s.writeErr(w, r, xcerr.E(xcerr.CodeInternal, "cannot stage the photo", err))
		return
	}
	written, err := copyUploadBody(w, tmp, io.LimitReader(r.Body, maxSpotPhotoBytes+1), maxSpotPhotoBytes)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		s.writeErr(w, r, xcerr.E(xcerr.CodeInternal, "photo transfer failed", err))
		return
	}
	if written > maxSpotPhotoBytes {
		s.writeErr(w, r, xcerr.E(xcerr.CodeResourceLimit,
			"the photo exceeds the 64 MiB reference-photo bound", nil))
		return
	}
	if written == 0 {
		s.writeErr(w, r, xcerr.E(xcerr.CodeValidation, "empty photo upload", nil))
		return
	}

	sig, mr, err := player.MeasureSignatureFromImage(r.Context(), media.ResolveTools(s.Pipe.Cfg), photoPath, rect)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	spot := &storage.PlayerSpot{Rect: rect, At: 0, Bins: sig.Bins}
	if mr != nil {
		spot.Bands = mr.Bands[:]
	} else {
		// Band-starving shape: the same present-but-empty marker the video
		// and CLI paths write, so the analyze pass sees the one-shot attempt
		// happened and never retries it.
		spot.Bands = make([][]float64, player.RegionCount)
	}
	spot.SampledAt = time.Now().Unix()
	if err := s.DB.SetAssetPlayerSpot(r.Context(), asset.ID, spot); err != nil {
		s.writeErr(w, r, err)
		return
	}
	if s.Log != nil {
		s.Log.Info("player spot seeded from photo",
			"asset_id", asset.ID, "bytes", written, "band_model", mr != nil)
	}
	writeJSON(w, http.StatusOK, map[string]any{"spot": spot})
}
