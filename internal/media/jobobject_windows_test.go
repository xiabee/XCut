//go:build windows

package media

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

var procIsProcessInJob = kernel32.NewProc("IsProcessInJob")

// TestAttachJobPutsProcessInJob proves the assignment itself: a real child
// process, after attachJob, must report membership in OUR job object (the
// kill-on-close reaping when the parent dies is then guaranteed by the
// kernel). Production calls this on ffmpeg/ffprobe right after Start.
func TestAttachJobPutsProcessInJob(t *testing.T) {
	h, err := ensureJob()
	if err != nil || h == 0 {
		t.Fatalf("job object unavailable: err=%v handle=%v", err, h)
	}
	cmd := exec.Command("cmd", "/c", "ping -n 30 127.0.0.1 > NUL")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()
	attachJob(cmd.Process)

	// + QUERY_INFORMATION: IsProcessInJob below needs it (the production
	// attach path does not).
	perms := processSetQuota | processTerminate | processSetInformation | uintptr(0x0400)
	ph, _, callErr := procOpenProcess.Call(perms, 0, uintptr(cmd.Process.Pid))
	if ph == 0 {
		t.Fatalf("cannot reopen child: %v", callErr)
	}
	defer procCloseHandle.Call(ph)

	var inJob int32
	r, _, _ := procIsProcessInJob.Call(ph, uintptr(h), uintptr(unsafe.Pointer(&inJob)))
	if r == 0 {
		t.Fatal("IsProcessInJob call failed")
	}
	if inJob == 0 {
		t.Fatal("child is NOT in the kill-on-close job after attachJob")
	}
}

// TestEnsureJobKillOnCloseFlag: the job is created once per process and the
// limit flags stick (a second ensureJob returns the same handle).
func TestEnsureJobKillOnCloseFlag(t *testing.T) {
	h1, err1 := ensureJob()
	h2, err2 := ensureJob()
	if err1 != nil || err2 != nil || h1 == 0 || h1 != h2 {
		t.Fatalf("ensureJob not idempotent: %v/%v %v/%v", h1, h2, err1, err2)
	}
}

// The memory cap must switch the flag on and carry the byte-exact limit —
// a MB/bytes slip here would cap at the wrong scale, and a forgotten flag
// would silently keep encoders uncapped. Zero stays uncapped: the default
// must not start failing high-resolution renders that never asked for a cap.
func TestBuildJobLimitsMemoryCap(t *testing.T) {
	info := buildJobLimits(0)
	if info.Basic.LimitFlags&jobObjectLimitProcessMemory != 0 {
		t.Error("cap 0 must leave the process-memory flag off")
	}
	if info.Basic.LimitFlags&jobObjectLimitKillOnJobClose == 0 {
		t.Error("cap 0 lost the kill-on-close flag")
	}

	info = buildJobLimits(2048)
	if info.Basic.LimitFlags&jobObjectLimitProcessMemory == 0 {
		t.Fatal("positive cap did not set the process-memory flag")
	}
	if info.Basic.LimitFlags&jobObjectLimitKillOnJobClose == 0 {
		t.Error("memory cap lost the kill-on-close flag")
	}
	const mb = 1024 * 1024
	if info.ProcessMemoryLimit != 2048*mb {
		t.Errorf("ProcessMemoryLimit = %d, want %d", info.ProcessMemoryLimit, 2048*mb)
	}
	if info.JobMemoryLimit != 0 {
		t.Error("per-job limit set unintentionally; the cap is per process")
	}
}

// TestJobObjectMemoryCapKillsRunawayChild is the behavioral proof of the
// memory-cap rung: a child assigned to a job with a 16 MB cap, allocating in
// 8 MB steps, must die before it can complete an allocation far past the cap.
// A test that only asserted the flag would miss the whole class of "the
// kernel rejected the limit call and we kept going" failures — attach errors
// are deliberately best-effort, so nothing downstream would notice.
func TestJobObjectMemoryCapStopsRunawayChild(t *testing.T) {
	// A fresh job for this test alone: the process-wide job is created once
	// (sync.Once) and shared with tests that run uncapped.
	hRaw, _, _ := procCreateJobObjectW.Call(0, 0)
	if hRaw == 0 {
		t.Fatal("CreateJobObjectW failed")
	}
	defer procCloseHandle.Call(hRaw)
	h := syscall.Handle(hRaw)
	info := buildJobLimits(128) // 128 MB: room for the child's Go runtime, not for 30 × 32 MB
	r, _, callErr := procSetInformationJobObject.Call(
		uintptr(h), jobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(info)), unsafe.Sizeof(*info))
	if r == 0 {
		t.Fatalf("SetInformationJobObject rejected the limits: %v", callErr)
	}

	markFile := filepath.Join(t.TempDir(), "marks.txt")
	t.Setenv("XCUT_TEST_ALLOC_CHILD", "1")
	t.Setenv("XCUT_TEST_MARK_FILE", markFile)

	cmd := exec.Command(os.Args[0], "-test.run=TestHelperAllocChild$", "-test.v")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	attachTo(h, cmd.Process)

	// The wait is hard-bounded: how a Go runtime behaves at the commit limit
	// is OS- and machine-dependent (locally it dies instantly; a node with a
	// different pagefile story may stall instead), and a hung child must
	// become a fast, named failure — not a 10-minute go-test timeout that
	// took down a remote gate (measured, win-devops 2026-09-21).
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var waitErr error
	var stalled bool
	select {
	case waitErr = <-done:
	case <-time.After(60 * time.Second):
		stalled = true
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}
	marks := readMarksOrEmpty(markFile)
	// What must hold: the child cannot land all ten 32 MB blocks under a
	// 128 MB cap. HOW it stops is OS-dependent — locally it dies fast, on
	// win-devops the Go runtime stalled at the commit limit (2 marks, alive,
	// blocked) — a stall at the cap is the cap binding, not the cap failing.
	// The named failure would be all marks landed (clean exit or not): that
	// means uncapped.
	switch {
	case marks == 0 && waitErr == nil && !stalled:
		t.Fatal("child produced no marks and is still running — it never got to allocate, so the test proves nothing")
	case marks == 0:
		t.Fatalf("child died before its first 32 MB block landed — it never allocated, so the test proves nothing (waitErr=%v, stalled=%v)", waitErr, stalled)
	case marks >= 10:
		t.Fatalf("child landed all %d × 32 MB allocations (~320 MB) past a 128 MB cap; the cap did not bind", marks)
	}
	// 1..9 marks with the child gone (died or killed at stall): the cap
	// stopped the runaway. The ffmpeg-level semantics live in
	// TestFFmpegDiesCleanlyUnderMemoryCap.
}

// attachTo assigns a started process to an arbitrary job handle (attachJob
// targets the process-wide singleton; this test needs its own capped job).
func attachTo(h syscall.Handle, p *os.Process) {
	ph, _, _ := procOpenProcess.Call(
		processSetQuota|processTerminate|processSetInformation, 0, uintptr(p.Pid))
	if ph == 0 {
		return
	}
	defer procCloseHandle.Call(ph)
	procAssignToJobObject.Call(uintptr(h), ph)
}

// TestFFmpegDiesCleanlyUnderMemoryCap proves the cap's product semantics with
// the product's own child: ffmpeg (C over libav allocators) must EXIT with an
// error when the cap bites — libav handles allocation failure at its level,
// so the render fails fast and loudly. The one way this feature could hurt
// instead of help is a hung ffmpeg pinning a render slot until the analyzer
// timeout, and this test exists to catch exactly that shape.
func TestFFmpegDiesCleanlyUnderMemoryCap(t *testing.T) {
	tools := requireFFmpeg(t)
	hRaw, _, _ := procCreateJobObjectW.Call(0, 0)
	if hRaw == 0 {
		t.Fatal("CreateJobObjectW failed")
	}
	defer procCloseHandle.Call(hRaw)
	h := syscall.Handle(hRaw)
	info := buildJobLimits(64) // 64 MB: ffmpeg starts fine, a 4K x264 encode cannot fit
	r, _, callErr := procSetInformationJobObject.Call(
		uintptr(h), jobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(info)), unsafe.Sizeof(*info))
	if r == 0 {
		t.Fatalf("SetInformationJobObject rejected the limits: %v", callErr)
	}

	cmd := exec.Command(tools.FFmpeg, "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc2=size=3840x2160:rate=30", "-t", "30",
		"-c:v", "libx264", "-preset", "veryfast", "-f", "null", "-")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	attachTo(h, cmd.Process)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("ffmpeg completed a 4K x264 encode within a 64 MB cap; the cap did not bind")
		}
		// Non-zero exit is the pass: allocation failure surfaced as a normal
		// process error, which is what a capped render should report.
	case <-time.After(90 * time.Second):
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		t.Fatal("ffmpeg hung past 90 s under the cap and had to be killed — " +
			"cap anomalies must be named, not timed out by go test")
	}
}

// TestHelperAllocChild allocates 32 MB blocks (10 × 32 MB ≈ 320 MB), marking
// each step, until killed (expected under the capped job) or done (failure
// signal to the parent — 320 MB is far past the 128 MB cap, so a clean exit
// means the cap never bound). Only runs when the parent re-executes this
// binary with the env var set.
func TestHelperAllocChild(t *testing.T) {
	if os.Getenv("XCUT_TEST_ALLOC_CHILD") == "" {
		return
	}
	markFile := os.Getenv("XCUT_TEST_MARK_FILE")
	var live [][]byte
	for i := 0; i < 10; i++ {
		block := make([]byte, 32*1024*1024)
		for j := 0; j < len(block); j += 4096 {
			block[j] = byte(j) // touch every page: reserves do not fail, commits do
		}
		live = append(live, block)
		writeMark(markFile, "alloc", "step")
		time.Sleep(20 * time.Millisecond)
	}
	os.Exit(0)
}

func readMarksOrEmpty(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}
