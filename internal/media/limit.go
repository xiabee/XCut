package media

import (
	"context"
	"os/exec"
	"sync"
	"sync/atomic"

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

	// ffmpegMemoryLimitMB is the per-process memory cap handed to the Windows
	// job object (0 = uncapped). Stored before any child can start: the CLI
	// wires it from config before the first exec, and ensureJob reads it once
	// when the job object is created.
	ffmpegMemoryLimitMB atomic.Int64
)

// SetProcessMemoryLimitMB configures the per-ffmpeg memory cap applied by the
// Windows job object (see jobobject_windows.go). 0 or less means uncapped,
// which is the default: a cap that is too tight fails real renders with
// allocation errors, so capping is opt-in. On other platforms this is a
// no-op — the cap surfaces only where the OS backstop exists.
func SetProcessMemoryLimitMB(mb int) {
	if mb < 0 {
		mb = 0
	}
	ffmpegMemoryLimitMB.Store(int64(mb))
}

// processMemoryLimitMB reports the configured per-process cap in MB.
func processMemoryLimitMB() int64 {
	return ffmpegMemoryLimitMB.Load()
}

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
	// Explicit Start/Wait so the process joins the kill-on-close job while
	// alive (attachJob).
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	attachJob(cmd.Process)
	// Read the buffer AFTER Wait(): the expression below was evaluated
	// left-to-right and handed back the pre-Run (empty) slice, stripping the
	// stderr tail from every failure diagnostic.
	err = cmd.Wait()
	return buf.b, err
}
