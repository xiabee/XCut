package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestUsageDocsMirrorTheBinary: docs/USAGE.md is where a person reads the flags from,
// and until now nothing in the build noticed when the binary grew one — adding --subs
// to `auto` left the doc stating the old syntax, in the same commit that wrote the
// test proving the new one works. The check reads the registry itself rather than a
// hand-kept list, so a command cannot ship with its page behind it.
func TestUsageDocsMirrorTheBinary(t *testing.T) {
	if len(commands) < 10 {
		t.Fatalf("the registry holds %d commands; init() did not run, so this check is empty", len(commands))
	}
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Skip("cannot locate the repo root")
	}
	doc := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(thisFile))), "docs", "USAGE.md")
	data, err := os.ReadFile(doc)
	if err != nil {
		t.Fatalf("read %s: %v", doc, err)
	}
	flat := spaces(string(data))

	var missing []string
	for _, c := range commands {
		if !strings.Contains(flat, spaces(c.usage)) {
			missing = append(missing, c.name+": usage line\n    "+c.usage)
		}
		if !strings.Contains(flat, spaces(c.summary)) {
			missing = append(missing, c.name+": summary line\n    "+c.summary)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("docs/USAGE.md is behind the binary for %d command(s):\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
}

// spaces collapses every run of whitespace, including the line wraps a markdown block
// uses, so the comparison is about the text and not about where the page broke a line.
func spaces(s string) string { return strings.Join(strings.Fields(s), " ") }
