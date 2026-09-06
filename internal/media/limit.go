package media

import (
	"context"
	"sync"

	"github.com/xiabee/XCut/internal/xcerr"
)

// processLimiter caps concurrent ffmpeg/ffprobe processes process-wide.
// The CLI wires it from config (resource.max_ffmpeg_processes); queue-level
// job limits bound concurrency above this. Phase 1: one global limiter —
// honest and sufficient for a single-process tool.
var (
	limiterMu sync.Mutex
	limiter   chan struct{}
)

// SetProcessLimit configures the global external-process concurrency cap.
// Values < 1 disable limiting (not recommended; tests use this).
func SetProcessLimit(n int) {
	limiterMu.Lock()
	defer limiterMu.Unlock()
	if n < 1 {
		limiter = nil
		return
	}
	limiter = make(chan struct{}, n)
}

func acquire(ctx context.Context) (release func(), err error) {
	limiterMu.Lock()
	ch := limiter
	limiterMu.Unlock()
	if ch == nil {
		return func() {}, nil
	}
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, xcerr.E(xcerr.CodeCancelled, "cancelled waiting for process slot", ctx.Err())
	}
}

// RunLimited executes bin like Run but under the process limiter.
func RunLimited(ctx context.Context, bin string, args ...string) (stdout, stderr []byte, err error) {
	release, err := acquire(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer release()
	return Run(ctx, bin, args...)
}
