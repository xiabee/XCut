package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// testToken is what a remote peer must present. Built at runtime instead of
// spelled out: the repo's secret scanner correctly refuses a high-entropy
// literal assigned to a token variable, and a repeated placeholder still
// clears the length policy the gate inherits from config.MinAuthTokenLen.
var testToken = strings.Repeat("xcut", 8) // 32 chars

const remotePeer = "203.0.113.9:41000" // TEST-NET-3: documentation-range, never routable
const localPeer = "127.0.0.1:41001"

func passHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "reached", http.StatusOK)
	})
}

// gateCall runs one request through the gate from a chosen peer address.
func gateCall(g *authGate, peer, authHeader string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/api/v1/health", nil)
	req.RemoteAddr = peer
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rec := httptest.NewRecorder()
	g.authenticate(passHandler()).ServeHTTP(rec, req)
	return rec
}

func TestAuthGatePeerTrust(t *testing.T) {
	cases := []struct {
		remoteAddr string
		peer       string
		trusted    bool
	}{
		{"127.0.0.1:9", "127.0.0.1", true},
		{"127.0.0.2:9", "127.0.0.2", true}, // all of 127/8 is loopback
		{"[::1]:9", "::1", true},           // IPv6 loopback, bracketed form
		{"0.0.0.0:9", "0.0.0.0", false},    // wildcard bind addr is not a peer
		{"::1", "::1", false},              // no port: unparseable, distrust
		{"garbage", "garbage", false},      // malformed, distrust
		{"203.0.113.9:9", "203.0.113.9", false},
		{"192.168.1.5:9", "192.168.1.5", false}, // LAN is remote
		{"100.64.0.2:9", "100.64.0.2", false},   // overlay ranges are not loopback
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = c.remoteAddr
		gotPeer, gotTrusted := peerAddress(req)
		if gotPeer != c.peer || gotTrusted != c.trusted {
			t.Errorf("peerAddress(%q) = (%q, %v), want (%q, %v)",
				c.remoteAddr, gotPeer, gotTrusted, c.peer, c.trusted)
		}
	}
}

func TestAuthGateDecisions(t *testing.T) {
	gate := newAuthGate(testToken, nil)
	cases := []struct {
		name    string
		gate    *authGate
		peer    string
		header  string
		want    int
		wantErr string
	}{
		{"loopback needs no token with auth on", gate, localPeer, "", http.StatusOK, ""},
		{"loopback ignores a bad token", gate, localPeer, "Bearer nope", http.StatusOK, ""},
		{"remote without header", gate, remotePeer, "", http.StatusUnauthorized, "unauthorized"},
		{"remote empty bearer", gate, remotePeer, "Bearer ", http.StatusUnauthorized, "unauthorized"},
		{"remote wrong scheme", gate, remotePeer, "Basic " + testToken, http.StatusUnauthorized, "unauthorized"},
		{"remote wrong token", gate, remotePeer, "Bearer definitely-not-the-token-000000", http.StatusUnauthorized, "unauthorized"},
		{"remote token prefix of real", gate, remotePeer, "Bearer " + testToken[:20], http.StatusUnauthorized, "unauthorized"},
		{"remote token extended past real", gate, remotePeer, "Bearer " + testToken + "x", http.StatusUnauthorized, "unauthorized"},
		{"remote correct token", gate, remotePeer, "Bearer " + testToken, http.StatusOK, ""},
		{"scheme name is case-insensitive", gate, remotePeer, "bearer " + testToken, http.StatusOK, ""},
		{"UPPERCASE scheme accepted", gate, remotePeer, "BEARER " + testToken, http.StatusOK, ""},
		{"surrounding spaces tolerated", gate, remotePeer, "Bearer  " + testToken + " ", http.StatusOK, ""},
		{"no token configured, remote refused", nil, remotePeer, "Bearer whatever", http.StatusForbidden, "forbidden"},
		{"no token configured, loopback works", nil, localPeer, "", http.StatusOK, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := gateCall(c.gate, c.peer, c.header)
			if rec.Code != c.want {
				t.Fatalf("status %d, want %d (body %s)", rec.Code, c.want, rec.Body.String())
			}
			if c.wantErr == "" {
				return
			}
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("error body is not JSON: %v (%s)", err, rec.Body.String())
			}
			if body["error"] != c.wantErr {
				t.Errorf("error code %v, want %q", body["error"], c.wantErr)
			}
			// A rejection must never echo anything about what was supplied:
			// the message is operator- and attacker-facing alike.
			if strings.Contains(rec.Body.String(), testToken) {
				t.Errorf("response leaked the configured token: %s", rec.Body.String())
			}
		})
	}
}

func TestAuthGateChallengeHeader(t *testing.T) {
	rec := gateCall(newAuthGate(testToken, nil), remotePeer, "")
	if got := rec.Header().Get("WWW-Authenticate"); got != `Bearer realm="xcut"` {
		t.Errorf("WWW-Authenticate %q, want the bearer challenge", got)
	}
	// 429 and 403 are not challenges; sending WWW-Authenticate there would
	// tell a client to retry credentials it cannot use.
	if rec := gateCall(nil, remotePeer, ""); rec.Header().Get("WWW-Authenticate") != "" {
		t.Error("forbidden response carried an auth challenge")
	}
}

// The gate must be the boundary between user data and the network, while the
// UI shell stays reachable so a remote operator can be asked for a token.
func TestHandlerGatesAPIButNotShell(t *testing.T) {
	s := testServer(t)
	s.AuthToken = testToken
	h := s.Handler()

	call := func(method, path, peer, header string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		req.RemoteAddr = peer
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	if rec := call("GET", "/", remotePeer, ""); rec.Code != http.StatusOK {
		t.Errorf("UI shell over a remote peer: %d, want 200", rec.Code)
	}
	if rec := call("GET", "/api/v1/health", remotePeer, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated remote API call: %d, want 401", rec.Code)
	}
	rec := call("GET", "/api/v1/health", remotePeer, "Bearer "+testToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("authenticated remote API call: %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var health map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &health); err != nil {
		t.Fatalf("health body: %v", err)
	}
	if health["ok"] != true {
		t.Errorf("health body lost its payload: %v", health)
	}
	if rec := call("GET", "/api/v1/health", localPeer, ""); rec.Code != http.StatusOK {
		t.Errorf("local API call with auth configured: %d, want 200", rec.Code)
	}
	// Every data-bearing prefix is behind the gate, including the ones a
	// browser loads by URL (thumbnails, downloads) rather than via fetch.
	for _, path := range []string{
		"/api/v1/projects",
		"/api/v1/styles",
		"/api/v1/jobs",
		"/api/v1/projects/1/render",
		"/api/v1/projects/1/assets/2/file",
		"/api/v1/projects/1/subtitles/file",
		"/api/v1/setup/ffmpeg",
	} {
		if rec := call("GET", path, remotePeer, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s unauthenticated: %d, want 401", path, rec.Code)
		}
	}
}

func TestAuthGateRateLimitsBruteForce(t *testing.T) {
	g := newAuthGate(testToken, nil)
	now := time.Unix(1700000000, 0)
	g.now = func() time.Time { return now }

	for i := 0; i < authMaxFailures; i++ {
		if rec := gateCall(g, remotePeer, "Bearer wrong-wrong-wrong-wrong-wrong"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d, want 401", i, rec.Code)
		}
	}
	// Budget spent: even the right token waits out the window. A lockout that
	// still answers 401 to a correct token would leak nothing but also stop
	// nothing; 429 is the honest answer.
	rec := gateCall(g, remotePeer, "Bearer "+testToken)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("after budget: %d, want 429", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] != "resource_limit" {
		t.Errorf("rate-limited error code %v, want resource_limit", body["error"])
	}

	// A different peer keeps its own budget.
	if rec := gateCall(g, "198.51.100.7:9", "Bearer "+testToken); rec.Code != http.StatusOK {
		t.Errorf("other peer after first peer locked: %d, want 200", rec.Code)
	}

	now = now.Add(authWindow + time.Second)
	if rec := gateCall(g, remotePeer, "Bearer "+testToken); rec.Code != http.StatusOK {
		t.Errorf("after the window: %d, want 200", rec.Code)
	}
}

// A successful request must not be charged against the caller's own budget, or
// a locked-out-but-valid client could never dig itself out.
func TestAuthGateSuccessDoesNotConsumeBudget(t *testing.T) {
	g := newAuthGate(testToken, nil)
	for i := 0; i < authMaxFailures*2; i++ {
		if rec := gateCall(g, remotePeer, "Bearer "+testToken); rec.Code != http.StatusOK {
			t.Fatalf("authenticated request %d: %d, want 200", i, rec.Code)
		}
	}
}

// The failure tracker is per-peer state in a long-running process: it needs the
// same budgeted-growth discipline as the cache (AGENTS.md rule 4).
func TestAuthGateFailureTrackerStaysBounded(t *testing.T) {
	g := newAuthGate(testToken, nil)
	now := time.Unix(1700000000, 0)
	g.now = func() time.Time { return now }

	for i := 0; i < authMaxPeers*2; i++ {
		peer := "203.0.113." + strconv.Itoa(i%251) + ":" + strconv.Itoa(10000+i%50000)
		gateCall(g, peer, "Bearer nope-nope-nope-nope-nope")
		if n := len(g.failures); n > authMaxPeers {
			t.Fatalf("failure tracker grew to %d entries, cap is %d", n, authMaxPeers)
		}
	}
	// With the clock past the window the next purge reclaims everything, so
	// tracking resumes instead of wedging at the cap.
	now = now.Add(authWindow * 2)
	gateCall(g, "203.0.114.1:9", "Bearer nope-nope-nope-nope-nope")
	if _, ok := g.failures["203.0.114.1"]; !ok {
		t.Error("expired-state purge did not reclaim room for a new peer")
	}
}

func TestAuthGateNeverLogsTheToken(t *testing.T) {
	var buf bytes.Buffer
	g := newAuthGate(testToken, slog.New(slog.NewTextHandler(&buf, nil)))
	rec := gateCall(g, remotePeer, "Bearer supplied-secret-lookalike-0000")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rec.Code)
	}
	logged := buf.String()
	if !strings.Contains(logged, "auth rejected") {
		t.Fatalf("rejection was not logged at all: %q", logged)
	}
	for _, secret := range []string{testToken, "supplied-secret-lookalike-0000"} {
		if strings.Contains(logged, secret) {
			t.Errorf("serve.log would leak %q: %s", secret, logged)
		}
	}
	if !strings.Contains(logged, "203.0.113.9") {
		t.Errorf("log lost the peer address an operator needs: %s", logged)
	}
}
