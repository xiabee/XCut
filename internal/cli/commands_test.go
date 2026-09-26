package cli

import "testing"

// TestCommandRegistryHasNoDuplicates pins the registration table's uniqueness:
// render was registered twice — the GPU session added the --encoder usage in a
// second init and left the stale one standing — and lookup returns the first
// match, so `xcut render -h` kept advertising a syntax without the flag the
// command actually takes. A duplicate name is silent until the help lies; this
// fails the build instead.
func TestCommandRegistryHasNoDuplicates(t *testing.T) {
	seen := make(map[string]int, len(commands))
	for _, c := range commands {
		if prev, dup := seen[c.name]; dup {
			t.Errorf("command %q registered twice (first at index %d) — every name must appear once in the help", c.name, prev)
		}
		seen[c.name]++
	}
}
