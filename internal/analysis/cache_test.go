package analysis

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(t.TempDir())
}

func writeEntry(t *testing.T, s *Store, key, pad string, mtimeSec int64) {
	t.Helper()
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := s.path(key)
	if err := os.WriteFile(p, []byte(`{"pad":"`+pad+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	then := time.Unix(mtimeSec, 0)
	if err := os.Chtimes(p, then, then); err != nil {
		t.Fatal(err)
	}
}

func TestEvictToRemovesOldestFirst(t *testing.T) {
	s := newTestStore(t)
	writeEntry(t, s, "aaa", "aaaa", 1000) // oldest
	writeEntry(t, s, "bbb", "b", 2000)
	writeEntry(t, s, "ccc", "c", 3000)

	// Total is ~36 bytes; a 20-byte budget must evict "aaa" then "bbb",
	// keeping the newest entry.
	removed, freed, err := s.EvictTo(20)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 || freed == 0 {
		t.Fatalf("removed=%d freed=%d, want 2 oldest entries", removed, freed)
	}
	if _, err := os.Stat(s.path("aaa")); !os.IsNotExist(err) {
		t.Fatal("oldest entry should be gone")
	}
	if _, err := os.Stat(s.path("bbb")); !os.IsNotExist(err) {
		t.Fatal("second-oldest entry should be gone")
	}
	if _, err := os.Stat(s.path("ccc")); err != nil {
		t.Fatal("newest entry must survive")
	}
}

func TestEvictToNoopUnderBudget(t *testing.T) {
	s := newTestStore(t)
	writeEntry(t, s, "x", "small", 1000)
	removed, _, err := s.EvictTo(1 << 30)
	if err != nil || removed != 0 {
		t.Fatalf("removed=%d err=%v", removed, err)
	}
}

func TestUsageAndEvictionOfMissingDir(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "does-not-exist"))
	entries, bytes, err := s.Usage()
	if err != nil || entries != 0 || bytes != 0 {
		t.Fatalf("usage on missing dir: %d %d %v", entries, bytes, err)
	}
	removed, _, err := s.EvictTo(1 << 30)
	if err != nil || removed != 0 {
		t.Fatalf("evict on missing dir: %d %v", removed, err)
	}
}

func TestSaveLoadRoundTripAndKey(t *testing.T) {
	s := newTestStore(t)
	r := &Result{Fingerprint: "fp", DurationSec: 12.5, Tracks: []FeatureTrack{{
		Analyzer: "a", Version: 1, Kind: "k", Unit: "u",
		Samples: []Sample{{T: 0, V: 1}},
	}}}
	if err := s.Save("key1", r); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load("key1")
	if err != nil || got == nil {
		t.Fatalf("load: %v %v", got, err)
	}
	if got.Tracks[0].Samples[0].T != 0 || got.DurationSec != 12.5 {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	entries, _, err := s.Usage()
	if err != nil || entries != 1 {
		t.Fatalf("usage: %d %v", entries, err)
	}
}

// TestLoadRefreshesRecency: a served entry is promoted in the eviction
// order — an old-but-hot result survives while newer never-hit entries are
// evicted first (true LRU, not FIFO-by-creation).
func TestLoadRefreshesRecency(t *testing.T) {
	s := newTestStore(t)
	writeEntry(t, s, "hot", "h", 1000) // old, then promoted by a hit
	writeEntry(t, s, "cold", "coldcold", 2000)

	if _, err := s.Load("hot"); err != nil {
		t.Fatal(err)
	}

	// Total is ~29 bytes; the 20-byte budget must evict the untouched
	// newer entry, not the older-but-just-served one.
	removed, _, err := s.EvictTo(20)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed=%d, want exactly 1", removed)
	}
	if _, err := os.Stat(s.path("cold")); !os.IsNotExist(err) {
		t.Fatal("cold newer entry should have been evicted before the hot one")
	}
	if _, err := os.Stat(s.path("hot")); err != nil {
		t.Fatal("just-served entry must survive eviction")
	}
}

// TestEvictToSparesInFlightScratch: .tmp-* files of a concurrent writer are
// counted toward the budget but never removed — deleting the half-written
// temp makes the writer's final rename fail (ENOENT on Linux) and kills the
// analysis. Their bytes still push finalized entries out so debris cannot
// wedge the budget.
func TestEvictToSparesInFlightScratch(t *testing.T) {
	s := newTestStore(t)
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	scratch := filepath.Join(s.dir, ".tmp-123456.mp4")
	if err := os.WriteFile(scratch, []byte("half-written"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeEntry(t, s, "old", "oldest-entry", 1000)
	writeEntry(t, s, "new", "n", 2000)

	removed, freed, err := s.EvictTo(10)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 || freed == 0 {
		t.Fatalf("removed=%d freed=%d, want both finalized entries", removed, freed)
	}
	if _, err := os.Stat(scratch); err != nil {
		t.Fatal("in-flight scratch must survive eviction")
	}
	if _, err := os.Stat(s.path("old")); !os.IsNotExist(err) {
		t.Fatal("oldest finalized entry should be gone")
	}
}

// TestEvictionPlanMatchesEvictTo: the dry-run plan reports exactly what a
// real eviction at the same budget would remove, and touches nothing.
func TestEvictionPlanMatchesEvictTo(t *testing.T) {
	s := newTestStore(t)
	writeEntry(t, s, "a1", "aaaa", 1000)
	writeEntry(t, s, "a2", "bb", 2000)
	writeEntry(t, s, "a3", "c", 3000)

	// ~38 bytes total; a 20-byte budget evicts a1 then a2, keeping a3.
	count, bytes, err := s.EvictionPlan(20)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 || bytes == 0 {
		t.Fatalf("plan: count=%d bytes=%d, want 2 entries", count, bytes)
	}
	// Planning must not delete anything.
	for _, key := range []string{"a1", "a2", "a3"} {
		if _, err := os.Stat(s.path(key)); err != nil {
			t.Fatalf("plan must keep %s on disk: %v", key, err)
		}
	}

	removed, freed, err := s.EvictTo(20)
	if err != nil {
		t.Fatal(err)
	}
	if removed != count || freed != bytes {
		t.Fatalf("evict removed=%d freed=%d, plan said %d/%d", removed, freed, count, bytes)
	}
	// The plan's chosen victims are the same oldest entries.
	if _, err := os.Stat(s.path("a3")); err != nil {
		t.Fatal("newest entry must survive")
	}
}

// TestProxyEvictionPlanNoTouch: the proxy plan is read-only as well.
func TestProxyEvictionPlanNoTouch(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "proxy", "abc.mp4")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("proxy-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := NewProxyStore(dir)
	count, bytes, err := store.EvictionPlan(1)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || bytes != int64(len("proxy-bytes")) {
		t.Fatalf("plan: count=%d bytes=%d", count, bytes)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal("plan must keep the proxy on disk")
	}
}
