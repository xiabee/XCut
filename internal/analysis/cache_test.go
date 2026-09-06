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
