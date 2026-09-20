//go:build windows

package media

import (
	"os/exec"
	"testing"
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
