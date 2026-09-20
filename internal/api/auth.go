package api

// Authentication gate (DECISIONS.md D12, SECURITY.md).
//
// The API has always been loopback-only by construction, because there was
// nothing to authenticate a remote peer with. This is that something: a bearer
// token compared in constant time, with the local machine still trusted so the
// desktop client and the double-clicked exe keep working with zero setup.
//
// What stays deliberately hard (enforced in config.Resolve and by
// TestServeRefusesRemoteBind): a remote bind without a token. Removing that is
// a product decision, not a convenience.

import (
	"crypto/subtle"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/xiabee/XCut/internal/xcerr"
)

// Ceilings on the gate's own growth — the "nothing unbounded" rule (AGENTS.md)
// applies to anti-brute-force state as much as to cache bytes.
const (
	// authWindow is how long a peer's failed attempts keep counting.
	authWindow = 5 * time.Minute
	// authMaxFailures is how many rejections one peer may accumulate inside
	// authWindow before it gets 429 instead of a fresh 401.
	authMaxFailures = 20
	// authMaxPeers bounds how many distinct peers the tracker may remember at
	// once, so a spray of source addresses cannot grow it without limit.
	authMaxPeers = 4096
)

// authGate answers "may this request through". nil (no token configured) means
// loopback peers are trusted and everyone else is refused — the same posture as
// before authentication existed, now explicit in the code that implements it.
type authGate struct {
	token    []byte
	log      *slog.Logger
	sessions *sessionStore

	mu       sync.Mutex
	failures map[string]*failureWindow
	now      func() time.Time // tests drive the window
}

// failureWindow counts one peer's rejections inside a rolling authWindow.
type failureWindow struct {
	count int
	first time.Time
}

func newAuthGate(token string, log *slog.Logger, sessions *sessionStore) *authGate {
	if token == "" {
		return nil
	}
	return &authGate{
		token:    []byte(token),
		log:      log,
		sessions: sessions,
		failures: map[string]*failureWindow{},
		now:      time.Now,
	}
}

// apiPrefix is the boundary between user data and the shipped binary: every
// route that can read or change a workspace lives under it, and only it is
// gated. The mux cannot express that split itself — registering a methodless
// "/api/v1/" beside the static "GET /" subtree is a pattern conflict — so the
// gate decides by path.
const apiPrefix = "/api/v1/"

// authenticate wraps the route tree in the gate. Anything outside apiPrefix
// (the embedded UI shell) passes: it carries no user data, and a remote
// operator has to be able to load it in order to be asked for a token.
func (g *authGate) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, apiPrefix) {
			next.ServeHTTP(w, r)
			return
		}
		peer, trusted := peerAddress(r)
		if trusted {
			next.ServeHTTP(w, r)
			return
		}
		if g == nil {
			g.reject(w, r, peer, http.StatusForbidden,
				"this server accepts local connections only", "")
			return
		}
		if g.rateLimited(peer) {
			g.reject(w, r, peer, http.StatusTooManyRequests,
				"too many failed authentication attempts", "")
			return
		}
		if g.validToken(r.Header.Get("Authorization")) {
			next.ServeHTTP(w, r)
			return
		}
		// No bearer token: a browser session may still authorize it, within the
		// method limits sessionStore.authorize enforces (a cookie alone never
		// covers a mutation).
		if g.sessions != nil && g.sessions.authorize(r) {
			next.ServeHTTP(w, r)
			return
		}
		// Only a presented credential can be a guess, so only a presented
		// credential that failed is charged against the brute-force budget. A
		// request with no credential at all — the sign-in page's background
		// health poll, a client that has not been configured yet — cannot
		// authenticate and learns nothing per attempt; charging it would let a
		// human lock themselves out by merely leaving the sign-in dialog open
		// (measured: the UI's 15 s health poll spends the whole 20-failure
		// budget in 5 minutes, and then the correct token gets 429 too).
		if requestPresentedCredential(r) {
			g.noteFailure(peer)
		}
		g.reject(w, r, peer, http.StatusUnauthorized,
			"missing or invalid bearer token", `Bearer realm="xcut"`)
	})
}

// requestPresentedCredential reports whether the request carries something
// that claims to authenticate it: any Authorization header (even a malformed
// one — attempting to authenticate is what the budget punishes), a session id
// echoed in the header, or a session cookie. An empty cookie value carries no
// guessable material and does not count.
func requestPresentedCredential(r *http.Request) bool {
	if r.Header.Get("Authorization") != "" {
		return true
	}
	if r.Header.Get(sessionHeaderName) != "" {
		return true
	}
	for _, c := range r.Cookies() {
		if c.Name == sessionCookieName && c.Value != "" {
			return true
		}
	}
	return false
}

// validToken checks an `Authorization: Bearer <token>` header. The scheme name
// is case-insensitive per RFC 6750; the token is compared byte-exact in
// constant time, so neither a matching prefix nor a length difference leaks how
// close an attempt was.
func (g *authGate) validToken(header string) bool {
	const prefix = "bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return false
	}
	supplied := strings.TrimSpace(header[len(prefix):])
	if supplied == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(supplied), g.token) == 1
}

// rateLimited reports whether a peer has already burned its failure budget.
// It records nothing: a request that authenticates correctly must not be
// charged for the peer next to it in the address book.
func (g *authGate) rateLimited(peer string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	fw, ok := g.failures[peer]
	if !ok {
		return false
	}
	if g.now().Sub(fw.first) > authWindow {
		return false
	}
	return fw.count >= authMaxFailures
}

// noteFailure charges a peer one rejection against its window, creating and
// pruning bounded tracker state as needed.
func (g *authGate) noteFailure(peer string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	fw, ok := g.failures[peer]
	if !ok {
		if len(g.failures) >= authMaxPeers {
			g.purgeLocked(now)
		}
		if len(g.failures) >= authMaxPeers {
			// Still full of live windows: stop tracking rather than grow. The
			// token check remains the real gate; untracked peers simply get a
			// fresh budget, which is no worse than the pre-gate behavior.
			return
		}
		fw = &failureWindow{first: now}
		g.failures[peer] = fw
	}
	if now.Sub(fw.first) > authWindow {
		fw.count, fw.first = 0, now
	}
	fw.count++
}

// purgeLocked drops expired windows. Caller holds g.mu.
func (g *authGate) purgeLocked(now time.Time) {
	for peer, fw := range g.failures {
		if now.Sub(fw.first) > authWindow {
			delete(g.failures, peer)
		}
	}
}

// peerAddress returns the connection's own source address and whether it is
// this machine. Only r.RemoteAddr (which the server fills from the socket) is
// consulted: Host and forwarding headers are attacker-chosen and must never
// decide trust.
func peerAddress(r *http.Request) (string, bool) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr, false
	}
	ip := net.ParseIP(host)
	return host, ip != nil && ip.IsLoopback()
}

// reject answers a failed attempt. Note what is absent: the configured token,
// the supplied header, and any detail about why the comparison failed —
// serve.log is a rotated file in the workspace and may be shared.
func (g *authGate) reject(w http.ResponseWriter, r *http.Request, peer string, status int, message, challenge string) {
	if g != nil && g.log != nil {
		g.log.Warn("auth rejected", "method", r.Method, "path", r.URL.Path,
			"peer", peer, "status", status)
	}
	if challenge != "" {
		w.Header().Set("WWW-Authenticate", challenge)
	}
	writeJSON(w, status, map[string]any{
		"error":   string(authErrorcodeFor(status)),
		"message": message,
	})
}

// authErrorcodeFor names a rejection the way the rest of the API names its
// failures, so a client can tell "wrong token" from "you are locked out" from
// "this server takes no remote clients at all" without reading prose.
func authErrorcodeFor(status int) xcerr.Code {
	switch status {
	case http.StatusForbidden:
		return xcerr.CodeForbidden
	case http.StatusTooManyRequests:
		return xcerr.CodeResourceLimit
	default:
		return xcerr.CodeUnauthorized
	}
}
