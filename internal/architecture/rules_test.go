package architecture

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// These two guards exist because AGENTS.md rules 2 and 5 say "never", and a "never" that
// lives only in prose decays: the next plausible line — one test spawning `cmd /c` to get a
// long-running child, one analyzer composing a filter graph to build a fixture — is
// individually reasonable and collectively the end of the rule. Both were real: the first
// existed in this tree until `6c0949b`, the second until the crossfade fixture moved to
// internal/testmedia in this commit.

// —— classifiers ————————————————————————————————————————————————————————————————

type codeLine struct {
	num  int
	text string
}

// codeLines drops comment-only content, so prose *about* a rule cannot violate it. A
// block comment inside a line is not tracked: the classifiers look for exec calls, which do
// not live inside /* */ in this tree, and a false pass there is a stated limitation.
func codeLines(src string) []codeLine {
	out := make([]codeLine, 0, 32)
	for i, raw := range strings.Split(src, "\n") {
		text := raw
		if c := strings.Index(text, "//"); c >= 0 {
			text = text[:c]
		}
		text = strings.TrimRight(text, " \t")
		if strings.TrimSpace(text) == "" {
			continue
		}
		out = append(out, codeLine{num: i + 1, text: text})
	}
	return out
}

// shellNames are the interpreters a product process may not hand a command string to. The
// list is by basename, so /usr/bin/bash and cmd.exe are both covered.
var shellNames = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true,
	"cmd": true, "cmd.exe": true,
	"powershell": true, "powershell.exe": true,
	"pwsh": true, "pwsh.exe": true,
}

// commandStringShapes are sequences that mean something only to a shell. They are matched
// inside an exec call's *string literals* — not on the line, because `; ` is also Go's
// statement separator and `Run(); err != nil` is not a command string. Redirection is left
// out on purpose: it only works through a shell, which the rule above already refuses, and
// FFmpeg filter graphs in internal/render use `|` and `;` legitimately.
var commandStringShapes = []string{"&&", "||"}

// literalsIn returns the contents of every double-quoted literal on a line. Enough for this
// tree, where no source line carries an escaped quote inside an exec argument.
func literalsIn(line string) []string {
	var out []string
	for i := 0; i < len(line); i++ {
		if line[i] != '"' {
			continue
		}
		j := strings.IndexByte(line[i+1:], '"')
		if j < 0 {
			return out
		}
		out = append(out, line[i+1:i+1+j])
		i += j
	}
	return out
}

// shellFindings reports rule 2 violations in one file's code lines.
func shellFindings(lines []codeLine) []string {
	var bad []string
	for _, l := range lines {
		if !strings.Contains(l.text, "exec.Command") {
			continue
		}
		if lit, ok := firstQuoted(l.text); ok && shellNames[strings.ToLower(filepath.Base(lit))] {
			bad = append(bad, fmt.Sprintf("line %d: exec of the shell %q", l.num, lit))
			continue
		}
		for _, lit := range literalsIn(l.text) {
			for _, shape := range commandStringShapes {
				if strings.Contains(lit, shape) {
					bad = append(bad, fmt.Sprintf(
						"line %d: %q inside an exec argument is a command string, not argv", l.num, shape))
					break
				}
			}
		}
	}
	return bad
}

// firstQuoted returns the contents of the first double-quoted literal on the line — which
// for exec.Command/CommandContext is the binary when it is written as a literal, and absent
// when it comes from a variable.
func firstQuoted(s string) (string, bool) {
	i := strings.Index(s, `"`)
	if i < 0 {
		return "", false
	}
	rest := s[i+1:]
	j := strings.Index(rest, `"`)
	if j < 0 {
		return "", false
	}
	return rest[:j], true
}

// renderImport and filterLiteral are rule 5's checkable half: the packages that *detect*
// edits must not import the renderer nor compose its command line. They may read media
// (analysis streams decoded frames through media.StreamStdout), which is why the guard looks
// for the render layer and its filter graph, not for FFmpeg use.
var (
	renderImport   = `"github.com/xiabee/XCut/internal/render"`
	filterLiterals = []string{`"-filter_complex"`, "xfade=", "overlay="}
)

func renderFindings(lines []codeLine) []string {
	var bad []string
	for _, l := range lines {
		if strings.Contains(l.text, renderImport) {
			bad = append(bad, fmt.Sprintf("line %d: imports the render layer", l.num))
			continue
		}
		for _, lit := range filterLiterals {
			if strings.Contains(l.text, lit) {
				bad = append(bad, fmt.Sprintf("line %d: composes a render filter graph (%s)", l.num, lit))
				break
			}
		}
	}
	return bad
}

// —— tree walk ——————————————————————————————————————————————————————————————————

func repoRoot(t *testing.T) string {
	t.Helper()
	_, this, _, ok := runtime.Caller(0)
	if !ok {
		t.Skip("cannot locate the repo root from the test file")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(this)))
}

// goFiles returns every .go file under the given repo-relative directories, keyed by the
// same relative path so a finding names what a reader can open.
func goFiles(t *testing.T, dirs ...string) map[string]string {
	t.Helper()
	root := repoRoot(t)
	out := map[string]string{}
	for _, dir := range dirs {
		base := filepath.Join(root, filepath.FromSlash(dir))
		err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(p, ".go") {
				return nil
			}
			data, rerr := os.ReadFile(p)
			if rerr != nil {
				return rerr
			}
			rel, aerr := filepath.Rel(root, p)
			if aerr != nil {
				return aerr
			}
			out[filepath.ToSlash(rel)] = string(data)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	return out
}

// —— the guards over the real tree ——————————————————————————————————————————————

const selfFixtureFile = "internal/architecture/rules_test.go"

func TestNoShellIsEverExecuted(t *testing.T) {
	files := goFiles(t, "internal", "cmd")
	if len(files) < 150 {
		t.Fatalf("scanned %d Go files; the walk is not reading the tree it claims", len(files))
	}
	sites := 0
	var findings []string
	for name, src := range files {
		if name == selfFixtureFile {
			// This guard's own tables carry deliberate violations so the classifier can be
			// shown to fire; scanning them would be the guard reporting its own fixtures.
			// The cost is that a real violation could hide in that one file, which is why
			// TestShellClassifierFiresWhatItMust exists: if a fixture stops firing, that
			// test fails, so the exclusion cannot quietly become an escape hatch.
			continue
		}
		lines := codeLines(src)
		for _, l := range lines {
			if strings.Contains(l.text, "exec.Command") {
				sites++
			}
		}
		for _, f := range shellFindings(lines) {
			findings = append(findings, name+": "+f)
		}
	}
	if sites < 20 {
		t.Fatalf("saw %d exec call sites across %d files — the classifier is reading nothing",
			sites, len(files))
	}
	// The count is the evidence the scan ran on real material, not just that no finding
	// came back: an empty tree and a clean tree would otherwise look identical.
	t.Logf("scanned %d Go files, %d exec call sites", len(files), sites)
	if len(findings) > 0 {
		t.Fatalf("AGENTS.md rule 2 (no shell, ever) is violated in %d place(s):\n  %s",
			len(findings), strings.Join(findings, "\n  "))
	}
}

func TestAnalyzersNeverComposeRenderCommands(t *testing.T) {
	dirs := []string{"internal/analysis", "internal/event", "internal/style"}
	files := goFiles(t, dirs...)
	if len(files) < 30 {
		t.Fatalf("scanned %d Go files across %s; the walk is not reading the analyzer layer",
			len(files), strings.Join(dirs, ", "))
	}
	var findings []string
	for name, src := range files {
		for _, f := range renderFindings(codeLines(src)) {
			findings = append(findings, name+": "+f)
		}
	}
	t.Logf("scanned %d files in %s", len(files), strings.Join(dirs, ", "))
	if len(findings) > 0 {
		t.Fatalf("AGENTS.md rule 5 (timeline before FFmpeg) is violated in %d place(s):\n  %s",
			len(findings), strings.Join(findings, "\n  "))
	}
}

// —— the classifiers' own teeth —————————————————————————————————————————————————
//
// A guard that has never fired on anything is indistinguishable from a guard that cannot
// fire. These tables are the difference.

func TestShellClassifierFiresWhatItMust(t *testing.T) {
	mustFire := []struct{ name, src, want string }{
		{"shell by bare name", `	cmd := exec.Command("cmd", "/c", "ping -n 5")`, "exec of the shell"},
		{"shell by path", `	exec.CommandContext(ctx, "/usr/bin/bash", "-c", script)`, "exec of the shell"},
		{"powershell by extension", `	cmd := exec.Command("powershell.exe", "-Command", s)`, "exec of the shell"},
		{"command string in argv", `	cmd := exec.Command("ffmpeg", "-i", in+"&& echo done")`, "command string"},
	}
	for _, tc := range mustFire {
		got := shellFindings(codeLines(tc.src))
		if len(got) == 0 {
			t.Errorf("%s: the classifier matched nothing on %q", tc.name, tc.src)
			continue
		}
		if !strings.Contains(got[0], tc.want) {
			t.Errorf("%s: got %q, want it to name %q", tc.name, got[0], tc.want)
		}
	}

	mustNotFire := []struct{ name, src string }{
		{"binary from a variable", `	cmd := exec.CommandContext(ctx, bin, args...)`},
		{"ffmpeg argv", `	out, err := exec.Command("ffmpeg", "-i", in, path).Output()`},
		{"user text as data", `	cmd := exec.Command("ffmpeg", "-i", name)`},
		{"prose about the rule", `// never hand cmd /c a command string here`},
		{"a filter graph with operators, in the render layer", `	args := []string{"-vf", "drawtext=text='a|b'"}`},
	}
	for _, tc := range mustNotFire {
		if got := shellFindings(codeLines(tc.src)); len(got) > 0 {
			t.Errorf("%s: false positive on %q: %q", tc.name, tc.src, got[0])
		}
	}
}

func TestRenderClassifierFiresWhatItMust(t *testing.T) {
	if got := renderFindings(codeLines(`import "github.com/xiabee/XCut/internal/render"`)); len(got) == 0 ||
		!strings.Contains(got[0], "imports the render layer") {
		t.Errorf("the render import was not flagged: %v", got)
	}
	if got := renderFindings(codeLines(`		"-filter_complex", "[0:v][1:v]xfade=transition=fade[v]",`)); len(got) == 0 {
		t.Error("a composed filter graph was not flagged")
	}
	for _, src := range []string{
		`	preset := "generic_xfade" // the style's name, not a command line`,
		`	err := media.StreamStdout(ctx, opts.Tools.FFmpeg, sink, args...)`,
		`// the renderer builds the filter graph from the timeline`,
	} {
		if got := renderFindings(codeLines(src)); len(got) > 0 {
			t.Errorf("false positive on %q: %q", src, got[0])
		}
	}
}
