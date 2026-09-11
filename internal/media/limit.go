package media

import (
	"context"
	"os/exec"
	"sync"

	"github.com/xiabee/XCut/internal/xcerr"
)

// processLimiter caps concurrent ffmpeg/ffprobe processes process-wide.
// media.Run / RunCombined / StreamStdout acquire it internally, so every
// product exec path (probe, analyzers, proxy, render) is capped by
// resource.max_ffmpeg_processes. The CLI wires it from config; queue-level
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

// RunCombined executes bin like Run but collects stdout and stderr into one
// capped buffer (last bytes win), under the process limiter. Used where the
// child's diagnostic output only matters on failure (renders).
func RunCombined(ctx context.Context, bin string, args ...string) ([]byte, error) {
	release, err := acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	cmd := exec.CommandContext(ctx, bin, args...)
	buf := &cappedBuffer{max: maxCapturedOutput}
	cmd.Stdout = buf
	cmd.Stderr = buf
	// Read the buffer AFTER Run(): the expression below was evaluated
	// left-to-right and handed back the pre-Run (empty) slice, stripping the
	// stderr tail from every failure diagnostic.
	err = cmd.Run()
	return buf.b, err
}
