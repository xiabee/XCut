package api

import (
	"encoding/json"
	"net/http"
	"os"

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
	hasBackup := false
	if bakPath, berr := s.Pipe.TimelineBackupPath(p.ID); berr == nil {
		if _, serr := os.Stat(bakPath); serr == nil {
			hasBackup = true
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"timeline": tl, "has_backup": hasBackup})
}

// handleTimelinePut replaces the project timeline with the posted document.
// The document must validate against the project's real assets before it is
// stored — a bad edit can never reach the renderer. Saves are guarded by the
// document's server-managed Revision: a client must send the revision it
// read; a mismatch (another tab saved, or the timeline was regenerated)
// refuses the save with 409 instead of silently destroying those changes.
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

	// Serialize the read-check-write against other PUTs, backup restores,
	// and regeneration writes (single serve process owns the workspace, so
	// an in-process mutex is the whole story; regeneration takes the same
	// lock through Pipe.TimelineWriteLock).
	s.TimelineMu.Lock()
	defer s.TimelineMu.Unlock()

	path, err := s.Pipe.TimelinePath(p.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	current, err := timeline.LoadFile(path)
	if err != nil && !xcerr.IsCode(err, xcerr.CodeNotFound) {
		writeErr(w, err)
		return
	}
	var storedRev int64
	if current != nil {
		if tl.Revision != current.Revision {
			writeErr(w, xcerr.E(xcerr.CodeConflict,
				"timeline changed since you loaded it (saved revision differs) — GET the current document and reapply your edits", nil))
			return
		}
		storedRev = current.Revision
	}
	tl.Revision = storedRev + 1

	b, err := json.MarshalIndent(tl, "", "  ")
	if err != nil {
		writeErr(w, xcerr.E(xcerr.CodeInternal, "cannot serialize timeline", err))
		return
	}
	if err := pipeline.WriteAtomic(path, b); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": true, "clips": countClips(tl), "revision": tl.Revision})
}

func countClips(tl *timeline.Timeline) int {
	n := 0
	for _, tr := range tl.Tracks {
		n += len(tr.Clips)
	}
	return n
}

// handleTimelineRestore swaps the one-level timeline backup back in as the
// current document (the swap is self-inverting). 404 when no backup exists.
func (s *Server) handleTimelineRestore(w http.ResponseWriter, r *http.Request) {
	p := s.requireProjectRow(w, r)
	if p == nil {
		return
	}
	// The restore's two-file swap races PUTs and regeneration writes on the
	// same documents — hold the timeline write lock across it.
	s.TimelineMu.Lock()
	ok, err := s.Pipe.RestoreTimelineBackup(p)
	s.TimelineMu.Unlock()
	if err != nil {
		writeErr(w, err)
		return
	}
	if !ok {
		writeErr(w, xcerr.E(xcerr.CodeNotFound, "no timeline backup for this project (nothing to restore)", nil))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"restored": true})
}
