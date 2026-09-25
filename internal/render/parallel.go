package render

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/timeline"
	"github.com/xiabee/XCut/internal/xcerr"
)

// normalizeClips fills parts with one normalized file per clip. Workers run
// min(ClipWorkers, clips) at a time: each drives one ffmpeg child, so the
// global process limiter stays the real ceiling. The first failure cancels
// the rest and is the error this function returns (caller cancellation
// surfaces as CodeCancelled).
func normalizeClips(ctx context.Context, tl *timeline.Timeline, clips []timeline.Clip, fades [][2]float64, parts []string, opts Options) error {
	workers := opts.ClipWorkers
	if workers <= 0 {
		if n := media.ProcessLimit(); n > 0 {
			workers = n
		} else {
			workers = defaultClipWorkers
		}
	}
	if workers > len(clips) {
		workers = len(clips)
	}
	if workers <= 1 {
		return normalizeClipsSerial(ctx, tl, clips, fades, parts, opts)
	}

	total := len(clips)
	var done atomic.Int64
	var mu sync.Mutex
	var firstErr error

	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	fail := func(err error) {
		mu.Lock()
		defer mu.Unlock()
		if firstErr == nil {
			firstErr = err
		}
		cancel()
	}

	var wg sync.WaitGroup
	next := make(chan int)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range next {
				if workerCtx.Err() != nil {
					return
				}
				probe, err := media.ProbeFile(workerCtx, opts.Tools, clips[idx].SourcePath)
				if err != nil {
					fail(xcerr.E(xcerr.CodeRenderFailure, "cannot probe source for clip "+clips[idx].ID, err))
					return
				}
				part, err := normalizeClip(workerCtx, tl, clips[idx], idx, fades[idx], probe.HasAudio, opts)
				if err != nil {
					fail(err)
					return
				}
				parts[idx] = part
				if opts.TempBudgetBytes > 0 {
					mu.Lock()
					used := dirBytes(opts.TempDir)
					mu.Unlock()
					if used > opts.TempBudgetBytes {
						fail(xcerr.E(xcerr.CodeResourceLimit,
							fmt.Sprintf("render scratch exceeded its budget (%s in use, budget %s) — raise resource.max_temp_gb or use a shorter timeline",
								humanBytes(used), humanBytes(opts.TempBudgetBytes)), nil))
						return
					}
				}
				// OnProgress is delivered under the lock: callers code
				// against a serial callback (the pipeline's
				// monotonic-percentage closure reads and writes its `last`
				// unsynchronized), and done.Add inside the lock keeps the
				// delivered counts strictly increasing. This is the call
				// site that raced exactly that closure — caught by the
				// gate's race subset.
				mu.Lock()
				opts.OnProgress(int(done.Add(1)), total)
				mu.Unlock()
			}
		}()
	}

dispatch:
	for i := range clips {
		select {
		case next <- i:
		case <-workerCtx.Done():
			break dispatch
		}
	}
	close(next)
	wg.Wait()

	if firstErr != nil {
		return firstErr
	}
	if err := ctx.Err(); err != nil {
		return xcerr.E(xcerr.CodeCancelled, "render cancelled", err)
	}
	return nil
}

func normalizeClipsSerial(ctx context.Context, tl *timeline.Timeline, clips []timeline.Clip, fades [][2]float64, parts []string, opts Options) error {
	for i := range clips {
		if err := ctx.Err(); err != nil {
			return xcerr.E(xcerr.CodeCancelled, "render cancelled", err)
		}
		probe, err := media.ProbeFile(ctx, opts.Tools, clips[i].SourcePath)
		if err != nil {
			return xcerr.E(xcerr.CodeRenderFailure, "cannot probe source for clip "+clips[i].ID, err)
		}
		part, err := normalizeClip(ctx, tl, clips[i], i, fades[i], probe.HasAudio, opts)
		if err != nil {
			return err
		}
		parts[i] = part
		if opts.TempBudgetBytes > 0 {
			if used := dirBytes(opts.TempDir); used > opts.TempBudgetBytes {
				return xcerr.E(xcerr.CodeResourceLimit,
					fmt.Sprintf("render scratch exceeded its budget (%s in use, budget %s) — raise resource.max_temp_gb or use a shorter timeline",
						humanBytes(used), humanBytes(opts.TempBudgetBytes)), nil)
			}
		}
		opts.OnProgress(i+1, len(clips))
	}
	return nil
}

// defaultClipWorkers is the fallback when no process limit is configured.
const defaultClipWorkers = 2
