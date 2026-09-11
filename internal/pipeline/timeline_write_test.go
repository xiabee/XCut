package pipeline

import (
	"encoding/json"
	"os"
	"sync"
	"testing"

	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/timeline"
	"github.com/xiabee/XCut/internal/workspace"
)

// countingLocker is a real exclusive lock that also records how many
// goroutines held it at once — the regeneration write section must hold it
// exclusively, never nested.
type countingLocker struct {
	mu      sync.Mutex // the actual lock
	cmu     sync.Mutex // guards the counters below
	held    int
	maxHeld int
}

func (l *countingLocker) Lock() {
	l.mu.Lock()
	l.cmu.Lock()
	defer l.cmu.Unlock()
	l.held++
	if l.held > l.maxHeld {
		l.maxHeld = l.held
	}
}

func (l *countingLocker) Unlock() {
	l.cmu.Lock()
	l.held--
	l.cmu.Unlock()
	l.mu.Unlock()
}

func tlForRegen(clipID string) *timeline.Timeline {
	return &timeline.Timeline{
		Version: timeline.Version,
		Canvas:  timeline.Canvas{Width: 640, Height: 360, FPS: 30},
		Tracks: []timeline.Track{{
			ID:   "v1",
			Kind: "video",
			Clips: []timeline.Clip{{
				ID: clipID, AssetID: "a", SourceStart: 0, SourceEnd: 1,
				TimelineStart: 0, Speed: 1, Volume: 1,
			}},
		}},
	}
}

// TestWriteRegeneratedTimelineSerializes: concurrent regeneration writes
// take the injected lock exclusively and bump the revision from whatever is
// current at write time — no two writes may ever land on the same revision
// (the PUT-vs-regeneration collision that used to break the 409 guard).
func TestWriteRegeneratedTimelineSerializes(t *testing.T) {
	ws := workspace.New(t.TempDir())
	if err := ws.Ensure(); err != nil {
		t.Fatal(err)
	}
	lock := &countingLocker{}
	d := Deps{WS: ws, TimelineWriteLock: lock}
	p := &storage.Project{ID: "p1"}

	const n = 12
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := d.WriteRegeneratedTimeline(p, tlForRegen("gen")); err != nil {
				t.Errorf("regen %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	if lock.maxHeld != 1 {
		t.Fatalf("lock was held by %d goroutines at once, want exclusive", lock.maxHeld)
	}
	path, err := d.TimelinePath(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc timeline.Timeline
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Revision != n {
		t.Fatalf("final revision = %d, want %d (one bump per write, no collisions)", doc.Revision, n)
	}
	// The last write's predecessor is the backup.
	bakPath, err := d.TimelineBackupPath(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	bb, err := os.ReadFile(bakPath)
	if err != nil {
		t.Fatal(err)
	}
	var bak timeline.Timeline
	if err := json.Unmarshal(bb, &bak); err != nil {
		t.Fatal(err)
	}
	if bak.Revision != n-1 {
		t.Fatalf("backup revision = %d, want %d", bak.Revision, n-1)
	}
}

// TestWriteRegeneratedTimelineWithoutLock: the CLI path (no in-process
// writers; the workspace writer lock excludes cross-process ones) still
// writes a valid document with a bumped revision.
func TestWriteRegeneratedTimelineWithoutLock(t *testing.T) {
	ws := workspace.New(t.TempDir())
	if err := ws.Ensure(); err != nil {
		t.Fatal(err)
	}
	d := Deps{WS: ws}
	p := &storage.Project{ID: "p2"}
	if err := d.WriteRegeneratedTimeline(p, tlForRegen("first")); err != nil {
		t.Fatal(err)
	}
	path, err := d.TimelinePath(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc timeline.Timeline
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Revision != 1 {
		t.Fatalf("first regeneration revision = %d, want 1", doc.Revision)
	}
}

// TestWriteRegeneratedTimelineProgressesPastManualPut: a manual PUT that
// lands while a regeneration is running becomes the backup at write time —
// the regenerated document must bump past it instead of colliding with it.
func TestWriteRegeneratedTimelineProgressesPastManualPut(t *testing.T) {
	ws := workspace.New(t.TempDir())
	if err := ws.Ensure(); err != nil {
		t.Fatal(err)
	}
	d := Deps{WS: ws}
	p := &storage.Project{ID: "p3"}

	// Manual PUT equivalent: doc at revision 5 on disk.
	manual := tlForRegen("manual")
	manual.Revision = 5
	path, err := d.TimelinePath(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	mb, err := json.MarshalIndent(manual, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(path, mb); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := d.WriteRegeneratedTimeline(p, tlForRegen("gen")); err != nil {
			t.Errorf("regen: %v", err)
		}
	}()
	<-done

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc timeline.Timeline
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Revision != 6 {
		t.Fatalf("regen revision = %d, want 6 (bumped from the manual save, no collision)", doc.Revision)
	}
	bakPath, err := d.TimelineBackupPath(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	bb, err := os.ReadFile(bakPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(bb) != string(mb) {
		t.Fatal("backup must hold the manual document the regeneration replaced")
	}
}
