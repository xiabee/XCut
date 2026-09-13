//go:build windows

package media

import (
	"errors"
	"os"
	"sync"
	"syscall"
	"unsafe"
)

// jobObject is the OS-level backstop for child-process cleanup: every
// ffmpeg/ffprobe is assigned to one job created with KILL_ON_JOB_CLOSE, so
// if the parent dies without running its context-kill path (task-manager
// kill -9, power loss of the parent only), the kernel reaps the whole
// child tree instead of leaving orphan encoders burning CPU for the rest
// of a render. Phase-4 "sandbox options for FFmpeg" (ROADMAP), first rung.
var (
	jobOnce    sync.Once
	jobHandle  syscall.Handle
	jobInitErr error
)

// jobObjectBasicLimitInformation mirrors JOBOBJECT_BASIC_LIMIT_INFORMATION
// exactly - x64 alignment matters (DWORD fields get uint32 padding before
// the following pointer-size members).
type jobObjectBasicLimitInformation struct {
	PerProcessUserLimitTime uint64
	PerJobUserLimitTime     uint64
	LimitFlags              uint32
	_                       uint32
	MinWorkingSetSize       uintptr
	MaxWorkingSetSize       uintptr
	ActiveProcessLimit      uint32
	_                       uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

// ioCounters mirrors IO_COUNTERS.
type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

// jobObjectExtendedLimit mirrors JOBOBJECT_EXTENDED_LIMIT_INFORMATION.
type jobObjectExtendedLimit struct {
	Basic                 jobObjectBasicLimitInformation
	IoInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

const (
	jobObjectExtendedLimitInformation = 9
	jobObjectLimitKillOnJobClose      = 0x2000
)

func ensureJob() (syscall.Handle, error) {
	jobOnce.Do(func() {
		h, _, _ := procCreateJobObjectW.Call(0, 0)
		if h == 0 {
			jobInitErr = errors.New("CreateJobObjectW failed")
			return
		}
		info := jobObjectExtendedLimit{}
		info.Basic.LimitFlags = jobObjectLimitKillOnJobClose
		r, _, _ := procSetInformationJobObject.Call(
			h, jobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info))
		// LazyProc's error is noise on success; the boolean return (r) is
		// the real verdict.
		if r == 0 {
			jobHandle = 0
			jobInitErr = errors.New("SetInformationJobObject(KILL_ON_JOB_CLOSE) failed")
			return
		}
		jobHandle = syscall.Handle(h)
	})
	return jobHandle, jobInitErr
}

// attachJob assigns a started process to the kill-on-close job. Best
// effort: any failure degrades silently to the existing context-kill
// cleanup, which remains the primary mechanism.
func attachJob(p *os.Process) {
	if p == nil {
		return
	}
	h, err := ensureJob()
	if err != nil || h == 0 {
		return
	}
	// Open by pid: os.Process hides its handle (and WithHandle needs
	// go1.26 while go.mod targets 1.25). AssignProcessToJobObject wants
	// SET_QUOTA | TERMINATE | SET_INFORMATION on the target.
	ph, _, _ := procOpenProcess.Call(
		processSetQuota|processTerminate|processSetInformation, 0, uintptr(p.Pid))
	if ph == 0 {
		return
	}
	defer procCloseHandle.Call(ph)
	procAssignToJobObject.Call(uintptr(h), ph)
}

var (
	procCreateJobObjectW        = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject = kernel32.NewProc("SetInformationJobObject")
	procAssignToJobObject       = kernel32.NewProc("AssignProcessToJobObject")
	procOpenProcess             = kernel32.NewProc("OpenProcess")
	procCloseHandle             = kernel32.NewProc("CloseHandle")
	kernel32                    = syscall.NewLazyDLL("kernel32.dll")

	processTerminate      = uintptr(0x0001)
	processSetQuota       = uintptr(0x0100)
	processSetInformation = uintptr(0x0800)
)
