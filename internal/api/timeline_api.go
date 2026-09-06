package api

import (
	"encoding/json"
	"net/http"

	"github.com/xiabee/XCut/internal/pipeline"
	"github.com/xiabee/XCut/internal/timeline"
	"github.com/xiabee/XCut/internal/xcerr"
)

// handleTimelineGet returns the project's current timeline document (404
// before the first generation).
func (s *Server) handleTimelineGet(w http.ResponseWriter, r *http.Request) {
	p := s.requireProjectRow(w, r)
	if p == nil {
		return
	}
	path, err := s.Pipe.TimelinePath(p.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	tl, err := timeline.LoadFile(path)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"timeline": tl})
}

// handleTimelinePut replaces the project timeline with the posted document.
// The document must validate against the project's real assets before it is
// stored — a bad edit can never reach the renderer.
func (s *Server) handleTimelinePut(w http.ResponseWriter, r *http.Request) {
	p := s.requireProjectRow(w, r)
	if p == nil {
		return
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20))
	tl := &timeline.Timeline{}
	if err := dec.Decode(tl); err != nil {
		writeErr(w, xcerr.E(xcerr.CodeValidation, "invalid timeline JSON", err))
		return
	}

	assets, err := s.DB.ListAssets(r.Context(), p.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	durations := make(map[string]float64, len(assets))
	for i := range assets {
		durations[assets[i].ID] = assets[i].DurationSec
	}
	if err := tl.Validate(func(id string) (float64, bool) {
		d, ok := durations[id]
		return d, ok
	}); err != nil {
		writeErr(w, err)
		return
	}

	b, err := json.MarshalIndent(tl, "", "  ")
	if err != nil {
		writeErr(w, xcerr.E(xcerr.CodeInternal, "cannot serialize timeline", err))
		return
	}
	path, err := s.Pipe.TimelinePath(p.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := pipeline.WriteAtomic(path, b); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": true, "clips": countClips(tl)})
}

func countClips(tl *timeline.Timeline) int {
	n := 0
	for _, tr := range tl.Tracks {
		n += len(tr.Clips)
	}
	return n
}
