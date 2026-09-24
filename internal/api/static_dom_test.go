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

// TestSaveRejectionQuotesTheServer: the API distinguishes two 409s (the saved
// document moved under this window / the document sent carries no revision), and the
// distinction dies the moment the UI replaces it with a sentence of its own. The walk
// over the editing surface found the banner asserting "the timeline changed elsewhere"
// for every rejection — a cause the UI cannot check. Pin the failure path to the
// server's words.
func TestSaveRejectionQuotesTheServer(t *testing.T) {
	js := staticFile(t, "static/app.js")
	end := strings.Index(js, "timelineDoc.revision = body.revision;")
	if end < 0 {
		t.Fatal("saveTimeline's success path no longer records the returned revision — find the rejection block above it")
	}
	start := strings.LastIndex(js[:end], "if (!resp.ok) {")
	if start < 0 {
		t.Fatal("no `if (!resp.ok) {` above the success path — the rejection block moved out of saveTimeline")
	}
	// Without this the check would silently fall back to some other function's
	// rejection block if saveTimeline ever lost its own.
	if fn := strings.Index(js, "async function saveTimeline("); fn < 0 || start < fn {
		t.Fatal("the rejection block being checked is not inside saveTimeline")
	}
	path := js[start:end]
	for _, want := range []string{"banner(", "resp.status === 409"} {
		if !strings.Contains(path, want) {
			t.Errorf("the rejection path never reaches %s on screen:\n%s", want, path)
		}
	}
	// The 409 arm's own key literal. Checking `{msg}` anywhere in the block would
	// pass with the conflict text dropped from that arm alone, since the other arm
	// already interpolates it.
	arm := regexp.MustCompile(`(?s)resp\.status === 409\s*\n\s*\?\s*tf\("([^"]*)"`).FindStringSubmatch(path)
	if arm == nil {
		t.Fatalf("the 409 arm is no longer a tf() call with a literal key — read the block above and update this check:\n%s", path)
	}
	if !strings.Contains(arm[1], "{msg}") {
		t.Errorf("the 409 banner writes its own sentence instead of the server's: %s", arm[1])
	}
	if strings.Contains(path, "changed elsewhere") {
		t.Errorf("the UI is back to naming a cause the server did not report:\n%s", path)
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
// TestTheStripPreviewsTheSaveLayout pins the one-rule contract between the
// editing strip and the save. The strip's blocks are positioned by
// timeline_start, and nothing recompiled that claim: a drag reorder used to
// flip the array while every block kept its stale position, and because
// totalDuration read the new last row's stale timeline_start, the ruler
// collapsed to that clip alone and blew every width up fivefold — the strip
// destroyed by the very gesture that edits it. Every mutation path must run
// the same relayoutClips the save writes, and the save must not keep a
// private copy of the layout arithmetic for them to drift apart again.
func TestTheStripPreviewsTheSaveLayout(t *testing.T) {
	js := staticFile(t, "static/app.js")
	if n := strings.Count(js, "function relayoutClips("); n != 1 {
		t.Fatalf("relayoutClips is defined %d times, want exactly one", n)
	}
	// drop reorder, trim-handle release, inspector Apply, remove/undo remove.
	if n := strings.Count(js, "relayoutClips(clipEdits)"); n != 4 {
		t.Errorf("relayoutClips(clipEdits) is called %d times, want 4 (drop, trim release, Apply, remove) — an editing path that skips it previews a layout the save will not write", n)
	}
	if n := strings.Count(js, "relayoutClips(kept)"); n != 1 {
		t.Errorf("saveTimeline must derive the document's layout from relayoutClips(kept), found %d call(s)", n)
	}
	// The fits tolerance must live only in xfadeOverlap: a second copy of the
	// comparison is how the strip and the save learned to disagree before.
	if n := strings.Count(js, "playDur(prev) + 1e-9"); n != 1 {
		t.Errorf("the xfade fits-comparison appears %d times, want 1 (inside xfadeOverlap only)", n)
	}
	// totalDuration must be order-independent: the last row of the working
	// copy is wherever the last drag left it, not the reel's end.
	start := strings.Index(js, "function totalDuration(")
	if start < 0 {
		t.Fatal("totalDuration vanished — the ruler and the trim maths both read it")
	}
	body := js[start:]
	if end := strings.Index(body, "\n}"); end >= 0 {
		body = body[:end]
	}
	if strings.Contains(body, "[list.length - 1]") {
		t.Errorf("totalDuration reads the last array row again — a drag reorder collapses the ruler to that row's stale timeline_start:\n%s", body)
	}
}

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

// TestInspectorInputsStayInsideTheirLabels pins the pairing between a field's
// text and its control in the runtime-built inspector: field() hangs every
// input inside a label, and the xfade-duration input used to be re-parented
// bare onto the inspector box — its label text stayed behind in the grid as
// decoration, so clicking it focused nothing and the input had no accessible
// name for the screen-reader path.
func TestInspectorInputsStayInsideTheirLabels(t *testing.T) {
	js := staticFile(t, "static/app.js")
	if strings.Contains(js, "box.appendChild(tdur)") && !strings.Contains(js, "box.appendChild(tdur.parentElement)") {
		t.Error("the xfade duration input is appended bare to the inspector box — its label is stranded in the grid; append tdur.parentElement instead")
	}
	if !strings.Contains(js, "tdur.parentElement") {
		t.Error("the xfade duration input no longer travels inside its label — inspector fields must reach the DOM with their text attached")
	}
}

// TestThemeTokensCoverBothSchemes: the light block overrides by token NAME,
// so a token added to the dark :root block but missed in [data-theme="light"]
// silently keeps its dark value on a light background — usually unreadable.
// The two blocks must define the same set of custom properties.
func TestThemeTokensCoverBothSchemes(t *testing.T) {
	css := staticFile(t, "static/style.css")
	tokenRe := regexp.MustCompile(`--([a-z0-9-]+):`)
	blockRe := regexp.MustCompile(`(?s)(:root|:root\[data-theme="dark"\]|\[data-theme="light"\]) \{(.*?)\}`)
	dark := map[string]bool{}
	light := map[string]bool{}
	found := 0
	for _, m := range blockRe.FindAllStringSubmatch(css, -1) {
		names := map[string]bool{}
		for _, tk := range tokenRe.FindAllStringSubmatch(m[2], -1) {
			names[tk[1]] = true
		}
		switch m[1] {
		case ":root":
			dark = names
			found++
		case `[data-theme="light"]`:
			light = names
			found++
		}
	}
	if found != 2 || len(dark) == 0 {
		t.Fatalf("theme blocks not found (found=%d, dark tokens=%d) — the extractor or the stylesheet rotted", found, len(dark))
	}
	var missing []string
	for name := range dark {
		if !light[name] {
			missing = append(missing, name)
		}
	}
	for name := range light {
		if !dark[name] {
			missing = append(missing, "+"+name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("the theme token sets drifted (%d token(s)): %s — a token only one scheme defines renders unreadable in the other", len(missing), strings.Join(missing, ", "))
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
