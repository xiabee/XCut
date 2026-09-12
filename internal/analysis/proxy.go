package analysis

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/xcerr"
)

// ProxyStore manages content-addressed low-res analysis proxies under
// <workspace>/cache/proxy. Entries are keyed by the SOURCE fingerprint plus
// the analysis geometry (width, fps), so identical content shares one proxy
// and repeated analyze runs decode a tiny file instead of the original —
// while a changed analysis_width / frame_sample_fps can never be served a
// proxy encoded for a different canvas.
//
// Decision logic (configurable via resource.proxy_enabled plus the analysis
// sampling knobs): a proxy is only worth generating when the source is wider
// than the analysis canvas — otherwise every analyzer pass would decode the
// same number of pixels plus one re-encode. The proxy is encoded at exactly
// the analysis geometry (AnalysisWidth wide, FrameSampleFPS), which turns the
// analyzers' own fps/scale filters into no-ops.
type ProxyStore struct {
	dir      string
	MaxBytes int64 // 0 disables budget eviction
}

// NewProxyStore builds a proxy cache rooted at the workspace cache dir.
func NewProxyStore(cacheDir string) *ProxyStore {
	return &ProxyStore{dir: filepath.Join(cacheDir, "proxy")}
}

// Dir exposes the store's on-disk location (CLI reporting).
func (s *ProxyStore) Dir() string { return s.dir }

// path encodes the geometry into the name: a config change (analysis_width,
// frame_sample_fps) must regenerate, never silently reuse a proxy built for
// the old canvas — the cache key would claim the new geometry while the
// pixels come from the old one. Stale-geometry entries age out through
// budget eviction.
func (s *ProxyStore) path(fingerprint string, width int, fps float64) string {
	return filepath.Join(s.dir, fingerprint+".w"+strconv.Itoa(width)+".f"+formatFPS(fps)+".mp4")
}

// Usage returns the number of proxy files and total bytes on disk.
func (s *ProxyStore) Usage() (count int, bytes int64, err error) {
	return dirUsage(s.dir)
}

// EvictTo prunes proxies down to at most maxBytes, least-recently-used
// first (an Ensure hit refreshes the reused proxy's recency).
func (s *ProxyStore) EvictTo(maxBytes int64) (removed int, freed int64, err error) {
	return evictDirTo(s.dir, maxBytes)
}

// Ensure returns the proxy path for the source, generating it when missing.
// used=false means the decision declined (source not wider than the analysis
// canvas, or probing failed benignly) — the caller must analyze the original
// file instead. Generation is atomic (unique temp + rename) so concurrent
// analyzers racing on the same fingerprint never observe a partial file.
// proxyThreads is the encode's own thread budget; <=0 inherits the tools'
// default cap (the one-shot encode is decode-bound, so a higher budget here
// is safe and measured to cut cold-start cost ~4x).
func (s *ProxyStore) Ensure(ctx context.Context, tools media.Tools, srcPath, fingerprint string, width int, fps float64, proxyThreads int, log *slog.Logger) (proxyPath string, used bool, err error) {
	probe, err := media.ProbeFile(ctx, tools, srcPath)
	if err != nil {
		return "", false, xcerr.E(xcerr.CodeInternal, "cannot probe source for proxy decision", err)
	}
	// Decision: decode savings only exist when the source exceeds the
	// analysis canvas. Even-width requirement keeps chroma alignment.
	target := width - width%2
	if probe.Width <= target {
		return "", false, nil
	}

	p := s.path(fingerprint, target, fps)
	if _, err := os.Stat(p); err == nil {
		touchRecency(p) // LRU: a reused proxy keeps its place in the cache
		return p, true, nil
	}

	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return "", false, xcerr.E(xcerr.CodeInternal, "cannot create proxy cache dir", err)
	}
	tmp, err := os.CreateTemp(s.dir, ".tmp-*.mp4")
	if err != nil {
		return "", false, xcerr.E(xcerr.CodeInternal, "cannot create proxy temp file", err)
	}
	tmpName := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return "", false, xcerr.E(xcerr.CodeInternal, "cannot create proxy temp file", err)
	}

	// The encode mirrors the analyzers' canvas: same width, same sampling
	// fps, audio kept (RMS/onset analyzers read it) at a modest bitrate.
	threads := proxyThreads
	if threads <= 0 {
		threads = tools.Threads
	}
	args := []string{
		"-hide_banner", "-nostdin", "-v", "error", "-y",
		"-threads", strconv.Itoa(threadCap(threads)),
		"-i", srcPath,
		"-vf", fmt.Sprintf("scale=%d:-2,fps=%s", target, strconv.FormatFloat(fps, 'f', -1, 64)),
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "23",
		"-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", "96k", "-ac", "2", "-ar", "48000",
		"-movflags", "+faststart",
		tmpName,
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	_, stderr, runErr := media.Run(cctx, tools.FFmpeg, args...)
	if runErr != nil {
		_ = os.Remove(tmpName)
		if cctx.Err() != nil {
			return "", false, xcerr.E(xcerr.CodeInternal, "proxy generation timed out or was cancelled", cctx.Err())
		}
		return "", false, xcerr.E(xcerr.CodeInternal, "proxy generation failed",
			fmt.Errorf("%v: %s", runErr, tailStr(stderr, 300)))
	}
	if err := os.Rename(tmpName, p); err != nil {
		_ = os.Remove(tmpName)
		return "", false, xcerr.E(xcerr.CodeInternal, "cannot finalize proxy file", err)
	}

	// Budget is housekeeping, not correctness (mirrors the analysis store).
	// Eviction runs AFTER the new file lands, so an absurdly small budget
	// can evict the just-created proxy — verify it survived and fall back
	// to the original rather than handing analysis a missing file.
	if s.MaxBytes > 0 {
		if _, _, err := s.EvictTo(s.MaxBytes); err != nil {
			log.Warn("proxy cache eviction failed", "err", err)
		}
		if _, err := os.Stat(p); err != nil {
			log.Warn("proxy evicted immediately (budget below one proxy); analyzing original", "err", err)
			return "", false, nil
		}
	}
	log.Debug("analysis proxy ready", "source_width", probe.Width, "proxy_width", target, "fps", fps)
	return p, true, nil
}

func threadCap(n int) int {
	if n <= 0 {
		return 2
	}
	return n
}

func tailStr(b []byte, n int) string {
	if len(b) > n {
		return string(b[len(b)-n:])
	}
	return string(b)
}
