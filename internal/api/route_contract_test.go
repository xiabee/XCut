package api

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// The UI↔API contract, held where no browser has to be open to enforce it.
// B6-style screen verification is an owner-window activity; this test is what
// the gate can hold every night instead: every route app.js can name must be
// served by the mux with the method the UI actually uses, because a route
// renamed or removed on the Go side breaks the panel silently — the fetch
// fails, the banner says "network error", and nothing in the Go tree goes
// red. Removing only the GET half of a route family breaks the panel exactly
// as hard as removing the whole family, so the assertion is per-method.
//
// The probe is deliberately side-effect-free: a GET with fake ids only (every
// handler answers a missing resource with JSON), and PATCH for everything
// else — the mux rejects PATCH before any handler runs, and its Allow header
// names the methods that could serve the path. The catch-all "GET /" (static
// UI) matches every path's method slot, which is why a GET answer must be
// JSON to count (the static file server answers plain text), and why Allow
// must name a real API method (POST/PUT/DELETE) rather than GET/HEAD. No
// probe ever creates a project, starts a job, or reaches the FFmpeg setup
// installer.

// routeLitRe finds path text after a quote that opens a JS string: the
// backtick templates and plain strings app.js builds its fetches from.
// Comments are skipped on purpose — only quoted text is a contract.
var routeLitRe = regexp.MustCompile("[`\"'](/api/v1/[A-Za-z0-9_${}()\\-./?=&,]*)")

// jsTemplateRe matches one ${...} hole (single JS expression, no nested
// braces in this codebase) so it can collapse to a wildcard segment.
var jsTemplateRe = regexp.MustCompile(`\$\{[^}]*\}`)

// methodOptRe reads the `method: "X"` option inside a fetch/api option
// object that follows a route literal.
var methodOptRe = regexp.MustCompile(`method:\s*"([A-Z]+)"`)

// xhrOpenRe reads the method argument of an XMLHttpRequest .open("X", url)
// call — the upload progress path fetch() cannot express. The quote that
// opens the URL literal ends the prefix window.
var xhrOpenRe = regexp.MustCompile(`open\(\s*["']([A-Z]+)["']\s*,\s*["'\x60]$`)

// triggerTailRe extracts the path argument of trigger(...) call sites —
// the helper that POSTs to /api/v1/projects/{id}<tail>. The wrapper's own
// literal normalizes to /api/v1/projects/{}; the tails are the contract.
// Real call sites are two-argument (`trigger("/analyze", {...})`), so the
// regex must not pin a ")" against the closing quote — the single-argument
// spelling is what made this extraction silently empty once already.
var triggerTailRe = regexp.MustCompile(`trigger\(\s*['"](/[A-Za-z0-9_\-]+)['"]`)

// extraProbes cover the templates static extraction cannot resolve: the
// asset-route helper builds .../assets/{id}/{name} where {name} comes from
// its callers and a ternary, and the photo seed appends "/photo?..." to the
// player-spot path — a fragment that does not start with /api/v1, so the
// literal extractor can never see it. These are probed method-agnostically
// (the literal that GETs each of them carries its own method assertion). The
// guards in the contract test keep this table honest — if the template shape
// changes, the test fails and asks for the table to be revisited rather than
// silently probing shapes the UI no longer builds.
var extraProbes = []string{
	"/api/v1/projects/{}/assets/{}/roi",
	"/api/v1/projects/{}/assets/{}/score",
	"/api/v1/projects/{}/assets/{}/file",
	"/api/v1/projects/{}/assets/{}/player-spot",
	"/api/v1/projects/{}/assets/{}/player-spot/photo",
}

// uiCalls maps each normalized path to the methods the UI exercises it with.
// methodAt reads the JS immediately around a literal: post( means POST,
// and an options object after the literal may override the api()/fetch()
// GET default with an explicit method.
func uiCalls(appjs string) map[string]map[string]bool {
	calls := map[string]map[string]bool{}
	norm := func(p string) (string, bool) {
		p = strings.SplitN(jsTemplateRe.ReplaceAllString(p, "{}"), "?", 2)[0]
		// "id" + injected dynamic tail: this literal is the trigger()
		// wrapper, a prefix rather than a request — the tails carry the
		// real calls, so the wrapper itself asserts nothing (a collapsed
		// POST here would invent a POST /api/v1/projects/{id} contract
		// the server never had).
		if strings.Contains(p, "{}{}") {
			return "", false
		}
		p = strings.ReplaceAll(p, "{}{}", "{}")
		// An unterminated ${ means the hole held a ternary (spaces ended the
		// capture): the template's concrete outputs are the extraProbes
		// table's job, pinned by the guard strings in the contract test.
		if p == "" || strings.Contains(p, "${") {
			return "", false
		}
		return p, true
	}
	note := func(p, method string) {
		if p == "" {
			return
		}
		if calls[p] == nil {
			calls[p] = map[string]bool{}
		}
		calls[p][method] = true
	}
	for _, loc := range routeLitRe.FindAllStringSubmatchIndex(appjs, -1) {
		p, ok := norm(appjs[loc[2]:loc[3]])
		method := "GET"
		// The call's opening decides the verb: `post(` is the POST helper,
		// `xhr.open("POST", ` is the upload's XHR, an options object after
		// the literal may override the api()/fetch() GET default. loc[0] is
		// the quote that opens the literal, so the XHR check needs it in the
		// window and the post( check needs the window without it.
		withQuote := appjs[max(0, loc[0]-30) : loc[0]+1]
		if m := xhrOpenRe.FindStringSubmatch(withQuote); m != nil {
			method = m[1]
		} else if strings.HasSuffix(withQuote[:len(withQuote)-1], "post(") {
			method = "POST"
		} else if m := methodOptRe.FindStringSubmatch(appjs[loc[1]:min(len(appjs), loc[1]+120)]); m != nil {
			method = m[1]
		}
		if ok {
			note(p, method)
		}
	}
	for _, m := range triggerTailRe.FindAllStringSubmatch(appjs, -1) {
		p, _ := norm("/api/v1/projects/{}" + m[1])
		note(p, "POST")
	}
	return calls
}

func TestRouteContractEveryPathTheUIFetchesIsRegistered(t *testing.T) {
	src, err := staticFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatalf("read embedded app.js: %v", err)
	}
	appjs := string(src)
	// Guards for the dynamic shapes extraProbes exist for. Each names the
	// app.js text it watches; edits to those lines must revisit the table.
	for _, guard := range []string{"currentProject.id}${path}", "assets/${assetValue}/", `+ "/photo?filename=" +`} {
		if !strings.Contains(appjs, guard) {
			t.Fatalf("app.js no longer contains %q — the dynamic-route table in this test watches a shape that moved; revisit extraProbes", guard)
		}
	}

	calls := uiCalls(appjs)
	// The floor exists so a regex regression that silently extracts nothing
	// fails here instead of asserting on an empty set. 25 distinct routes
	// today; legitimate UI churn moves this by a few, not by twenty.
	if len(calls) < 20 {
		t.Fatalf("extracted %d routes from app.js, want >= 20 — the extractor likely regressed and would assert on nothing", len(calls))
	}

	s := testServer(t)
	h := s.Handler()
	for path, methods := range calls {
		for method := range methods {
			if routeServes(h, path, method) {
				continue
			}
			t.Errorf("app.js can %s %s but no registered route serves it — the panel breaks silently at this call", method, path)
		}
	}
	for _, p := range extraProbes {
		if routeClaimable(h, p) {
			continue
		}
		t.Errorf("app.js's asset helper can build %s but no registered route serves it", p)
	}
}

// routeServes answers whether the mux serves path with exactly the method
// the UI uses, without running any mutating handler: a GET must be answered
// with JSON by a real handler (the static fallback's plain text means no API
// route claims GET here); any other method is probed with PATCH, which the
// mux refuses with Allow naming the methods that could serve the path.
func routeServes(h http.Handler, path, method string) bool {
	if method == http.MethodGet {
		return answersJSON(h, path)
	}
	return allowHas(h, path, method)
}

// routeClaimable is the method-agnostic form for the extraProbes table.
func routeClaimable(h http.Handler, path string) bool {
	return answersJSON(h, path) ||
		allowHas(h, path, http.MethodPost) ||
		allowHas(h, path, http.MethodPut) ||
		allowHas(h, path, http.MethodDelete)
}

func answersJSON(h http.Handler, path string) bool {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = "127.0.0.1:52000" // the local client, per the gate's trust rule
	h.ServeHTTP(rec, req)
	return strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json")
}

func allowHas(h http.Handler, path, method string) bool {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, path, nil)
	req.RemoteAddr = "127.0.0.1:52000"
	h.ServeHTTP(rec, req)
	return strings.Contains(rec.Header().Get("Allow"), method)
}

func TestExtractUICallsNormalization(t *testing.T) {
	// Call-site shapes copied from app.js, two-argument triggers included:
	// the one-argument spelling here is what let the tail regex rot silently
	// on its first draft — this snippet is the regression test for that.
	appjs := "fetch(`/api/v1/projects/${id}/timeline?rev=3`);" +
		"post(`/api/v1/projects/${currentProject.id}${path}`, body);" +
		"trigger(\"/analyze\", {}); trigger('/timeline', req());" +
		"api(`/api/v1/projects/${pid}`, { method: \"DELETE\" });" +
		"// prose mentions /api/v1/not/a/string and is skipped\n" +
		"await api(`/api/v1/styles/${encodeURIComponent(name())}/roi`);"
	calls := uiCalls(appjs)
	want := map[string]map[string]bool{
		"/api/v1/projects/{}/timeline": {"GET": true, "POST": true}, // template+query strip; trigger tail POST
		"/api/v1/projects/{}/analyze":  {"POST": true},
		"/api/v1/projects/{}":          {"DELETE": true}, // explicit method option; wrapper literal asserts nothing
		"/api/v1/styles/{}/roi":        {"GET": true},    // function call inside the hole
	}
	for path, methods := range want {
		if calls[path] == nil {
			t.Errorf("uiCalls missed %s entirely (got %v)", path, calls)
			continue
		}
		for m := range methods {
			if !calls[path][m] {
				t.Errorf("uiCalls[%s] missing method %s (got %v)", path, m, calls[path])
			}
		}
	}
	if len(calls) != len(want) {
		t.Errorf("uiCalls extracted %d paths, want exactly %d: %v", len(calls), len(want), calls)
	}
	if got := calls["/api/v1/not/a/string"]; got != nil {
		t.Errorf("comment prose leaked into the route set: %v", got)
	}
}
