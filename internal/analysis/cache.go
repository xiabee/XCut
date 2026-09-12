package analysis

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/xiabee/XCut/internal/xcerr"
)

// ConfigKey captures the analysis-relevant configuration so a change to
// sampling settings invalidates cached results.
type ConfigKey struct {
	SampleFPS     float64 `json:"sample_fps"`
	AnalysisWidth int     `json:"analysis_width"`
	Proxy         bool    `json:"proxy,omitempty"` // analyzed a generated proxy, not the original
}

// cacheKey = SHA256(fingerprint | analyzer names+versions | config). Stored
// results are self-describing, so a mismatch can never be silently served
// (DECISIONS D9: fingerprint-based invalidation).
func cacheKey(fingerprint string, analyzers []Analyzer, cfg ConfigKey) string {
	h := sha256.New()
	h.Write([]byte(fingerprint))
	h.Write([]byte{0})
	for _, a := range analyzers {
		h.Write([]byte(a.Name()))
		fmt.Fprintf(h, "|v%d", a.Version())
		h.Write([]byte{0})
	}
	cb, _ := json.Marshal(cfg)
	h.Write(cb)
	return hex.EncodeToString(h.Sum(nil))
}

// Store is the on-disk analysis cache under <workspace>/cache/analysis.
// When MaxBytes > 0, every Save is followed by an eviction pass to keep the
// cache within budget (resource.max_cache_gb).
type Store struct {
	dir      string
	MaxBytes int64
}

// NewStore builds a cache rooted at the workspace cache dir.
func NewStore(cacheDir string) *Store {
	return &Store{dir: filepath.Join(cacheDir, "analysis")}
}

// Dir exposes the store's on-disk location (CLI reporting).
func (s *Store) Dir() string { return s.dir }

// Key exposes the cache key computation (used by CLI logging).
func Key(fingerprint string, analyzers []Analyzer, cfg ConfigKey) string {
	return cacheKey(fingerprint, analyzers, cfg)
}

// Load returns a cached result or nil on miss. Corrupted cache entries are
// treated as misses (and removed), never as errors.
func (s *Store) Load(key string) (*Result, error) {
	path := s.path(key)
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, xcerr.E(xcerr.CodeInternal, "cannot read analysis cache", err)
	}
	var r Result
	if err := json.Unmarshal(b, &r); err != nil {
		_ = os.Remove(path) // poisoned entry: drop it
		return nil, nil
	}
	touchRecency(path) // LRU: a served entry keeps its place in the cache
	return &r, nil
}

// Save writes a result atomically (temp file + rename, CRASH SAFETY) and is
// idempotent for identical content.
func (s *Store) Save(key string, r *Result) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return xcerr.E(xcerr.CodeInternal, "cannot create analysis cache dir", err)
	}
	path := s.path(key)
	b, err := json.Marshal(r)
	if err != nil {
		return xcerr.E(xcerr.CodeInternal, "cannot serialize analysis result", err)
	}
	tmp, err := os.CreateTemp(s.dir, ".tmp-*")
	if err != nil {
		return xcerr.E(xcerr.CodeInternal, "cannot create analysis cache temp file", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		_ = os.Remove(tmpName)
		return xcerr.E(xcerr.CodeInternal, "cannot write analysis cache", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return xcerr.E(xcerr.CodeInternal, "cannot close analysis cache temp file", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return xcerr.E(xcerr.CodeInternal, "cannot finalize analysis cache entry", err)
	}
	return nil
}

func (s *Store) path(key string) string {
	return filepath.Join(s.dir, key+".json")
}

// Usage returns the number of cache entries and total bytes on disk.
func (s *Store) Usage() (count int, bytes int64, err error) {
	return dirUsage(s.dir)
}

// EvictTo prunes the cache down to at most maxBytes, least-recently-used
// first. A Load hit refreshes the served entry's recency, so entries the
// pipeline keeps re-reading survive while never-hit entries age out.
// Returns how many entries were removed and bytes reclaimed.
func (s *Store) EvictTo(maxBytes int64) (removed int, freed int64, err error) {
	return evictDirTo(s.dir, maxBytes)
}

// EvictionPlan reports what EvictTo would remove at this budget without
// touching anything (dry-run reporting for `xcut cleanup`).
func (s *Store) EvictionPlan(maxBytes int64) (count int, bytes int64, err error) {
	return planEvictDirTo(s.dir, maxBytes)
}
