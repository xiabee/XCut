package api

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The light theme's text tokens were contrast-audited in a browser against
// WCAG AA (4.5:1 for normal text) before shipping — and the audit is cheap
// enough to be a gate: these are pure token maths on the stylesheet, so a
// future palette tweak that quietly drops below AA fails here instead of on
// a user's screen in a bright room. Audited pairs: the accent, error and
// warning hues against the panel and the background; body fg against the
// background; dim text against the background. The faint hint token is
// deliberately decorative (3.06/3.36 as shipped) and is not gated.

func wcagLuminance(hex string) float64 {
	h := strings.TrimPrefix(hex, "#")
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	ch := func(i int) float64 {
		v, err := strconv.ParseInt(h[i*2:i*2+2], 16, 64)
		if err != nil {
			return 0
		}
		f := float64(v) / 255
		if f <= 0.03928 {
			return f / 12.92
		}
		return math.Pow((f+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(0) + 0.7152*ch(1) + 0.0722*ch(2)
}

func wcagRatio(a, b string) float64 {
	la, lb := wcagLuminance(a), wcagLuminance(b)
	hi, lo := math.Max(la, lb), math.Min(la, lb)
	return (hi + 0.05) / (lo + 0.05)
}

func themeBlockTokens(t *testing.T, css, header string) map[string]string {
	t.Helper()
	re := regexp.MustCompile(`(?s)` + regexp.QuoteMeta(header) + ` \{(.*?)\}`)
	m := re.FindStringSubmatch(css)
	if m == nil {
		t.Fatalf("stylesheet block %q not found — the extractor rotted", header)
	}
	tokRe := regexp.MustCompile(`--([a-z0-9-]+): *(#[0-9a-fA-F]{3,6})`)
	out := map[string]string{}
	for _, tk := range tokRe.FindAllStringSubmatch(m[1], -1) {
		out[tk[1]] = tk[2]
	}
	return out
}

func TestLightThemeTextTokensMeetAAContrast(t *testing.T) {
	css := staticFile(t, "static/style.css")
	light := themeBlockTokens(t, css, `[data-theme="light"]`)
	bg, panel := light["bg"], light["panel"]
	if bg == "" || panel == "" {
		t.Fatal("light theme must define --bg and --panel — every ratio is measured against them")
	}
	for _, name := range []string{"fg", "dim", "acc", "err", "warn"} {
		if light[name] == "" {
			t.Fatalf("light theme lost --%s — the token set drifted", name)
		}
		if r := wcagRatio(light[name], bg); r < 4.5 {
			t.Errorf("--%s %s on --bg: %.2f:1, want ≥ 4.5 (WCAG AA normal text)", name, light[name], r)
		}
		if r := wcagRatio(light[name], panel); r < 4.5 {
			t.Errorf("--%s %s on --panel: %.2f:1, want ≥ 4.5 (WCAG AA normal text)", name, light[name], r)
		}
	}
}
