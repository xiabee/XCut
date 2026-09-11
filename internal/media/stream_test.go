package media

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xiabee/XCut/internal/xcerr"
)

// TestHelperStream is re-executed by the StreamStdout tests. The mode and
// payload come from the environment so one helper covers every branch:
//
//	bytes      write N patterned bytes to stdout, exit 0
//	flood      write 1 MiB zero chunks until killed (4 GiB cap), exit 0
//	fail       write stdout + stderr detail, exit 3
//	sleepwrite write "first", pause, write "second", exit 0
func TestHelperStream(t *testing.T) {
	switch os.Getenv("XCUT_TEST_STREAM") {
	case "":
		return
	case "bytes":
		n, _ := strconv.Atoi(os.Getenv("XCUT_TEST_STREAM_BYTES"))
		pattern := []byte("0123456789abcdef")
		for n > 0 {
			c := n
			if c > len(pattern) {
				c = len(pattern)
			}
			if _, err := os.Stdout.Write(pattern[:c]); err != nil {
				os.Exit(1)
			}
			n -= c
		}
	case "flood":
		chunk := make([]byte, 1<<20)
		written := 0
		for written < 4<<30 {
			if _, err := os.Stdout.Write(chunk); err != nil {
				// Killed by the budget enforcement — the expected exit.
				os.Exit(0)
			}
			written += len(chunk)
		}
	case "fail":
		fmt.Fprint(os.Stdout, "partial-output")
		fmt.Fprint(os.Stderr, "boom-detail: something went wrong")
		os.Exit(3)
	case "sleepwrite":
		fmt.Fprint(os.Stdout, "first")
		time.Sleep(600 * time.Millisecond)
		fmt.Fprint(os.Stdout, "second")
	}
	os.Exit(0)
}

// TestStreamStdoutDeliversAllBytes: the sink must see every byte the child
// wrote, in order, in chunks no larger than streamChunkSize.
func TestStreamStdoutDeliversAllBytes(t *testing.T) {
	const total = 1<<20 + 12345 // spans many chunks plus a ragged tail
	t.Setenv("XCUT_TEST_STREAM", "bytes")
	t.Setenv("XCUT_TEST_STREAM_BYTES", strconv.Itoa(total))

	pattern := []byte("0123456789abcdef")
	want := make([]byte, total)
	for i := range want {
		want[i] = pattern[i%len(pattern)]
	}

	var got []byte
	err := StreamStdout(context.Background(), os.Args[0], func(chunk []byte) error {
		if len(chunk) > streamChunkSize {
			t.Errorf("chunk of %d bytes exceeds the %d-byte budget", len(chunk), streamChunkSize)
		}
		got = append(got, chunk...)
		return nil
	}, "-test.run=TestHelperStream$")
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if len(got) != total {
		t.Fatalf("received %d bytes, want %d", len(got), total)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("byte %d = %c, want %c (stream corrupted)", i, got[i], want[i])
		}
	}
}

// TestStreamStdoutBudgetKill: output beyond MaxStreamBytes must kill the
// child and report a resource-limit error, not buffer or hang.
func TestStreamStdoutBudgetKill(t *testing.T) {
	t.Setenv("XCUT_TEST_STREAM", "flood")

	err := StreamStdout(context.Background(), os.Args[0], func(chunk []byte) error {
		return nil
	}, "-test.run=TestHelperStream$")
	if !xcerr.IsCode(err, xcerr.CodeResourceLimit) {
		t.Fatalf("overflow err = %v, want CodeResourceLimit", err)
	}
}

// TestStreamStdoutSinkErrorAborts: a sink error stops the stream and comes
// back verbatim; the child is killed so the call cannot hang on a writer
// that never ends.
func TestStreamStdoutSinkErrorAborts(t *testing.T) {
	t.Setenv("XCUT_TEST_STREAM", "flood")

	sinkErr := errors.New("sink exploded")
	start := time.Now()
	err := StreamStdout(context.Background(), os.Args[0], func(chunk []byte) error {
		return sinkErr
	}, "-test.run=TestHelperStream$")
	if !errors.Is(err, sinkErr) {
		t.Fatalf("err = %v, want the sink error verbatim", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("sink abort took %s — the child was not killed", elapsed)
	}
}

// TestStreamStdoutChildFailure: a failing child surfaces CodeFFmpegFailure
// with the tail of its stderr (the part ffmpeg diagnostics live in).
func TestStreamStdoutChildFailure(t *testing.T) {
	t.Setenv("XCUT_TEST_STREAM", "fail")

	err := StreamStdout(context.Background(), os.Args[0], func(chunk []byte) error {
		return nil
	}, "-test.run=TestHelperStream$")
	if !xcerr.IsCode(err, xcerr.CodeFFmpegFailure) {
		t.Fatalf("err = %v, want CodeFFmpegFailure", err)
	}
	if !strings.Contains(err.Error(), "boom-detail") {
		t.Fatalf("error must carry the child's stderr tail, got: %v", err)
	}
}

// TestStreamStdoutCancellation: cancelling the context mid-stream kills the
// child and maps to CodeCancelled (the onset analyzer's per-call timeout
// rides on this path).
func TestStreamStdoutCancellation(t *testing.T) {
	t.Setenv("XCUT_TEST_STREAM", "sleepwrite")

	ctx, cancel := context.WithCancel(context.Background())
	start := time.Now()
	err := StreamStdout(ctx, os.Args[0], func(chunk []byte) error {
		cancel() // kill while the child sleeps between writes
		return nil
	}, "-test.run=TestHelperStream$")
	if !xcerr.IsCode(err, xcerr.CodeCancelled) {
		t.Fatalf("err = %v, want CodeCancelled", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("cancellation took %s — the child outlived its context", elapsed)
	}
}

// TestStreamStdoutArgumentValidation: the cheap guards.
func TestStreamStdoutArgumentValidation(t *testing.T) {
	err := StreamStdout(context.Background(), "", func([]byte) error { return nil })
	if !xcerr.IsCode(err, xcerr.CodeInternal) {
		t.Fatalf("empty bin: %v, want CodeInternal", err)
	}
	err = StreamStdout(context.Background(), "anything", nil)
	if !xcerr.IsCode(err, xcerr.CodeInternal) {
		t.Fatalf("nil sink: %v, want CodeInternal", err)
	}
}
