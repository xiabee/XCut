package api

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// docs/SECURITY.md puts the whole remote-session posture (D12, D14) behind one
// habit: the UI renders server data with textContent and never as HTML, because a
// same-origin XSS would read the workspace through the visitor's own authenticated
// session. Until this file that habit was carried by convention alone — nothing in
// the build noticed, and a single future `el.innerHTML = "<b>" + project.name`
// would have shipped an XSS that no test could see, on the one bind where the token
// and the cookie actually grant something.
//
// So the sinks are scanned, the one form the codebase really uses is allowed (an
// empty-string assignment, which clears a node and interprets nothing), and the
// scanner is run against injected violations: a regex that quietly stopped matching
// anything would otherwise report a clean UI forever.

var (
	htmlSinkRe = regexp.MustCompile(`\.\s*(?:innerHTML|outerHTML)\s*=|\.insertAdjacentHTML\s*\(|\bdocument\s*\.\s*(?:write|writeln)\s*\(`)
	// An empty-string clear of a node: "anything.innerHTML = """ after whitespace
	// is folded. This is the only markup-sink form the UI is allowed to contain.
	htmlClearRe = regexp.MustCompile(`^.*\.\s*(?:innerHTML|outerHTML)\s*=\s*(?:""|'')$`)
	// Code built from strings: every form here interprets its argument. The timer
	// arms match only the string form; `setTimeout(fn, ms)` is what the app uses.
	// (The backtick is spliced in because it cannot sit inside a raw literal.)
	dynamicCodeRe = regexp.MustCompile(`\beval\s*\(|\bnew\s+Function\s*\(|\b(?:setTimeout|setInterval)\s*\(\s*(?:"|'|` + "`" + `)`)
)

// scriptStatement is one `;`-terminated piece of a line, with whitespace folded.
// Whole-line comments are skipped; a trailing comment on a code line is scanned
// with the code, because the miss (a violation hidden by a mid-line cut at a
// "http://" literal) is worse than the false positive, which is loud.
type scriptStatement struct {
	file string
	line int
	text string
}

func scanScript(src, file string) []scriptStatement {
	var out []scriptStatement
	for i, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") ||
			strings.HasPrefix(trimmed, "/*") {
			continue
		}
		for _, stmt := range strings.Split(line, ";") {
			stmt = strings.Join(strings.Fields(stmt), " ")
			if stmt == "" {
				continue
			}
			out = append(out, scriptStatement{file: file, line: i + 1, text: stmt})
		}
	}
	return out
}

func report(t *testing.T, kind string, hits []scriptStatement) {
	t.Helper()
	var b strings.Builder
	for _, h := range hits {
		b.WriteString("\n  ")
		b.WriteString(h.file)
		b.WriteString(":")
		b.WriteString(fmt.Sprint(h.line))
		b.WriteString(": ")
		b.WriteString(h.text)
	}
	t.Errorf("%s in the shipped UI script%s", kind, b.String())
}

// classifyMarkup splits the same way the guards do, so the control test below
// exercises the pipeline (line folding, `;` splitting, comment skipping) and not
// just the two regexes — the first version of the control fed the regexes a whole
// line with its terminator and disagreed with the guard about what a clear is.
func classifyMarkup(src, file string) (sinks, clears []scriptStatement) {
	for _, s := range scanScript(src, file) {
		if !htmlSinkRe.MatchString(s.text) {
			continue
		}
		if htmlClearRe.MatchString(s.text) {
			clears = append(clears, s)
			continue
		}
		sinks = append(sinks, s)
	}
	return sinks, clears
}

func classifyDynamic(src, file string) (hits []scriptStatement) {
	for _, s := range scanScript(src, file) {
		if dynamicCodeRe.MatchString(s.text) {
			hits = append(hits, s)
		}
	}
	return hits
}

func TestUINeverWritesMarkupFromAString(t *testing.T) {
	var sinks, clears []scriptStatement
	for _, name := range []string{"static/app.js", "static/i18n.js"} {
		s, c := classifyMarkup(staticFile(t, name), name)
		sinks, clears = append(sinks, s...), append(clears, c...)
	}
	// The floor: the scanner demonstrably saw the sink family in the real file, so
	// an empty list below means "no violation", not "no match". Without this the
	// guard could rot into a regex that finds nothing and still pass.
	if len(clears) < 10 {
		t.Fatalf("the scan found %d empty-string clears in the UI scripts; expected the dozen the code carries — the sink regex has stopped matching what it is looking for", len(clears))
	}
	if len(sinks) > 0 {
		report(t, "markup sinks beyond an empty-string clear", sinks)
		t.Log("server data must reach the DOM through textContent, see docs/SECURITY.md")
	}
	// And the convention itself must still be the majority path: textContent is what
	// the app writes with, hundreds of times over.
	js := staticFile(t, "static/app.js")
	if n := strings.Count(js, ".textContent"); n < 50 {
		t.Errorf("the UI script uses .textContent %d times; the rendering rule this guard protects is no longer the norm", n)
	}
}

func TestUINeverBuildsCodeFromStrings(t *testing.T) {
	var hits []scriptStatement
	for _, name := range []string{"static/app.js", "static/i18n.js"} {
		hits = append(hits, classifyDynamic(staticFile(t, name), name)...)
	}
	if len(hits) > 0 {
		report(t, "dynamic code execution (eval / Function / string timer)", hits)
	}
}

// TestTheScannersNoticeWhatTheyGuard: the refusal table, run through the same
// classifier the two guards use. A scanner that reports a clean UI while failing
// this is not guarding anything, and that is the failure mode this file exists to
// make impossible.
func TestTheScannersNoticeWhatTheyGuard(t *testing.T) {
	markup := `
menu.innerHTML = "";
sel.innerHTML = '';
box.innerHTML = "<b>" + name;
list.insertAdjacentHTML("beforeend", html);
document.write(untrusted);
status.textContent = ""; links.innerHTML = markup;
// a comment that says el.innerHTML = html is not code
`
	sinks, clears := classifyMarkup(markup, "fixture")
	if len(clears) != 2 {
		t.Errorf("accepted %d clears, want the two empty-string ones: %v", len(clears), texts(clears))
	}
	wantSinks := []string{`box.innerHTML = "<b>" + name`, `list.insertAdjacentHTML("beforeend", html)`,
		`document.write(untrusted)`, `links.innerHTML = markup`}
	if len(sinks) != len(wantSinks) {
		t.Fatalf("found %d sinks, want %d: %v", len(sinks), len(wantSinks), texts(sinks))
	}
	for i, want := range wantSinks {
		if sinks[i].text != want {
			t.Errorf("sink %d = %q, want %q", i, sinks[i].text, want)
		}
	}

	dyn := `
eval(payload);
const f = new Function("return " + s);
setTimeout("tick()", 50);
setInterval(` + "`" + `poll()` + "`" + `, 1000);
setTimeout(() => refresh(), 500);
setInterval(poll, 1000);
`
	hits := classifyDynamic(dyn, "fixture")
	if len(hits) != 4 {
		t.Fatalf("dynamic-code scanner reported %d hits, want the four string-form ones: %v", len(hits), texts(hits))
	}
}

func texts(hits []scriptStatement) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.text)
	}
	return out
}
