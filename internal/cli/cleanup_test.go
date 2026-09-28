package cli

import (
	"os"
	"path/filepath"
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

// TestCleanupReclaimsCacheDebris: the .tmp-* scratch a dead process left
// under cache/ is never an eviction victim, so cleanup is its only remover
// on the CLI path. Running the real command also exercises the writer-lock
// gate the sweep's safety argument stands on — Run acquires it for cleanup.
func TestCleanupReclaimsCacheDebris(t *testing.T) {
	root := testWorkspace(t)
	proxyDir := seedProxy(t, root)
	debris := filepath.Join(proxyDir, ".tmp-orphan.mp4")
	if err := os.WriteFile(debris, []byte("half-written proxy"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out, errOut := runCapture(t, "cleanup", "--dry-run")
	if code != 0 {
		t.Fatalf("cleanup --dry-run failed: %s", errOut)
	}
	if !strings.Contains(out, "cache debris: would remove 1 entries") {
		t.Errorf("dry-run output missing the debris plan:\n%s", out)
	}
	if _, err := os.Stat(debris); err != nil {
		t.Fatal("dry run must keep the debris")
	}

	code, out, errOut = runCapture(t, "cleanup")
	if code != 0 {
		t.Fatalf("cleanup failed: %s", errOut)
	}
	if !strings.Contains(out, "cache debris: removed 1 entries") {
		t.Errorf("cleanup output missing the debris reclaim:\n%s", out)
	}
	if _, err := os.Stat(debris); !os.IsNotExist(err) {
		t.Fatal("the debris must be removed")
	}
	proxy := filepath.Join(proxyDir, "deadbeef.mp4")
	if _, err := os.Stat(proxy); err != nil {
		t.Fatalf("the finalized proxy must survive: %v", err)
	}
}
