package api

// Remote web UI sessions (D12 follow-up).
//
// A bearer header is enough for an API client, but not for a browser: the web
// UI loads media through element URLs — <video src>, thumbnails, subtitle and
// render downloads — and a browser cannot attach a header to those. Query-string
// tokens were rejected in D12 (they land in logs, history and Referer), so the
// document arrives through a cookie instead, and the cookie is only ever
// sufficient for the safe methods.
//
// The split is the security property: a GET may be authorized by the cookie, a
// mutation may not (it needs the bearer token or the session id echoed in a
// request header). A cross-site page cannot read an HttpOnly cookie to echo it,
// and SameSite=Strict stops the cookie being attached to its GETs at all — so
// there is no request shape where the browser's own credential handling is what
// authorizes a change.
//
// Sessions live in memory with a TTL and a hard cap. That is deliberate: a
// restart logs remote UIs out (annoying, harmless), while an unbounded or
// persisted session table would be a growth axis and a revoked-token-walking-
// back-in problem (AGENTS.md rule 4).

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/xiabee/XCut/internal/xcerr"
)

const (
	// sessionTTL bounds how long a remote UI stays signed in without proof of
	// the token. A day is a working session; longer would outlive most uses.
	sessionTTL = 12 * time.Hour
	// maxSessions bounds the table: one entry per signed-in client. Past the
	// cap new logins are refused rather than the map growing.
	maxSessions = 256

	sessionCookieName = "xcut_session"
	sessionHeaderName = "X-Cut-Session"
)

type sessionStore struct {
	mu       sync.Mutex
	sessions map[string]time.Time // id -> expiry
	now      func() time.Time
}

func newSessionStore() *sessionStore {
	return &sessionStore{sessions: map[string]time.Time{}, now: time.Now}
}

// Sessions returns the store, creating it on first use so a Server built as a
// struct literal (every test, and the CLI's construction) needs no new wiring.
func (s *Server) Sessions() *sessionStore {
	s.sessMu.Lock()
	defer s.sessMu.Unlock()
	if s.sessions == nil {
		s.sessions = newSessionStore()
	}
	return s.sessions
}

// issue creates a session id. It reports false when the table is full of live
// sessions — the refusal is the point, not a condition to work around.
func (st *sessionStore) issue() (string, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	now := st.now()
	for id, exp := range st.sessions {
		if !exp.After(now) {
			delete(st.sessions, id)
		}
	}
	if len(st.sessions) >= maxSessions {
		return "", false
	}
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", false
	}
	id := hex.EncodeToString(b[:])
	st.sessions[id] = now.Add(sessionTTL)
	return id, true
}

// valid reports whether id is a live session, without extending it: a session
// that never expires on activity is the behavior users expect from a login,
// but it also makes the cap and the TTL unbounded in wall-clock terms.
func (st *sessionStore) valid(id string) bool {
	if id == "" {
		return false
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	exp, ok := st.sessions[id]
	if !ok {
		return false
	}
	if !st.now().Before(exp) {
		delete(st.sessions, id)
		return false
	}
	return true
}

func (st *sessionStore) revoke(id string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	delete(st.sessions, id)
}

// count is for tests and the bounded-growth assertion.
func (st *sessionStore) count() int {
	st.mu.Lock()
	defer st.mu.Unlock()
	return len(st.sessions)
}

// sessionFromCookie extracts the session id a browser attached to this
// document/subresource request.
func sessionFromCookie(r *http.Request) string {
	c, err := r.Cookie(sessionCookieName)
	if err != nil || !isWellFormedSessionID(c.Value) {
		return ""
	}
	return c.Value
}

func sessionFromHeader(r *http.Request) string {
	v := strings.TrimSpace(r.Header.Get(sessionHeaderName))
	if !isWellFormedSessionID(v) {
		return ""
	}
	return v
}

// isWellFormedSessionID keeps a stray header value from turning into a map
// probe: ids are 64 lowercase hex chars and nothing else.
func isWellFormedSessionID(v string) bool {
	if len(v) != 64 {
		return false
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// authorize answers a peer that did not present the bearer token: does it hold
// a live session, in a form this method may use?
//
// A safe method may ride the cookie — that is the whole reason a cookie exists,
// since the browser fetches media by URL. An unsafe one may not: it must echo
// the id in a header, which a cross-site page cannot produce from an HttpOnly
// cookie (and whose cookie SameSite=Strict keeps off the request in the first
// place). That asymmetry is the CSRF defence, not decoration.
func (st *sessionStore) authorize(r *http.Request) bool {
	id := sessionFromHeader(r)
	if id == "" && isSafeMethod(r.Method) {
		id = sessionFromCookie(r)
	}
	return st.valid(id)
}

// isSafeMethod is HTTP's own division, not a convenience list: these are the
// methods a browser may issue as a side-effect-free subresource.
func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead:
		return true
	default:
		return false
	}
}

// handleSessionStart issues a remote UI session. Reaching here already means
// the caller cleared the gate, i.e. presented the bearer token (a remote peer)
// or is local — so this cannot be used to mint sessions anonymously.
func (s *Server) handleSessionStart(w http.ResponseWriter, r *http.Request) {
	id, ok := s.Sessions().issue()
	if !ok {
		s.writeErr(w, r, xcerr.E(xcerr.CodeResourceLimit,
			"too many signed-in sessions right now; try again once one expires", nil))
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(sessionTTL / time.Second),
		// No Secure flag: serve has no TLS (D12). Setting it would break every
		// remote UI rather than protect one.
	})
	writeJSON(w, http.StatusCreated, map[string]any{
		"session":    id,
		"expires_in": int(sessionTTL / time.Second),
	})
}

// handleSessionEnd drops the session and clears the cookie. A peer can only
// revoke a session it can already present, so no extra authorization is needed
// beyond the gate that got the request here.
func (s *Server) handleSessionEnd(w http.ResponseWriter, r *http.Request) {
	st := s.Sessions()
	if id := sessionFromHeader(r); st.valid(id) {
		st.revoke(id)
	}
	if id := sessionFromCookie(r); st.valid(id) {
		st.revoke(id)
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: "", Path: "/",
		HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
