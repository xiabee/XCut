package api

import (
	"fmt"
	"testing"
	"time"
)

// TestAuthGateStaysBoundedAtThePeerCap covers the tracker's two ceiling rules,
// neither of which had ever run: at authMaxPeers it either recovers space by
// dropping windows that aged out, or — when everything in there is still live —
// refuses to track the newcomer rather than grow. Both exist because a spray of
// source addresses is hostile input; a map that grows without limit violates the
// "nothing unbounded" rule, and one that fills with stale entries stops
// rate-limiting anyone at all.
func TestAuthGateStaysBoundedAtThePeerCap(t *testing.T) {
	clock := time.Unix(1_700_000_000, 0)
	g := newAuthGate(testToken, nil, nil)
	g.now = func() time.Time { return clock }

	fill := func(base int) {
		t.Helper()
		// Compute the gap once: re-evaluating len(g.failures) in the loop
		// condition makes the target shrink as the map grows, and the fill
		// silently stops at half the cap.
		need := authMaxPeers - len(g.failures)
		for i := 0; i < need; i++ {
			g.noteFailure(fmt.Sprintf("203.0.113.%d.%d:5000", i/256, i%256+base))
		}
	}
	fill(0)
	if len(g.failures) != authMaxPeers {
		t.Fatalf("tracker holds %d peers, want exactly the cap %d", len(g.failures), authMaxPeers)
	}

	// Every window has aged out: the next rejection must reclaim the space.
	clock = clock.Add(authWindow + time.Second)
	fresh := "198.51.100.7:5000"
	g.noteFailure(fresh)
	if _, tracked := g.failures[fresh]; !tracked {
		t.Fatalf("a fresh peer went untracked at the cap while %d expired windows sat in the map", len(g.failures))
	}
	if len(g.failures) > authMaxPeers {
		t.Fatalf("tracker grew past its cap: %d > %d", len(g.failures), authMaxPeers)
	}
	if g.rateLimited(fresh) {
		t.Fatal("one rejection must not rate limit a peer")
	}

	// Full of *live* windows: nothing is reclaimable, so the newcomer goes
	// untracked and the map stops growing. Documented degradation, not a leak —
	// the token check is still the gate.
	fill(1)
	if len(g.failures) != authMaxPeers {
		t.Fatalf("setup: tracker holds %d, want the cap", len(g.failures))
	}
	newcomer := "192.0.2.1:5000"
	g.noteFailure(newcomer)
	if _, tracked := g.failures[newcomer]; tracked {
		t.Fatal("a live-windowed tracker must not displace existing peers")
	}
	if len(g.failures) != authMaxPeers {
		t.Fatalf("tracker grew to %d with nothing to evict, want it held at %d", len(g.failures), authMaxPeers)
	}
}
