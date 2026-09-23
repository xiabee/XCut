package media

import (
	"context"
	"fmt"
	"io"
	"os/exec"

	"github.com/xiabee/XCut/internal/xcerr"
)

// streamChunkSize is the fixed read size handed to the sink.
const streamChunkSize = 64 << 10

// MaxStreamBytes caps the total stdout a streaming invocation may deliver
// (bounded intake: at 16 kHz mono s16le this allows ≈ 4.5 hours of audio).
const MaxStreamBytes = 512 << 20

// StreamStdout executes bin with args (same security contract as Run) under
// the process limiter and a context deadline, streaming stdout to sink in
// fixed-size chunks. stdout is never fully buffered — memory stays flat for
// arbitrarily large outputs. stderr is captured up to 64 KB for error
// reporting. A sink error aborts the process and is returned verbatim.
func StreamStdout(ctx context.Context, bin string, sink func(chunk []byte) error, args ...string) error {
	if bin == "" {
		return xcerr.E(xcerr.CodeInternal, "empty binary path", nil)
	}
	if sink == nil {
		return xcerr.E(xcerr.CodeInternal, "nil stream sink", nil)
	}
	release, err := acquire(ctx)
	if err != nil {
		return err
	}
	defer release()

	wbin, wargs := wrapChild(bin, args...)
	cmd := exec.CommandContext(ctx, wbin, wargs...)
	// stderr is diagnostics: last-64KB wins (bounded intake — a corrupt file
	// can emit decode errors per frame for the whole pass).
	errBuf := &cappedBuffer{max: 64 << 10}
	cmd.Stderr = errBuf
	stdout, pipeErr := cmd.StdoutPipe()
	if pipeErr != nil {
		return xcerr.E(xcerr.CodeFFmpegFailure, "cannot create process pipe", pipeErr)
	}
	if err := cmd.Start(); err != nil {
		return xcerr.E(xcerr.CodeFFmpegFailure, "cannot start "+bin, err)
	}
	attachJob(cmd.Process)

	buf := make([]byte, streamChunkSize)
	var total int64
	for {
		n, readErr := stdout.Read(buf)
		if n > 0 {
			total += int64(n)
			if total > MaxStreamBytes {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				return xcerr.E(xcerr.CodeResourceLimit,
					"process output exceeded the streaming budget", nil)
			}
			if serr := sink(buf[:n]); serr != nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				return serr
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			_ = cmd.Wait()
			return xcerr.E(xcerr.CodeFFmpegFailure, "cannot read process output", readErr)
		}
	}

	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return xcerr.E(xcerr.CodeCancelled, "streaming process cancelled", ctx.Err())
		}
		const tail = 2000
		s := string(errBuf.b)
		if len(s) > tail {
			s = s[len(s)-tail:]
		}
		return xcerr.E(xcerr.CodeFFmpegFailure, "process failed", fmt.Errorf("%v: %s", err, s))
	}
	return nil
}
