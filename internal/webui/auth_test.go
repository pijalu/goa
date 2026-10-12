// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

// Tests for the auth + exposure layer (spec §10). Every guard has a test that
// fails when the guard is removed — a security test that only exercises the
// happy path documents nothing.

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func newAuthServer(t *testing.T, cfg AuthConfig) *Server {
	t.Helper()
	vt := NewVirtualTerminal(40, 10)
	vt.Start(func(string) {}, func() {})
	srv := NewServer(vt, 40, 10, ServerOptions{
		SessionID: func() string { return "sess-1" },
		Auth:      cfg,
		Logger:    log.New(io.Discard, "", 0),
	})
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

// fakeClock is a hand-advanced clock so the lockout test needs no sleep.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time      { return c.t }
func (c *fakeClock) add(d time.Duration) { c.t = c.t.Add(d) }

func TestParseAuthMode(t *testing.T) {
	tests := []struct {
		in      string
		want    AuthMode
		wantErr bool
	}{
		{"", AuthNone, false},
		{"none", AuthNone, false},
		{"BASIC", AuthBasic, false},
		{" token ", AuthToken, false},
		{"oauth", "", true},
	}
	for _, tc := range tests {
		got, err := ParseAuthMode(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("ParseAuthMode(%q) err = %v, wantErr %v", tc.in, err, tc.wantErr)
			continue
		}
		if !tc.wantErr && got != tc.want {
			t.Errorf("ParseAuthMode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// CHECKPOINT: off-loopback without auth refuses to start.
func TestCheckExposure_RefusesNonLoopbackWithoutAuth(t *testing.T) {
	tests := []struct {
		name    string
		addr    string
		cfg     AuthConfig
		insec   bool
		wantErr bool
	}{
		{name: "loopback no auth", addr: "127.0.0.1:8080"},
		{name: "ipv6 loopback no auth", addr: "[::1]:8080"},
		{name: "localhost no auth", addr: "localhost:8080"},
		{name: "wildcard no auth", addr: ":8080", wantErr: true},
		{name: "all interfaces no auth", addr: "0.0.0.0:8080", wantErr: true},
		{name: "lan ip no auth", addr: "192.168.1.10:8080", wantErr: true},
		{name: "public ip with token", addr: "0.0.0.0:8080", cfg: AuthConfig{Mode: AuthToken, Token: "t"}},
		{name: "lan ip with basic", addr: "192.168.1.10:8080", cfg: AuthConfig{Mode: AuthBasic, Username: "u", Password: "p"}},
		{name: "wildcard explicitly insecure", addr: "0.0.0.0:8080", insec: true},
		{name: "unknown mode", addr: "0.0.0.0:8080", cfg: AuthConfig{Mode: "kerberos"}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckExposure(tc.addr, tc.cfg, tc.insec)
			if (err != nil) != tc.wantErr {
				t.Fatalf("CheckExposure(%q, %+v, %v) err = %v, wantErr %v", tc.addr, tc.cfg, tc.insec, err, tc.wantErr)
			}
			if err != nil && strings.Contains(err.Error(), "refusing") && !strings.Contains(err.Error(), "--insecure-no-auth") {
				t.Errorf("refusal must name the escape hatch, got: %v", err)
			}
		})
	}
}

func TestIsLoopbackAddr(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:8080", true},
		{"127.0.0.53:1", true},
		{"[::1]:9", true},
		{"localhost:0", true},
		{"LOCALHOST:0", true},
		{"", false},
		{":8080", false},
		{"0.0.0.0:0", false},
		{"10.0.0.5:0", false},
		{"example.com:80", false},
		{"8080", false},
	}
	for _, tc := range tests {
		if got := IsLoopbackAddr(tc.addr); got != tc.want {
			t.Errorf("IsLoopbackAddr(%q) = %v, want %v", tc.addr, got, tc.want)
		}
	}
}

// The server refuses to be constructed at all when the exposure rule fails —
// it must not merely log and carry on.
func TestNewServer_RefusesUnsafeBind(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewServer served an unauthenticated 0.0.0.0 bind without panicking")
		}
	}()
	vt := NewVirtualTerminal(40, 10)
	NewServer(vt, 40, 10, ServerOptions{Addr: "0.0.0.0:0", Logger: discardLogger()})
}

func TestNewAuthenticator_ValidatesConfig(t *testing.T) {
	if _, err := NewAuthenticator(AuthConfig{}); err != nil {
		t.Errorf("AuthNone must build a nil authenticator, got %v", err)
	}
	if _, err := NewAuthenticator(AuthConfig{Mode: AuthBasic}); err == nil {
		t.Error("basic without credentials must fail")
	}
	if _, err := NewAuthenticator(AuthConfig{Mode: AuthToken}); err == nil {
		t.Error("token without a token must fail")
	}
	if _, err := NewAuthenticator(AuthConfig{Mode: "sso"}); err == nil {
		t.Error("unknown mode must fail")
	}
}

// CHECKPOINT: the auth cookie flow passes.
func TestTokenAuth_CookieFlowUnlocksTheSession(t *testing.T) {
	srv := newAuthServer(t, AuthConfig{Mode: AuthToken, Token: "s3cret"})

	// 1. Unauthenticated: refused, and a browser is sent to the login page.
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, browserGet("/s/sess-1"))
	if rec.Code != http.StatusFound {
		t.Fatalf("unauthenticated page status = %d, want 302 to /login", rec.Code)
	}
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "/login") {
		t.Errorf("Location = %q, want a /login redirect", loc)
	}

	// 2. The login form posts the token; the answer is a hardened cookie.
	rec = httptest.NewRecorder()
	form := url.Values{"token": {"s3cret"}, "next": {"/s/sess-1"}}
	srv.Handler().ServeHTTP(rec, browserPost("/login", form))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/s/sess-1" {
		t.Errorf("Location = %q, want /s/sess-1", loc)
	}
	cookie := authCookieOf(t, rec)
	if !cookie.HttpOnly {
		t.Error("auth cookie must be HttpOnly: page script must never read the token")
	}
	if cookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("cookie SameSite = %v, want Strict", cookie.SameSite)
	}
	if cookie.Secure {
		t.Error("plain HTTP login must not set Secure, or the cookie never arrives")
	}

	// 3. That cookie alone opens the page.
	req := browserGet("/s/sess-1")
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("page with cookie = %d, want 200", rec.Code)
	}

	// 4. And it opens the write path too, cookie-only (no header needed).
	req = httptest.NewRequest(http.MethodPost, "/input", strings.NewReader(`{"data":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("input with cookie = %d, want 200 (body: %s)", rec.Code, rec.Body)
	}
}

func TestTokenAuth_BearerHeaderIsAccepted(t *testing.T) {
	srv := newAuthServer(t, AuthConfig{Mode: AuthToken, Token: "s3cret"})

	req := httptest.NewRequest(http.MethodGet, "/s/sess-1", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("bearer page = %d, want 200", rec.Code)
	}

	// A wrong token is refused without a redirect: a scripted client wants the
	// status, not an HTML login page.
	req = httptest.NewRequest(http.MethodGet, "/s/sess-1", nil)
	req.Header.Set("Authorization", "Bearer nope")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad bearer = %d, want 401", rec.Code)
	}
	if wa := rec.Header().Get("WWW-Authenticate"); !strings.Contains(wa, "Bearer") {
		t.Errorf("WWW-Authenticate = %q, want a Bearer challenge", wa)
	}
}

// A token in the query string would end up in logs and in the Referer header,
// so it is not a credential as far as the server is concerned.
func TestTokenAuth_QueryParameterIsNotACredential(t *testing.T) {
	srv := newAuthServer(t, AuthConfig{Mode: AuthToken, Token: "s3cret"})
	req := httptest.NewRequest(http.MethodGet, "/s/sess-1?token=s3cret", nil)
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusFound {
		t.Fatalf("query token = %d, want a refusal (401/302)", rec.Code)
	}
}

func TestBasicAuth_RequiresCredentials(t *testing.T) {
	srv := newAuthServer(t, AuthConfig{Mode: AuthBasic, Username: "op", Password: "hunter2"})

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/s/sess-1", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no credentials = %d, want 401", rec.Code)
	}
	if wa := rec.Header().Get("WWW-Authenticate"); !strings.HasPrefix(wa, "Basic ") {
		t.Errorf("WWW-Authenticate = %q, want a Basic challenge", wa)
	}

	req := httptest.NewRequest(http.MethodGet, "/s/sess-1", nil)
	req.SetBasicAuth("op", "hunter2")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("good credentials = %d, want 200", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/s/sess-1", nil)
	req.SetBasicAuth("op", "hunter3")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d, want 401", rec.Code)
	}
}

// Basic auth has no login page: the browser negotiates it, so /login must not
// exist and an unauthenticated page must 401, not redirect.
func TestBasicAuth_HasNoLoginPage(t *testing.T) {
	srv := newAuthServer(t, AuthConfig{Mode: AuthBasic, Username: "op", Password: "hunter2"})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/login", nil))
	if rec.Code == http.StatusOK {
		t.Error("basic auth must not serve a login page")
	}
}

// Brute force has to cost time: five wrong answers lock the client out for a
// minute, and even the correct answer is refused meanwhile.
func TestAuthLockout(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1700000000, 0)}
	srv := newAuthServer(t, AuthConfig{
		Mode:             AuthToken,
		Token:            "s3cret",
		LockoutThreshold: 3,
		LockoutWindow:    time.Minute,
		Clock:            clock.now,
	})

	try := func(token string) int {
		req := httptest.NewRequest(http.MethodGet, "/s/sess-1", nil)
		req.RemoteAddr = "203.0.113.9:51000"
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec.Code
	}

	for i := 1; i <= 2; i++ {
		if code := try("wrong"); code != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d, want 401", i, code)
		}
	}
	if code := try("wrong"); code != http.StatusTooManyRequests {
		t.Fatalf("tripping attempt = %d, want 429", code)
	}
	// Inside the window even the right token is refused: the lockout is on the
	// client, not on the guess.
	if code := try("s3cret"); code != http.StatusTooManyRequests {
		t.Fatalf("correct token during lockout = %d, want 429", code)
	}
	clock.add(time.Minute)
	if code := try("s3cret"); code != http.StatusOK {
		t.Fatalf("after the window = %d, want 200", code)
	}
}

// A success clears the client's history: someone who types the token correctly
// once must not be one mistake away from a lockout.
func TestAuthLockout_SuccessResetsTheCount(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1700000000, 0)}
	srv := newAuthServer(t, AuthConfig{
		Mode: AuthToken, Token: "s3cret",
		LockoutThreshold: 3, LockoutWindow: time.Minute, Clock: clock.now,
	})
	try := func(token string) int {
		req := httptest.NewRequest(http.MethodGet, "/s/sess-1", nil)
		req.RemoteAddr = "198.51.100.4:1234"
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec.Code
	}
	if code := try("wrong"); code != http.StatusUnauthorized {
		t.Fatalf("first = %d, want 401", code)
	}
	if code := try("s3cret"); code != http.StatusOK {
		t.Fatalf("success = %d, want 200", code)
	}
	for i := 0; i < 2; i++ {
		if code := try("wrong"); code != http.StatusUnauthorized {
			t.Fatalf("post-reset failure %d = %d, want 401", i, code)
		}
	}
	if code := try("s3cret"); code != http.StatusOK {
		t.Fatalf("still open after reset = %d, want 200", code)
	}
}

// One noisy client must not lock out the rest of the machine.
func TestAuthLockout_IsPerClient(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1700000000, 0)}
	srv := newAuthServer(t, AuthConfig{
		Mode: AuthToken, Token: "s3cret",
		LockoutThreshold: 2, LockoutWindow: time.Minute, Clock: clock.now,
	})
	hammer := func(addr string) int {
		req := httptest.NewRequest(http.MethodGet, "/s/sess-1", nil)
		req.RemoteAddr = addr
		req.Header.Set("Authorization", "Bearer wrong")
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec.Code
	}
	hammer("203.0.113.1:1")
	hammer("203.0.113.1:1")
	hammer("203.0.113.1:1")
	req := httptest.NewRequest(http.MethodGet, "/s/sess-1", nil)
	req.RemoteAddr = "203.0.113.2:1"
	req.Header.Set("Authorization", "Bearer s3cret")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("bystander = %d, want 200 (lockout must be per client)", rec.Code)
	}
}

// Constant-time comparison is a property of the helper, not of a timing test:
// a test that waits for a measurable timing difference would be flaky in both
// directions. What is pinned here is the contract — equal secrets match,
// anything else does not, including the length-only and near-miss cases a
// byte-wise memcmp would leak.
func TestSecretEqual(t *testing.T) {
	same := [][2]string{
		{"", ""},
		{"token", "token"},
		{"a", "a"},
		{strings.Repeat("x", 64), strings.Repeat("x", 64)},
	}
	for _, p := range same {
		if !secretEqual(p[0], p[1]) {
			t.Errorf("secretEqual(%q, %q) = false, want true", p[0], p[1])
		}
	}
	diff := [][2]string{
		{"token", "toke"},
		{"token", "tokens"},
		{"token", "tokeN"},
		{"a", "b"},
		{"", "x"},
	}
	for _, p := range diff {
		if secretEqual(p[0], p[1]) {
			t.Errorf("secretEqual(%q, %q) = true, want false", p[0], p[1])
		}
	}
}

func TestBearerToken(t *testing.T) {
	tests := []struct {
		name   string
		header string
		cookie string
		want   string
	}{
		{name: "nothing"},
		{name: "header", header: "Bearer abc", want: "abc"},
		{name: "header case-insensitive scheme", header: "bearer abc", want: "abc"},
		{name: "cookie", cookie: "xyz", want: "xyz"},
		{name: "header wins", header: "Bearer abc", cookie: "xyz", want: "abc"},
		{name: "wrong scheme", header: "Basic abc"},
	}
	for _, tc := range tests {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if tc.header != "" {
			req.Header.Set("Authorization", tc.header)
		}
		if tc.cookie != "" {
			req.AddCookie(&http.Cookie{Name: AuthCookieName, Value: tc.cookie})
		}
		if got := BearerToken(req); got != tc.want {
			t.Errorf("%s: BearerToken = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// The login redirect is attacker-steerable through ?next=, so only local
// absolute paths survive.
func TestSafeNext(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"/s/sess-1", "/s/sess-1"},
		{"", "/"},
		{"//evil.example/x", "/"},
		{"https://evil.example", "/"},
		{"javascript:alert(1)", "/"},
		{`/\evil.example`, "/"},
		{`\evil.example`, "/"},
	}
	for _, tc := range tests {
		if got := safeNext(tc.in); got != tc.want {
			t.Errorf("safeNext(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestLoginPost_RedirectTargetIsSanitised(t *testing.T) {
	srv := newAuthServer(t, AuthConfig{Mode: AuthToken, Token: "s3cret"})
	form := url.Values{"token": {"s3cret"}, "next": {"//evil.example/pwned"}}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, browserPost("/login", form))
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Fatalf("Location = %q, want / (open redirect)", loc)
	}
}

func TestLoginPost_WrongTokenIsRefused(t *testing.T) {
	srv := newAuthServer(t, AuthConfig{Mode: AuthToken, Token: "s3cret"})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, browserPost("/login", url.Values{"token": {"nope"}}))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d, want 401", rec.Code)
	}
	if len(authCookiesOf(rec)) != 0 {
		t.Error("a refused login must not set the auth cookie")
	}
}

func TestLoginGet_RendersFormWithSanitisedNext(t *testing.T) {
	srv := newAuthServer(t, AuthConfig{Mode: AuthToken, Token: "s3cret"})
	req := httptest.NewRequest(http.MethodGet, `/login?next=%22%3E%3Cscript%3E`, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login page = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "<script>") {
		t.Errorf("login page reflected script:\n%s", body)
	}
	if !strings.Contains(body, `name="token"`) {
		t.Errorf("login page has no token field:\n%s", body)
	}
}

// browserGet/browserPost build requests that look like a browser: an Accept
// header that wants HTML, and — where it matters — an Origin.
func browserGet(path string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("Accept", "text/html")
	return r
}

func browserPost(path string, form url.Values) *http.Request {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Accept", "application/json, text/html")
	return r
}

func authCookiesOf(rec *httptest.ResponseRecorder) []*http.Cookie {
	return (&http.Response{Header: rec.Result().Header}).Cookies()
}

func authCookieOf(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range authCookiesOf(rec) {
		if c.Name == AuthCookieName {
			return c
		}
	}
	t.Fatalf("no %s cookie in response (headers: %v)", AuthCookieName, rec.Header())
	return nil
}
