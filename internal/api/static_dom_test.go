package api

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The UI is one HTML file plus script that reaches into it by id. Nothing
// compiles that coupling, so a renamed or deleted element shows up as a
// silently missing control rather than an error — session #13 shipped a
// timeline handle whose pointer target was never drawn, and the empty-state
// editor rendered a panel no code could reach. These tests resolve every id
// the script asks for against the markup that exists, in both files.

var (
	jsDollarIDRe = regexp.MustCompile(`\$\("([a-zA-Z0-9_-]+)"\)`)
	jsQueryIDRe  = regexp.MustCompile(`querySelector(?:All)?\("#([a-zA-Z0-9_-]+)`)
	anyIDRe      = regexp.MustCompile(`id="([a-zA-Z0-9_-]+)"`)
	labelForRe   = regexp.MustCompile(`for="([a-zA-Z0-9_-]+)"`)
	ariaByRe     = regexp.MustCompile(`aria-labelledby="([a-zA-Z0-9_-]+)"`)
	// A class the script switches on: classList.add("x") / remove / toggle.
	jsToggleClassRe = regexp.MustCompile(`classList\.(?:add|remove|toggle)\("([a-zA-Z0-9_-]+)"\)`)
	// Any class selector in the stylesheet, including inside a group
	// (.a, .b {}) and pseudo/state compounds (.open:hover).
	cssClassRe = regexp.MustCompile(`\.([a-zA-Z0-9_-]+)`)
)

// staticFile reads one embedded UI asset.
func staticFile(t *testing.T, name string) string {
	t.Helper()
	b, err := staticFS.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

func definedIDs(t *testing.T) map[string]bool {
	t.Helper()
	ids := map[string]bool{}
	// Elements may also be built by the script itself, so both files count.
	for _, src := range []string{staticFile(t, "static/index.html"), staticFile(t, "static/app.js")} {
		for _, m := range anyIDRe.FindAllStringSubmatch(src, -1) {
			ids[m[1]] = true
		}
	}
	return ids
}

// TestDollarIsGetElementById pins the assumption every other check here makes:
// that $("x") means "the element whose id is x".
func TestDollarIsGetElementById(t *testing.T) {
	js := staticFile(t, "static/app.js")
	if !strings.Contains(js, `const $ = (id) => document.getElementById(id);`) {
		t.Fatal(`$("id") is no longer plain getElementById; update the id-resolution tests to match its real meaning`)
	}
}

func TestEveryIDTheScriptReachesForExists(t *testing.T) {
	js := staticFile(t, "static/app.js")
	defined := definedIDs(t)
	var missing []string
	for _, re := range []*regexp.Regexp{jsDollarIDRe, jsQueryIDRe} {
		for _, m := range re.FindAllStringSubmatch(js, -1) {
			if !defined[m[1]] {
				missing = append(missing, m[1])
			}
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("app.js reaches for %d id(s) no markup defines: %s",
			len(missing), strings.Join(dedupe(missing), ", "))
	}
}

// TestLabelAndAriaTargetsExist covers the two attributes that name another
// element: a wrong value there costs the keyboard and screen-reader path,
// which is exactly the path a remote operator uses when the layout is tight.
func TestLabelAndAriaTargetsExist(t *testing.T) {
	html := staticFile(t, "static/index.html")
	defined := definedIDs(t)
	for _, re := range []*regexp.Regexp{labelForRe, ariaByRe} {
		for _, m := range re.FindAllStringSubmatch(html, -1) {
			if !defined[m[1]] {
				t.Errorf("attribute target %q names no element in index.html", m[1])
			}
		}
	}
}

// TestToggledClassesAreStyled closes the other half of the same coupling: the
// script shows and hides things by flipping a class, and nothing compiles the
// claim that the stylesheet answers. It used to flip a class the stylesheet had
// no rule for and, on top of that, left a [hidden] attribute nobody cleared, so
// the project picker's list could never appear and an already-created project
// was unreachable after a reload — while the empty state still read "select or
// create a project". A class no selector reads is not a style miss, it is a
// control that does nothing.
func TestToggledClassesAreStyled(t *testing.T) {
	js := staticFile(t, "static/app.js")
	css := staticFile(t, "static/style.css")

	styled := map[string]bool{}
	for _, m := range cssClassRe.FindAllStringSubmatch(css, -1) {
		styled[m[1]] = true
	}
	if len(styled) == 0 {
		t.Fatal("no class selectors found in style.css — the extractor rotted")
	}

	flipped := map[string]bool{}
	for _, m := range jsToggleClassRe.FindAllStringSubmatch(js, -1) {
		flipped[m[1]] = true
	}
	if len(flipped) == 0 {
		t.Fatal(`no classList.add/remove/toggle("x") calls found in app.js — the extractor rotted`)
	}

	var unstyled []string
	for name := range flipped {
		if !styled[name] {
			unstyled = append(unstyled, name)
		}
	}
	if len(unstyled) > 0 {
		sort.Strings(unstyled)
		t.Errorf("app.js flips %d class(es) no stylesheet rule reads: %s",
			len(unstyled), strings.Join(unstyled, ", "))
	}
}

// TestAriaLabelMatchesItsI18nKey checks the pair written in the markup: an
// element translated through data-i18n-aria must also carry a static
// aria-label, so the field is named even before the script runs.
func TestAriaLabelMatchesItsI18nKey(t *testing.T) {
	html := staticFile(t, "static/index.html")
	for _, tag := range strings.Split(html, "\n") {
		const marker = `data-i18n-aria="`
		i := strings.Index(tag, marker)
		if i < 0 {
			continue
		}
		key := tag[i+len(marker):]
		key = key[:strings.Index(key, `"`)]
		if !strings.Contains(tag, `aria-label="`+key+`"`) {
			t.Errorf("data-i18n-aria=%q has no matching static aria-label in the same tag", key)
		}
	}
}

// TestSignInOutElementsExist pins the remote-access panel specifically: it is
// the only UI a user sees before the session exists, and it is rendered by
// toggling `hidden` on a markup node rather than by the script.
func TestSignInOutElementsExist(t *testing.T) {
	html := staticFile(t, "static/index.html")
	js := staticFile(t, "static/app.js")
	for _, id := range []string{"login-modal", "login-token", "login-error", "login-submit", "sign-out"} {
		if !strings.Contains(html, `id="`+id+`"`) {
			t.Errorf("sign-in flow needs #%s in index.html", id)
		}
		if !strings.Contains(js, `$("`+id+`")`) {
			t.Errorf("index.html has #%s but app.js never resolves it", id)
		}
	}
}

// Every form that carries a submit button must be wired to a submit listener.
// A form without one performs a native submission — the page navigates away,
// UI state is lost, and nothing the user asked for happens. That is not
// hypothetical: the workspace redesign deleted the import-form handler while
// keeping the form, and the path-import button silently reloaded the page for
// three sessions until a real browser drive hit it. Existence of an id (the
// check above) cannot see an unwired form; this can.
func TestEverySubmitFormHasAHandler(t *testing.T) {
	html := staticFile(t, "static/index.html")
	js := staticFile(t, "static/app.js")

	formIDRe := regexp.MustCompile(`<form[^>]*id="([a-zA-Z0-9_-]+)"`)
	for _, m := range formIDRe.FindAllStringSubmatch(html, -1) {
		id := m[1]
		wired := strings.Contains(js, `$("`+id+`").addEventListener("submit"`)
		if !wired {
			t.Errorf("form #%s has a submit button in index.html but app.js never listens for its submit event: clicking it would navigate the page away instead of doing the work", id)
		}
	}
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
