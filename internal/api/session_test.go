package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// hexID builds a syntactically valid session id for fixtures.
func hexID(seed byte) string {
	return strings.Repeat(fmt.Sprintf("%02x", seed), 32)
}

func TestSessionStoreLifecycle(t *testing.T) {
	st := newSessionStore()
	now := time.Unix(1700000000, 0)
	st.now = func() time.Time { return now }

	id, ok := st.issue()
	if !ok || !isWellFormedSessionID(id) {
		t.Fatalf("issued id is not a session id: %q ok=%v", id, ok)
	}
	if !st.valid(id) {
		t.Fatal("a fresh session is not valid")
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: id})
	if !st.authorize(req) {
		t.Error("GET with a live cookie must authorize")
	}

	now = now.Add(sessionTTL + time.Minute)
	if st.valid(id) {
		t.Fatal("session outlived its TTL")
	}
	if st.authorize(req) {
		t.Error("expired session still authorized a GET")
	}
	// An expired entry is reclaimed by the next issue rather than accumulating.
	if _, ok := st.issue(); !ok {
		t.Fatal("issue refused with only one expired entry present")
	}
	if st.count() != 1 {
		t.Fatalf("expired entries were not pruned: %d live", st.count())
	}

	st.revoke(id)
	if st.valid(id) {
		t.Fatal("revoked session still valid")
	}
}

// The table is a growth axis in a long-running process, so the cap has to hold
// under pressure and refuse rather than expand.
func TestSessionStoreCapIsHard(t *testing.T) {
	st := newSessionStore()
	for i := 0; i < maxSessions; i++ {
		if _, ok := st.issue(); !ok {
			t.Fatalf("refused issue %d before the cap", i)
		}
	}
	if st.count() != maxSessions {
		t.Fatalf("store holds %d, want %d", st.count(), maxSessions)
	}
	if _, ok := st.issue(); ok {
		t.Fatal("issue accepted past the cap")
	}
	if st.count() != maxSessions {
		t.Fatalf("the refusal grew the table anyway: %d", st.count())
	}
}

// TestSessionAuthorizeMethodAsymmetry pins the property the whole design rests
// on: a cookie is enough to *read*, never enough to *change*.
func TestSessionAuthorizeMethodAsymmetry(t *testing.T) {
	st := newSessionStore()
	id, _ := st.issue()
	other := hexID(0xcd)

	req := func(method, cookie, header string) *http.Request {
		r := httptest.NewRequest(method, "/api/v1/projects/1/timeline", nil)
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
		}
		if header != "" {
			r.Header.Set(sessionHeaderName, header)
		}
		return r
	}

	cases := []struct {
		name                string
		method, cookie, hdr string
		want                bool
	}{
		{"get with cookie", http.MethodGet, id, "", true},
		{"head with cookie", http.MethodHead, id, "", true},
		{"get with header", http.MethodGet, "", id, true},
		{"post with header", http.MethodPost, "", id, true},
		{"put with header", http.MethodPut, "", id, true},
		{"delete with header", http.MethodDelete, "", id, true},
		// The CSRF boundary: a cross-site page can make the browser send the
		// cookie but cannot read it to echo it in a header.
		{"post with cookie alone must fail", http.MethodPost, id, "", false},
		{"put with cookie alone must fail", http.MethodPut, id, "", false},
		{"post with mismatched echo", http.MethodPost, id, other, false},
		{"post with matching echo", http.MethodPost, id, id, true},
		// Junk never reaches the map.
		{"no credential", http.MethodGet, "", "", false},
		{"unknown cookie", http.MethodGet, other, "", false},
		{"unknown header", http.MethodPost, "", other, false},
		{"uppercase hex rejected", http.MethodGet, strings.ToUpper(id), "", false},
		{"short id rejected", http.MethodGet, id[:60], "", false},
		{"long id rejected", http.MethodGet, id + "ab", "", false},
		{"injection-ish value rejected", http.MethodGet, "x\" ; DROP TABLE", "", false},
		{"empty cookie value", http.MethodGet, "", id, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := st.authorize(req(c.method, c.cookie, c.hdr)); got != c.want {
				t.Fatalf("authorize(%s, cookie=%v, header=%v) = %v, want %v",
					c.method, c.cookie != "", c.hdr != "", got, c.want)
			}
		})
	}
}

// remoteCall drives the real handler tree as a LAN peer. Each case gets its own
// peer address so one test's rejections cannot spend another's rate budget.
type remoteCall struct {
	t     *testing.T
	h     http.Handler
	peerN int
}

func (rc *remoteCall) call(method, path, auth, cookie, header string) *httptest.ResponseRecorder {
	rc.t.Helper()
	rc.peerN++
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = fmt.Sprintf("203.0.113.%d:41%03d", rc.peerN%250, rc.peerN%1000)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	}
	if header != "" {
		req.Header.Set(sessionHeaderName, header)
	}
	rec := httptest.NewRecorder()
	rc.h.ServeHTTP(rec, req)
	return rec
}

func sessionHarness(t *testing.T) (*Server, *remoteCall) {
	t.Helper()
	s := testServer(t)
	s.AuthToken = testToken
	return s, &remoteCall{t: t, h: s.Handler()}
}

func TestSessionLoginRoundTrip(t *testing.T) {
	s, rc := sessionHarness(t)

	// Login is a mutation, so the bearer token is the only thing that opens it.
	if rec := rc.call(http.MethodPost, "/api/v1/session", "", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated login: %d, want 401", rec.Code)
	}
	rec := rc.call(http.MethodPost, "/api/v1/session", "Bearer "+testToken, "", "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("login with the token: %d %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	id, _ := out["session"].(string)
	if !isWellFormedSessionID(id) {
		t.Fatalf("login returned no usable session: %v", out["session"])
	}
	if out["expires_in"].(float64) != float64(sessionTTL/time.Second) {
		t.Errorf("expires_in %v, want the TTL", out["expires_in"])
	}

	// The cookie must be the one the browser can use and JS cannot read.
	sc := rec.Header().Get("Set-Cookie")
	for _, want := range []string{"xcut_session=" + id, "HttpOnly", "SameSite=Strict", "Path=/"} {
		if !strings.Contains(sc, want) {
			t.Errorf("Set-Cookie missing %q: %q", want, sc)
		}
	}
	// No Secure flag is deliberate (serve has no TLS) — but it must be stated
	// in the docs, so assert it here to make an unexplained change visible.
	if strings.Contains(strings.ToLower(sc), "secure") {
		t.Errorf("Secure set on a cleartext server would break every remote UI: %q", sc)
	}

	// From here the browser reads with the cookie alone...
	if rec := rc.call(http.MethodGet, "/api/v1/health", "", id, ""); rec.Code != http.StatusOK {
		t.Fatalf("GET with the session cookie: %d, want 200", rec.Code)
	}
	// ...and cannot write with it.
	if rec := rc.call(http.MethodPost, "/api/v1/projects", "", id, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("POST with only the cookie: %d, want 401 (the CSRF boundary)", rec.Code)
	}
	// The UI echoes the id in a header for mutations.
	body := `{"name":"from-remote-ui"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(body))
	req.RemoteAddr = "203.0.113.99:41999"
	req.Header.Set("Authorization", "Bearer "+testToken)
	rec2 := httptest.NewRecorder()
	rc.h.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusOK && rec2.Code != http.StatusCreated {
		t.Fatalf("bearer mutation: %d %s", rec2.Code, rec2.Body.String())
	}

	// Signing out kills the session for both transports.
	rec = rc.call(http.MethodDelete, "/api/v1/session", "", id, id)
	if rec.Code != http.StatusOK {
		t.Fatalf("logout: %d %s", rec.Code, rec.Body.String())
	}
	if rec := rc.call(http.MethodGet, "/api/v1/health", "", id, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET after logout: %d, want 401", rec.Code)
	}
	if s.Sessions().valid(id) {
		t.Fatal("session survived logout in the store")
	}
}

// A loopback client never needs a session; this pins that the local path did
// not acquire a second credential path by accident.
func TestSessionNeedsNoCookieLocally(t *testing.T) {
	_, rc := sessionHarness(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.RemoteAddr = "127.0.0.1:9999"
	rec := httptest.NewRecorder()
	rc.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("local GET without any credential: %d, want 200", rec.Code)
	}
}

// A forged cookie must not be distinguishable-by-trying from a missing one —
// both are plain 401s, and neither leaks that the id was close.
func TestSessionForgeryRejected(t *testing.T) {
	_, rc := sessionHarness(t)
	rec := rc.call(http.MethodGet, "/api/v1/health", "", hexID(0x01), "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("forged session accepted: %d", rec.Code)
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["error"] != "unauthorized" {
		t.Errorf("forgery reported as %v, want the same unauthorized as anything else", out["error"])
	}
}
