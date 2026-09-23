package media

import (
	"fmt"
	"strconv"
	"strings"
)

// memoryMaxAnswer is the decision half of SandboxEnforcement, kept out of the Linux file
// because reading a number and deciding what it means is not platform code — and a
// decision that can only be tested on a host with a session bus is a decision the fast
// gate never checks.
//
// The shapes it has to distinguish, all of them measured or observed: a byte count
// ("134217728" for a 128 MB cap), "infinity" (what Kylin's hybrid hierarchy reports for a
// scope systemd accepted and never attached), and the empty string (a unit the manager
// does not know). applied=false with known=true is the answer that must reach the operator
// as a WARN; known=false is the "this host will not say" case, which is not the same
// statement and must not be reported as one.
func memoryMaxAnswer(value string, wantMB int64) (applied, known bool, detail string) {
	value = strings.TrimSpace(value)
	want := wantMB * 1024 * 1024
	switch {
	case value == "":
		return false, true, "systemd answers nothing for the unit it just started — no limit is attached"
	case value == "infinity":
		return false, true, fmt.Sprintf("systemd reports MemoryMax=infinity on the scope it accepted: the %d MB limit is not attached here", wantMB)
	}
	got, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return false, true, "systemd answered MemoryMax=" + value + ", which is not a byte count"
	}
	if got != want {
		return false, true, fmt.Sprintf("systemd applied %d bytes, not the %d MB (%d bytes) asked for", got, wantMB, want)
	}
	return true, true, fmt.Sprintf("systemd reports MemoryMax=%d bytes on a live scope", got)
}
