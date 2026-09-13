package api

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// The UI i18n layer is a plain dictionary (static/i18n.js) keyed by the
// English source string. Nothing compiles it, so drift is silent: an HTML
// data-i18n attribute or a JS t()/tf() key with no dictionary entry quietly
// renders as English in the Chinese UI, and a renamed key leaves a dead
// dictionary entry behind. These tests are the gate — they parse the
// embedded assets the way the browser would and refuse either direction.

var (
	// data-i18n, data-i18n-title, data-i18n-placeholder in index.html.
	i18nHTMLKeyRe = regexp.MustCompile(`data-i18n(?:-title|-placeholder)?="([^"]+)"`)
	// t("key") and tf("key", ...) in app.js — the key literal ends at a
	// quote followed by ")" or "," (tf's params object).
	i18nJSKeyRe = regexp.MustCompile(`\bt(?:f)?\("([^"]+)"[,)]`)
)

func i18nAssets(t *testing.T) (html, js, dictJS string) {
	t.Helper()
	for name, dst := range map[string]*string{
		"static/index.html": &html,
		"static/app.js":     &js,
		"static/i18n.js":    &dictJS,
	} {
		b, err := staticFS.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		*dst = string(b)
	}
	return html, js, dictJS
}

func i18nDict(t *testing.T, dictJS string) map[string]string {
	t.Helper()
	// Anchor on the assignment, not the first "{": the header comment may
	// itself contain braces (e.g. "{name}").
	anchor := strings.Index(dictJS, "XCUT_I18N")
	if anchor < 0 {
		t.Fatal("i18n.js: cannot find the XCUT_I18N assignment")
	}
	start := strings.Index(dictJS[anchor:], "{")
	if start < 0 {
		t.Fatal("i18n.js: cannot locate the JSON object")
	}
	start += anchor
	end := strings.LastIndex(dictJS, "}")
	if end <= start {
		t.Fatal("i18n.js: cannot locate the JSON object")
	}
	var parsed map[string]map[string]string
	if err := json.Unmarshal([]byte(dictJS[start:end+1]), &parsed); err != nil {
		t.Fatalf("i18n.js is not valid JSON (keep it parseable for this gate): %v", err)
	}
	zh, ok := parsed["zh"]
	if !ok {
		t.Fatal("i18n.js: missing the zh table")
	}
	return zh
}

func TestI18nKeysCovered(t *testing.T) {
	html, js, dictJS := i18nAssets(t)
	zh := i18nDict(t, dictJS)

	seen := map[string]string{} // key → first referencing file
	for _, m := range i18nHTMLKeyRe.FindAllStringSubmatch(html, -1) {
		seen[m[1]] = "index.html"
	}
	for _, m := range i18nJSKeyRe.FindAllStringSubmatch(js, -1) {
		if _, dup := seen[m[1]]; !dup {
			seen[m[1]] = "app.js"
		}
	}
	if len(seen) == 0 {
		t.Fatal("no i18n keys found in index.html/app.js — the extraction regexes rotted")
	}

	var missing []string
	for key, where := range seen {
		if v, ok := zh[key]; !ok || strings.TrimSpace(v) == "" {
			missing = append(missing, where+": "+key)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("i18n keys with no zh translation:\n  %s", strings.Join(missing, "\n  "))
	}
}

func TestI18nDictKeysAreReferenced(t *testing.T) {
	html, js, dictJS := i18nAssets(t)
	zh := i18nDict(t, dictJS)

	referenced := func(key string) bool {
		// The key must appear as a string literal in the UI sources —
		// either a data-i18n attribute value (HTML) or a t()/tf() argument
		// (app.js, single- or double-quoted). Matching the literal, not the
		// call prefix, stays correct for both t("k") and tf("k", {...}).
		dq := `"` + key + `"`
		sq := `'` + key + `'`
		return strings.Contains(html, dq) || strings.Contains(js, dq) ||
			strings.Contains(js, sq)
	}

	var orphans []string
	for key := range zh {
		if !referenced(key) {
			orphans = append(orphans, key)
		}
	}
	if len(orphans) > 0 {
		t.Fatalf("zh entries never referenced from index.html/app.js:\n  %s",
			strings.Join(orphans, "\n  "))
	}
}
