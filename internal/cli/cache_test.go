package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedCacheEntries writes n fake (but parseable) analysis cache entries into
// the workspace cache; Store treats content as opaque on read.
func seedCacheEntries(t *testing.T, root string, n int) string {
	t.Helper()
	dir := filepath.Join(root, "cache", "analysis")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		name := filepath.Join(dir, strings.Repeat("a", 64)+"-"+string(rune('0'+i))+".json")
		if err := os.WriteFile(name, []byte(`{"ok":true}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// seedProxy drops one fake proxy file into the workspace proxy cache.
func seedProxy(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, "cache", "proxy")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "deadbeef.mp4"), []byte("fake proxy bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func runCapture(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func testWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XCUT_WORKSPACE", root)
	return root
}

func TestCacheStats(t *testing.T) {
	root := testWorkspace(t)
	dir := seedCacheEntries(t, root, 3)

	code, out, errOut := runCapture(t, "cache", "stats")
	if code != 0 {
		t.Fatalf("cache stats failed: %s", errOut)
	}
	if !strings.Contains(out, "entries:   3") {
		t.Errorf("stats output missing entry count:\n%s", out)
	}
	if !strings.Contains(out, dir) {
		t.Errorf("stats output missing cache dir:\n%s", out)
	}
	if !strings.Contains(out, "proxy dir:") {
		t.Errorf("stats output missing proxy section:\n%s", out)
	}

	code, out, errOut = runCapture(t, "cache", "stats", "--json")
	if code != 0 {
		t.Fatalf("cache stats --json failed: %s", errOut)
	}
	var parsed struct {
		Analysis struct {
			Entries int    `json:"entries"`
			Bytes   int64  `json:"bytes"`
			Dir     string `json:"dir"`
		} `json:"analysis"`
		Proxy struct {
			Entries int    `json:"entries"`
			Dir     string `json:"dir"`
		} `json:"proxy"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("json stats unparseable: %v\n%s", err, out)
	}
	if parsed.Analysis.Entries != 3 || parsed.Analysis.Bytes <= 0 || parsed.Analysis.Dir != dir {
		t.Errorf("json analysis stats wrong: %+v", parsed.Analysis)
	}
	if parsed.Proxy.Entries != 0 || parsed.Proxy.Dir == "" {
		t.Errorf("json proxy stats wrong: %+v", parsed.Proxy)
	}
}

func TestCacheClear(t *testing.T) {
	root := testWorkspace(t)
	dir := seedCacheEntries(t, root, 2)
	proxyDir := seedProxy(t, root)

	// Dry run reports but keeps.
	code, out, errOut := runCapture(t, "cache", "clear", "--dry-run")
	if code != 0 {
		t.Fatalf("clear --dry-run failed: %s", errOut)
	}
	if !strings.Contains(out, "would remove 2 analysis entries") || !strings.Contains(out, "1 proxies") {
		t.Errorf("dry-run output unexpected:\n%s", out)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 2 {
		t.Fatalf("dry run must keep entries, found %d", len(entries))
	}
	if entries, _ := os.ReadDir(proxyDir); len(entries) != 1 {
		t.Fatalf("dry run must keep proxies, found %d", len(entries))
	}

	// Real clear removes every entry; other workspace state stays.
	code, out, errOut = runCapture(t, "cache", "clear")
	if code != 0 {
		t.Fatalf("clear failed: %s", errOut)
	}
	if !strings.Contains(out, "removed 2 analysis entries") || !strings.Contains(out, "1 proxies") {
		t.Errorf("clear output unexpected:\n%s", out)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("clear left %d analysis entries", len(entries))
	}
	if entries, _ := os.ReadDir(proxyDir); len(entries) != 0 {
		t.Fatalf("clear left %d proxies", len(entries))
	}
	if _, err := os.Stat(filepath.Join(root, "projects")); err != nil {
		t.Errorf("projects dir must survive cache clear: %v", err)
	}

	// Clear on an empty cache is a clean no-op.
	code, out, _ = runCapture(t, "cache", "clear")
	if code != 0 || !strings.Contains(out, "already empty") {
		t.Errorf("empty clear: code=%d out=%q", code, out)
	}
}

func TestCacheUsageErrors(t *testing.T) {
	testWorkspace(t)
	if code, _, _ := runCapture(t, "cache"); code != 1 {
		t.Errorf("bare cache must fail validation, got %d", code)
	}
	if code, _, _ := runCapture(t, "cache", "bogus"); code != 1 {
		t.Errorf("unknown subcommand must fail validation, got %d", code)
	}
	if code, _, _ := runCapture(t, "cache", "stats", "--nope"); code != 1 {
		t.Errorf("unknown stats flag must fail validation, got %d", code)
	}
	if code, _, _ := runCapture(t, "cache", "-h"); code != 0 {
		t.Errorf("cache -h must print help, got %d", code)
	}
}
