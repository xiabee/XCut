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
// <workspace>/cache/proxy. Entries are keyed by the SOURCE fingerprint, so
// identical content shares one proxy regardless of where it lives on disk,
// and repeated analyze runs decode a tiny file instead of the original.
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

func (s *ProxyStore) path(fingerprint string) string {
	return filepath.Join(s.dir, fingerprint+".mp4")
}

// Usage returns the number of proxy files and total bytes on disk.
func (s *ProxyStore) Usage() (count int, bytes int64, err error) {
	return dirUsage(s.dir)
}

// EvictTo prunes proxies down to at most maxBytes, oldest first.
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

	p := s.path(fingerprint)
	if _, err := os.Stat(p); err == nil {
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
	_, stderr, runErr := media.RunLimited(cctx, tools.FFmpeg, args...)
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
	if s.MaxBytes > 0 {
		if _, _, err := s.EvictTo(s.MaxBytes); err != nil {
			log.Warn("proxy cache eviction failed", "err", err)
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
