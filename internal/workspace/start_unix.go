//go:build unix

package workspace

import (
	"os"
	"strconv"
	"strings"
)

// currentProcessStart returns this process's start stamp as an opaque string
// (/proc/<pid>/stat field 22 — CPU jiffies since boot). Only equality between
// an acquired stamp and a later query is meaningful; the format is never
// parsed as time.
func currentProcessStart() string {
	s, _ := processStartTime(os.Getpid())
	return s
}

// processStartTime returns the start stamp of the process with the given PID
// (same opaque format). ok=false when the process does not exist, the platform
// has no /proc, or the stamp cannot be read (caller falls back to
// conservative behavior).
func processStartTime(pid int) (string, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", false
	}
	// Field 2 (comm) may contain spaces and parens: fields resume after the
	// LAST ')'. Field 22 (starttime) is index 19 after it (field 3 = index 0).
	s := string(b)
	i := strings.LastIndex(s, ")")
	if i < 0 || i+2 > len(s) {
		return "", false
	}
	fields := strings.Fields(s[i+2:])
	if len(fields) < 20 {
		return "", false
	}
	return fields[19], true
}
