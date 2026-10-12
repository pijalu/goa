// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

// Authentication and exposure guards for the web UI (spec §10).
//
// The browser drives the agent: keystrokes reach a live session, uploads write
// files, the screen shows whatever the agent is doing. A server reachable from
// another machine is therefore a remote control, so three things must hold
// before it is ever bound:
//
//  1. an explicit auth mode (none / basic / token) — never "whatever the
//     network hands out";
//  2. the loopback guard: no auth + non-loopback address is refused unless the
//     operator passed --insecure-no-auth, i.e. the unsafe choice is the one
//     that has to be typed;
//  3. brute-force resistance: repeated bad credentials cost an increasing
//     amount of wall clock, and the compare itself takes the same time whether
//     it succeeds or fails.
//
// Credentials are compared in constant time on SHA-256 digests, so neither the
// length nor the position of the first differing byte leaks through timing.

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// AuthMode selects how a request proves it may drive the session.
type AuthMode string

const (
	// AuthNone serves every request unauthenticated. Only safe on loopback.
	AuthNone AuthMode = "none"
	// AuthBasic is HTTP Basic with a username/password pair.
	AuthBasic AuthMode = "basic"
	// AuthToken is a single bearer token, presented either as
	// `Authorization: Bearer <token>` or through the login cookie set by
	// POST /login. The cookie exists so a browser can be let in once and then
	// reload/reconnect without the page holding the secret.
	AuthToken AuthMode = "token"
)

// ParseAuthMode maps a user-supplied string onto an AuthMode. An empty value
// means AuthNone.
func ParseAuthMode(s string) (AuthMode, error) {
	switch AuthMode(strings.ToLower(strings.TrimSpace(s))) {
	case "", AuthNone:
		return AuthNone, nil
	case AuthBasic:
		return AuthBasic, nil
	case AuthToken:
		return AuthToken, nil
	default:
		return "", fmt.Errorf("unknown auth mode %q (want none, basic or token)", s)
	}
}

// AuthConfig describes the credentials and the brute-force policy.
//
// The zero value is AuthNone with no lockout window: a loopback server needs no
// credential, and NewServer fills in the defaults below.
type AuthConfig struct {
	// Mode is the authentication scheme in force.
	Mode AuthMode
	// Username/Password are the Basic credentials.
	Username string
	Password string
	// Token is the bearer secret for AuthToken.
	Token string
	// LockoutThreshold is how many consecutive failures from one client trip
	// the lockout (0 = LockoutDefaults).
	LockoutThreshold int
	// LockoutWindow is how long a tripped lockout lasts (0 = LockoutDefaults).
	LockoutWindow time.Duration
	// Clock is the time source (tests inject a fake).
	Clock func() time.Time
}

const (
	// LockoutDefaults: five wrong credentials, then one minute of silence.
	// Long enough to make an offline search pointless, short enough that a
	// typo is not a coffee break.
	lockoutThresholdDefault = 5
	lockoutWindowDefault    = time.Minute
)

// AuthCookieName is the cookie POST /login sets. HttpOnly (script must never
// read the secret), SameSite=Strict (no cross-site submission), Secure whenever
// the request itself was TLS.
const AuthCookieName = "goa_auth"

// ErrLockout is returned by Authenticate once a client is serving a lockout.
var ErrLockout = errors.New("too many failed authentication attempts")

// Authenticator answers one question: is this request allowed in?
//
// It is an interface on purpose. The server depends on the behaviour, not on
// the credential storage, so a future OIDC or passkey scheme is a new
// implementation and not a rewrite of the middleware.
type Authenticator interface {
	// Authenticate reports whether r carries valid credentials. It returns
	// ErrLockout when the client is currently locked out, which the caller
	// must turn into 429 rather than 401 — a locked-out client gets no
	// chance to guess again.
	Authenticate(r *http.Request) error
	// LockedOut reports whether the client behind r is inside a lockout
	// window (used to answer 429 before doing any credential work).
	LockedOut(r *http.Request) bool
	// RetryAfterSeconds is what a 429 advertises, so the client knows when the
	// door reopens instead of guessing.
	RetryAfterSeconds() int
	// RecordFailure notes one rejected attempt.
	RecordFailure(r *http.Request)
	// RecordSuccess clears the failure history for the client.
	RecordSuccess(r *http.Request)
	// Challenge writes the 401 that asks the client to authenticate
	// (WWW-Authenticate for Basic, a redirect to the login page otherwise).
	Challenge(w http.ResponseWriter, r *http.Request)
	// LoginPath is the URL the Challenge redirects to, "" for Basic.
	LoginPath() string
}

// NewAuthenticator builds the Authenticator described by cfg. An AuthNone config
// yields nil, and every middleware treats nil as "no gate" — the guard that
// actually matters for AuthNone is the loopback check in CheckExposure.
func NewAuthenticator(cfg AuthConfig) (Authenticator, error) {
	mode, err := ParseAuthMode(string(cfg.Mode))
	if err != nil {
		return nil, err
	}
	cfg.Mode = mode
	switch mode {
	case AuthNone:
		return nil, nil
	case AuthBasic:
		if cfg.Username == "" || cfg.Password == "" {
			return nil, errors.New("basic auth needs both --server-auth-user and --server-auth-password")
		}
		return &basicAuth{lockoutState: newLockoutState(cfg)}, nil
	case AuthToken:
		if cfg.Token == "" {
			return nil, errors.New("token auth needs --server-auth-token")
		}
		return &tokenAuth{lockoutState: newLockoutState(cfg)}, nil
	}
	return nil, fmt.Errorf("unknown auth mode %q", cfg.Mode)
}

// withDefaults fills the zero values of the policy knobs.
func (c AuthConfig) withDefaults() AuthConfig {
	if c.LockoutThreshold <= 0 {
		c.LockoutThreshold = lockoutThresholdDefault
	}
	if c.LockoutWindow <= 0 {
		c.LockoutWindow = lockoutWindowDefault
	}
	if c.Clock == nil {
		c.Clock = time.Now
	}
	return c
}

// clientKey identifies the caller for lockout accounting. The remote address is
// the only identifier available before authentication — which is precisely the
// point: the lockout must be countable by an anonymous client.
func clientKey(r *http.Request) string {
	if r == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// attemptLog is the per-client failure history. It is shared by every scheme
// through lockoutState, so the policy lives in one place instead of being
// re-implemented (and mis-tuned) per authenticator.
type attemptLog struct {
	mu      sync.Mutex
	entries map[string]*attemptEntry
	clock   func() time.Time
}

type attemptEntry struct {
	failures int
	until    time.Time
}

func newAttemptLog(clock func() time.Time) *attemptLog {
	return &attemptLog{entries: map[string]*attemptEntry{}, clock: clock}
}

func (l *attemptLog) lockedOut(key string) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[key]
	if e == nil || e.until.IsZero() {
		return 0, false
	}
	if !l.clock().Before(e.until) {
		delete(l.entries, key)
		return 0, false
	}
	return e.until.Sub(l.clock()), true
}

func (l *attemptLog) fail(key string, threshold int, window time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[key]
	if e == nil {
		e = &attemptEntry{}
		l.entries[key] = e
	}
	e.failures++
	if e.failures >= threshold {
		e.until = l.clock().Add(window)
		e.failures = 0
	}
}

func (l *attemptLog) success(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, key)
}

// lockoutState is the embedded half shared by basicAuth and tokenAuth.
type lockoutState struct {
	log *attemptLog
	cfg AuthConfig
}

func newLockoutState(cfg AuthConfig) lockoutState {
	cfg = cfg.withDefaults()
	return lockoutState{log: newAttemptLog(cfg.Clock), cfg: cfg}
}

func (l lockoutState) LockedOut(r *http.Request) bool {
	_, locked := l.log.lockedOut(clientKey(r))
	return locked
}

// RetryAfterSeconds rounds the window up: telling a client to wait 0 seconds
// when the window is sub-second is a lie it will believe.
func (l lockoutState) RetryAfterSeconds() int {
	secs := int(l.cfg.LockoutWindow.Seconds())
	if secs < 1 {
		return 1
	}
	return secs
}

func (l lockoutState) RecordFailure(r *http.Request) {
	l.log.fail(clientKey(r), l.cfg.LockoutThreshold, l.cfg.LockoutWindow)
}

func (l lockoutState) RecordSuccess(r *http.Request) {
	l.log.success(clientKey(r))
}

// basicAuth is HTTP Basic against one fixed credential pair.
type basicAuth struct{ lockoutState }

var _ Authenticator = (*basicAuth)(nil)

func (a *basicAuth) Authenticate(r *http.Request) error {
	if _, locked := a.log.lockedOut(clientKey(r)); locked {
		return ErrLockout
	}
	user, pass, ok := r.BasicAuth()
	if !ok {
		return errors.New("basic: no credentials")
	}
	if !secretEqual(user, a.cfg.Username) || !secretEqual(pass, a.cfg.Password) {
		return errors.New("basic: bad credentials")
	}
	return nil
}

func (a *basicAuth) Challenge(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("WWW-Authenticate", `Basic realm="goa", charset="UTF-8"`)
	http.Error(w, "authentication required", http.StatusUnauthorized)
}

func (a *basicAuth) LoginPath() string { return "" }

// tokenAuth accepts the bearer token as a header or as the login cookie.
type tokenAuth struct{ lockoutState }

var _ Authenticator = (*tokenAuth)(nil)

func (a *tokenAuth) Authenticate(r *http.Request) error {
	if _, locked := a.log.lockedOut(clientKey(r)); locked {
		return ErrLockout
	}
	got := BearerToken(r)
	if got == "" {
		return errors.New("token: no credentials")
	}
	if !secretEqual(got, a.cfg.Token) {
		return errors.New("token: bad credentials")
	}
	return nil
}

func (a *tokenAuth) Challenge(w http.ResponseWriter, r *http.Request) {
	// A browser is sent to the login form; an API client is told, plainly,
	// that it needs the header. Redirecting curl to an HTML page would be a
	// worse error message than the truth.
	if wantsHTML(r) {
		http.Redirect(w, r, a.LoginPath()+"?next="+urlQueryEscape(r.URL.RequestURI()), http.StatusFound)
		return
	}
	w.Header().Set("WWW-Authenticate", "Bearer")
	http.Error(w, "authentication required", http.StatusUnauthorized)
}

func (a *tokenAuth) LoginPath() string { return "/login" }

// BearerToken extracts the presented token: the Authorization header first,
// the login cookie second. A query parameter is deliberately not accepted —
// tokens in URLs end up in logs and in the Referer header.
func BearerToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		if after, ok := cutPrefixFold(h, "Bearer "); ok {
			return strings.TrimSpace(after)
		}
	}
	if c, err := r.Cookie(AuthCookieName); err == nil {
		return c.Value
	}
	return ""
}

// wantsHTML reports whether the client is a browser that should be shown a page
// rather than an HTTP error status.
func wantsHTML(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "text/html")
}

// urlQueryEscape is url.QueryEscape, kept local so the login flow does not grow
// an import for one call.
func urlQueryEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) < len(prefix) || !strings.EqualFold(s[:len(prefix)], prefix) {
		return "", false
	}
	return s[len(prefix):], true
}

// secretEqual compares two secrets without leaking their contents through
// timing. Both sides are hashed first so the comparison runs over a fixed
// length regardless of the secret's length (a length-independent memcmp), and
// ConstantTimeCompare does the rest.
func secretEqual(got, want string) bool {
	g := sha256.Sum256([]byte(got))
	w := sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(g[:], w[:]) == 1
}
