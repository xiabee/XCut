package media

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xiabee/XCut/internal/xcerr"
)

func TestAcquireRespectsLimitAndCancellation(t *testing.T) {
	SetProcessLimit(1)
	defer SetProcessLimit(0)

	release1, err := acquire(context.Background())
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = acquire(ctx)
	if !xcerr.IsCode(err, xcerr.CodeCancelled) {
		t.Fatalf("cancelled acquire err = %v, want CodeCancelled", err)
	}

	done := make(chan struct{})
	go func() {
		r, err := acquire(context.Background())
		if err != nil {
			t.Errorf("blocked acquire: %v", err)
		}
		r()
		close(done)
	}()
	release1()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("acquire did not unblock after release")
	}
}

// TestRunSerializesUnderLimit proves Run honors the process cap with real
// processes: with a limit of 1, two helper invocations must not overlap in
// time. The helper (this same test binary, re-executed by Run) records its
// start and end instants in a shared mark file; children inherit the env
// the parent sets with t.Setenv.
func TestRunSerializesUnderLimit(t *testing.T) {
	SetProcessLimit(1)
	defer SetProcessLimit(0)

	markFile := filepath.Join(t.TempDir(), "marks.txt")
	t.Setenv("XCUT_TEST_HELPER_MARK", "1")
	t.Setenv("XCUT_TEST_MARK_FILE", markFile)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, errs[i] = Run(context.Background(), os.Args[0],
				"-test.run=TestHelperProcessMark$", "-test.v")
		}(i)
	}
	wg.Wait()

	intervals, err := readMarks(markFile)
	if err != nil {
		t.Fatalf("helper marks unreadable: %v (helper errors: %v)", err, errors.Join(errs...))
	}
	if len(intervals) != 2 {
		t.Fatalf("got %d helper runs, want 2", len(intervals))
	}
	a, b := intervals[0], intervals[1]
	if a.end > b.start && b.end > a.start {
		t.Fatalf("helper runs overlapped: a=[%d,%d] b=[%d,%d]", a.start, a.end, b.start, b.end)
	}
}

// TestHelperProcessMark is re-executed by TestRunSerializesUnderLimit. It
// appends "<pid> start <ns>" / "<pid> end <ns>" lines to the mark file; the
// pid distinguishes concurrent helper instances.
func TestHelperProcessMark(t *testing.T) {
	if os.Getenv("XCUT_TEST_HELPER_MARK") == "" {
		return
	}
	id := strconv.Itoa(os.Getpid())
	markFile := os.Getenv("XCUT_TEST_MARK_FILE")
	writeMark(markFile, id, "start")
	time.Sleep(250 * time.Millisecond)
	writeMark(markFile, id, "end")
	os.Exit(0)
}

func writeMark(markFile, id, phase string) {
	f, err := os.OpenFile(markFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mark open: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()
	line := id + " " + phase + " " + strconv.FormatInt(time.Now().UnixNano(), 10) + "\n"
	if _, err := f.WriteString(line); err != nil {
		fmt.Fprintf(os.Stderr, "mark write: %v\n", err)
		os.Exit(1)
	}
}

type markInterval struct{ start, end int64 }

// readMarks parses "<id> <phase> <ns>" lines into per-id intervals, ordered
// by start time. Small appends to an O_APPEND file are atomic in practice on
// Windows and POSIX, so lines do not interleave.
func readMarks(path string) ([]markInterval, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	byID := map[string]markInterval{}
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		id, phase := fields[0], fields[1]
		ns, perr := strconv.ParseInt(fields[2], 10, 64)
		if perr != nil {
			return nil, fmt.Errorf("bad mark line %q: %w", line, perr)
		}
		cur := byID[id]
		switch phase {
		case "start":
			cur.start = ns
		case "end":
			cur.end = ns
		}
		byID[id] = cur
	}
	if len(byID) == 0 {
		return nil, errors.New("no marks found")
	}
	out := make([]markInterval, 0, len(byID))
	for _, iv := range byID {
		out = append(out, iv)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].start < out[j].start })
	return out, nil
}
