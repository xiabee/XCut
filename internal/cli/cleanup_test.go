package cli

import (
	"os"
	"strings"
	"testing"
)

// TestCleanupDryRunKeepsCache: `cleanup --dry-run` used to run the real
// eviction while printing "would evict" — a preview quietly deleted the
// analysis cache and proxies. The dry run must only plan.
func TestCleanupDryRunKeepsCache(t *testing.T) {
	root := testWorkspace(t)
	cacheDir := seedCacheEntries(t, root, 2)
	proxyDir := seedProxy(t, root)

	code, out, errOut := runCapture(t, "cleanup", "--dry-run")
	if code != 0 {
		t.Fatalf("cleanup --dry-run failed: %s", errOut)
	}
	if !strings.Contains(out, "would evict") {
		t.Errorf("dry-run output missing eviction plan:\n%s", out)
	}
	if entries, _ := os.ReadDir(cacheDir); len(entries) != 2 {
		t.Fatalf("dry run must keep analysis entries, found %d", len(entries))
	}
	if entries, _ := os.ReadDir(proxyDir); len(entries) != 1 {
		t.Fatalf("dry run must keep proxies, found %d", len(entries))
	}

	// The real run evicts to the configured budget (default budgets keep
	// these small entries; the command must still succeed and report).
	code, out, errOut = runCapture(t, "cleanup")
	if code != 0 {
		t.Fatalf("cleanup failed: %s", errOut)
	}
	if !strings.Contains(out, "cache: 2 entries") || !strings.Contains(out, "proxies: 1 entries") {
		t.Errorf("cleanup output unexpected:\n%s", out)
	}
	if entries, _ := os.ReadDir(cacheDir); len(entries) != 2 {
		t.Fatalf("default budget must keep the seeded entries, found %d", len(entries))
	}
}
