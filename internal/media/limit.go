package media

import (
	"context"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xiabee/XCut/internal/xcerr"
)

// pipeDrainGrace is how long a finished child may keep its descriptors open before they
// are closed on it. A child that has exited and closed its ends is drained in
// microseconds, so this window only ever elapses when some *other* process still holds
// the write end — a decoder plugin or a sidecar's helper that outlived the tool we ran.
// Without it, exec waits for the pipe to drain and the per-call deadline stops being a
// ceiling: measured on linux-ci at `d1bb82d`, a 1 s budget on `Run` and on `StreamStdout`
// each took 60.01 s, because a grandchild that slept for a minute inherited the descriptor.
// Set at every exec site in this package.
const pipeDrainGrace = time.Second

// processLimiter caps concurrent ffmpeg/ffprobe processes process-wide.
// media.Run / RunCombined / StreamStdout acquire it internally, so every
// product exec path (probe, analyzers, proxy, render) is capped by
// resource.max_ffmpeg_processes. The CLI wires it from config; queue-level
// job limits bound concurrency above this. Phase 1: one global limiter —
// honest and sufficient for a single-process tool.
var (
	limiterMu sync.Mutex
	limiter   chan struct{}

	// ffmpegMemoryLimitMB is the per-process memory cap applied to every child:
	// through the Windows job object (jobobject_windows.go) or, on Linux, through a
	// transient systemd scope (sandbox_linux.go). 0 = uncapped. Stored before any
	// child can start: the CLI wires it from config before the first exec, and both
	// mechanisms read it when they resolve their posture.
	ffmpegMemoryLimitMB atomic.Int64
)

// SetProcessMemoryLimitMB configures the per-ffmpeg memory cap applied to each
// child (0 or less means uncapped, which is what an explicit `0` asks for; the
// shipped default is 1536 — see DECISIONS D16). A cap that is too tight fails real
// renders with allocation errors, which is why the number is a ceiling over the
// largest legitimate child rather than a comfortable margin, and why the Linux
// wrapper probes once and falls back to running children unwrapped rather than
// letting a refused scope look like an FFmpeg failure.
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
	wbin, wargs := wrapChild(bin, args...)
	cmd := exec.CommandContext(ctx, wbin, wargs...)
	cmd.WaitDelay = pipeDrainGrace
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

// ProcessLimit reports the configured global ffmpeg/ffprobe concurrency cap
// (0 = unlimited). Callers that spawn one child per unit of work use it as
// their default worker count so raising resource.max_ffmpeg_processes raises
// their parallelism without a second knob.
func ProcessLimit() int {
	limiterMu.Lock()
	defer limiterMu.Unlock()
	if limiter == nil {
		return 0
	}
	return cap(limiter)
}
