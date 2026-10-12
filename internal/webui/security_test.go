// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

// Tests for the exposure middlewares: response hardening, the origin guard,
// body limits and the absence of CORS (spec §10).

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// CHECKPOINT: CSP tests green.
func TestSecurityHeaders_CSPOnEveryResponse(t *testing.T) {
	srv, _ := newTestServer(t, 40, 10)
	for _, target := range []string{"/s/sess-1", "/s/sess-1/text", "/assets/app.js", "/healthz", "/"} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		csp := rec.Header().Get("Content-Security-Policy")
		if csp == "" {
			t.Fatalf("%s: no Content-Security-Policy", target)
		}
		for _, want := range []string{
			"default-src 'none'",
			"frame-ancestors 'none'",
			"base-uri 'none'",
			"form-action 'self'",
			"object-src 'none'",
		} {
			if !strings.Contains(csp, want) {
				t.Errorf("%s: CSP missing %q\n%s", target, want, csp)
			}
		}
		// The whole point of the nonce: nothing runs without it.
		if strings.Contains(csp, "'unsafe-inline'") || strings.Contains(csp, "'unsafe-eval'") {
			t.Errorf("%s: CSP has an escape hatch: %s", target, csp)
		}
	}
}

// The nonce in the header must be the one on the page, or the browser blocks
// the very script the page needs to render the screen.
func TestSecurityHeaders_NonceMatchesTheInlineTags(t *testing.T) {
	srv, _ := newTestServer(t, 40, 10)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/s/sess-1", nil))
	csp := rec.Header().Get("Content-Security-Policy")
	body := rec.Body.String()

	const prefix = "'nonce-"
	i := strings.Index(csp, prefix)
	if i < 0 {
		t.Fatalf("no nonce in CSP: %s", csp)
	}
	rest := csp[i+len(prefix):]
	nonce := rest[:strings.Index(rest, "'")]
	if nonce == "" {
		t.Fatalf("empty nonce in CSP: %s", csp)
	}
	if !strings.Contains(csp, "script-src 'self' 'nonce-"+nonce+"'") {
		t.Errorf("script-src does not carry the page nonce: %s", csp)
	}
	if !strings.Contains(csp, "style-src 'self' 'nonce-"+nonce+"'") {
		t.Errorf("style-src does not carry the page nonce: %s", csp)
	}
	for _, tag := range []string{`<style nonce="` + nonce + `">`, `<script nonce="` + nonce + `">`} {
		if !strings.Contains(body, tag) {
			t.Errorf("page does not use the CSP nonce: missing %q\n%s", tag, body)
		}
	}
}

func TestSecurityHeaders_NonceIsFreshPerResponse(t *testing.T) {
	srv, _ := newTestServer(t, 40, 10)
	seen := map[string]bool{}
	for range 8 {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/s/sess-1", nil))
		n := nonceOf(httptest.NewRequest(http.MethodGet, "/", nil))
		_ = n
		body := rec.Body.String()
		i := strings.Index(body, `<style nonce="`)
		if i < 0 {
			t.Fatal("no nonce on the page")
		}
		rest := body[i+len(`<style nonce="`):]
		seen[rest[:strings.Index(rest, `"`)]] = true
	}
	if len(seen) != 8 {
		t.Fatalf("nonce reused across %d responses (%d distinct)", 8, len(seen))
	}
}

func TestSecurityHeaders_HardeningHeaders(t *testing.T) {
	srv, _ := newTestServer(t, 40, 10)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/s/sess-1", nil))
	want := map[string]string{
		"X-Content-Type-Options":       "nosniff",
		"X-Frame-Options":              "DENY",
		"Referrer-Policy":              "no-referrer",
		"Cross-Origin-Opener-Policy":   "same-origin",
		"Cross-Origin-Resource-Policy": "same-origin",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
}

// Even a 401 is hardened: the login page the browser is redirected to must not
// be rendered in a context that allows the attacker's script.
func TestSecurityHeaders_HardenTheErrorResponses(t *testing.T) {
	srv := newAuthServer(t, AuthConfig{Mode: AuthToken, Token: "s3cret"})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/s/sess-1", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Error("a 401 carries no CSP")
	}
}

// CHECKPOINT: Origin tests green.
func TestOriginGuard_RefusesCrossOriginWrites(t *testing.T) {
	typed := make(chan string, 4)
	vt := NewVirtualTerminal(40, 10)
	vt.Start(func(s string) { typed <- s }, func() {})
	srv := NewServer(vt, 40, 10, ServerOptions{
		SessionID: func() string { return "sess-1" },
		Logger:    discardLogger(),
	})
	t.Cleanup(func() { _ = srv.Close() })

	form := strings.NewReader(`{"data":"typed from evil.example"}`)
	req := httptest.NewRequest(http.MethodPost, "/input", form)
	req.Header.Set("Content-Type", "application/json")
	req.Host = "goa.local:8080"
	req.Header.Set("Origin", "http://evil.example")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin POST = %d, want 403", rec.Code)
	}
	select {
	case s := <-typed:
		t.Errorf("cross-origin input reached the terminal: %q", s)
	default:
	}
}

func TestOriginGuard_AllowsSameOriginAndNativeClients(t *testing.T) {
	srv, _ := newTestServer(t, 40, 10)
	tests := []struct {
		name   string
		origin string
		want   int
	}{
		{name: "same origin", origin: "http://goa.local:8080", want: http.StatusOK},
		{name: "scheme upgrade same host", origin: "https://goa.local:8080", want: http.StatusOK},
		{name: "native client, no origin", want: http.StatusOK},
	}
	for _, tc := range tests {
		req := httptest.NewRequest(http.MethodPost, "/input", strings.NewReader(`{"data":"x"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Host = "goa.local:8080"
		if tc.origin != "" {
			req.Header.Set("Origin", tc.origin)
		}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d", tc.name, rec.Code, tc.want)
		}
	}
}

// Reads are not state changes; a cross-origin GET is answered (and unusable,
// since no CORS header is sent), not refused. Blocking it would break plain
// links to the screen.
func TestOriginGuard_DoesNotRefuseReads(t *testing.T) {
	srv, _ := newTestServer(t, 40, 10)
	req := httptest.NewRequest(http.MethodGet, "/s/sess-1/text", nil)
	req.Host = "goa.local:8080"
	req.Header.Set("Origin", "http://evil.example")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("cross-origin GET = %d, want 200", rec.Code)
	}
}

func TestOriginGuard_RefusesCrossOriginLogin(t *testing.T) {
	srv := newAuthServer(t, AuthConfig{Mode: AuthToken, Token: "s3cret"})
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("token=s3cret"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Host = "goa.local:8080"
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin login = %d, want 403", rec.Code)
	}
	for _, c := range authCookiesOf(rec) {
		if c.Name == AuthCookieName {
			t.Error("a cross-origin login set the auth cookie")
		}
	}
}

// A response may never grant cross-origin access: another site's script must
// not be able to read the session screen or this origin's assets.
func TestNoCORS(t *testing.T) {
	srv, _ := newTestServer(t, 40, 10)
	for _, target := range []string{"/s/sess-1", "/s/sess-1/text", "/assets/app.js", "/healthz", "/assets/app.css"} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.Header.Set("Origin", "http://evil.example")
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		for k := range rec.Header() {
			if strings.HasPrefix(strings.ToLower(k), "access-control-") {
				t.Errorf("%s: response carries CORS header %s: %q", target, k, rec.Header().Get(k))
			}
		}
	}
}

// MaxBytesReader stops the body at the cap: the handler sees an error instead
// of an unbounded stream, so a huge POST cannot pin memory or the disk.
//
// The body is *valid* JSON that is simply enormous, so nothing but the cap can
// refuse it: a malformed-body test would pass for the wrong reason.
func TestBodyLimit_CapsRequestBodies(t *testing.T) {
	srv, _ := newTestServer(t, 40, 10)
	body := `{"data":"` + strings.Repeat("a", MaxRequestBytes+4096) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/input", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("oversize body accepted with 200 (body: %s)", rec.Body)
	}
}

func TestBodyLimit_AcceptsOrdinaryBodies(t *testing.T) {
	srv, _ := newTestServer(t, 40, 10)
	req := httptest.NewRequest(http.MethodPost, "/input", strings.NewReader(`{"data":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ordinary body = %d, want 200 (body: %s)", rec.Code, rec.Body)
	}
}

// The upload path keeps its own content check on top of the transport cap.
func TestBodyLimit_RejectsOversizeUpload(t *testing.T) {
	srv, _ := newTestServer(t, 40, 10)
	body := strings.NewReader("--B\r\nContent-Disposition: form-data; name=\"file\"; filename=\"a.png\"\r\n" +
		"Content-Type: image/png\r\n\r\n" + strings.Repeat("A", MaxUploadBytes+1024) + "\r\n--B--\r\n")
	req := httptest.NewRequest(http.MethodPost, "/upload", body)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=B")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("oversize upload accepted with 200 (body: %s)", rec.Body)
	}
}

// The cap has to be in the *chain*, not merely available: this drives a real
// server with the ceiling lowered below the per-handler limits, so only the
// middleware can refuse.
func TestBodyLimit_IsWiredIntoTheChain(t *testing.T) {
	srv, _ := newTestServer(t, 40, 10)
	// The ceiling is the guard's property now: shrink it to keep the test
	// free of a 4 KiB-past-the-cap allocation dance.
	srv.guard.maxBody = 64
	req := httptest.NewRequest(http.MethodPost, "/input", strings.NewReader(`{"data":"`+strings.Repeat("a", 4096)+`"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("body past the server-wide cap was accepted with 200 (body: %s)", rec.Body)
	}
}

func TestBodyLimit_MiddlewareStopsAHandlerReadingPastTheCap(t *testing.T) {
	g, err := NewGuard(webuiAuthNoneForTest(), 64)
	if err != nil {
		t.Fatalf("guard: %v", err)
	}
	s := g
	var read int64
	var readErr error
	h := s.bodyLimit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		read, readErr = io.Copy(io.Discard, r.Body)
	}))
	h.ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(strings.Repeat("x", 4096))))
	if readErr == nil {
		t.Fatal("reading past the cap must fail, not return a short read")
	}
	if read > 64 {
		t.Fatalf("handler read %d bytes past a 64-byte cap", read)
	}
}

// A handler with no cap of its own is the case the middleware exists for.
func TestBodyLimit_MiddlewareLeavesSmallBodiesAlone(t *testing.T) {
	g, err := NewGuard(webuiAuthNoneForTest(), 4096)
	if err != nil {
		t.Fatalf("guard: %v", err)
	}
	s := g
	var body string
	h := s.bodyLimit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read: %v", err)
		}
		body = string(b)
	}))
	h.ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("hello")))
	if body != "hello" {
		t.Fatalf("body = %q, want hello", body)
	}
}

func TestStripCORS(t *testing.T) {
	h := http.Header{}
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("access-control-allow-credentials", "true")
	h.Set("Content-Type", "text/plain")
	stripCORS(h)
	for k := range h {
		if strings.HasPrefix(strings.ToLower(k), "access-control-") {
			t.Errorf("CORS header survived: %s", k)
		}
	}
	if h.Get("Content-Type") != "text/plain" {
		t.Error("stripCORS removed a header it must not touch")
	}
}

func TestCSPHeader_Shape(t *testing.T) {
	got := cspHeader("abc")
	if !strings.HasPrefix(got, "default-src 'none'; script-src 'self' 'nonce-abc'; style-src 'self' 'nonce-abc'") {
		t.Errorf("unexpected CSP shape:\n%s", got)
	}
	// connect-src 'self' must let the page reach its own WS/SSE endpoints.
	if !strings.Contains(got, "connect-src 'self'") {
		t.Errorf("CSP would block the live transport:\n%s", got)
	}
}

func TestSafeMethod(t *testing.T) {
	safe := []string{http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace}
	for _, m := range safe {
		if !safeMethod(m) {
			t.Errorf("%s must be safe", m)
		}
	}
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		if safeMethod(m) {
			t.Errorf("%s must not be safe", m)
		}
	}
}

func TestSameOrigin(t *testing.T) {
	tests := []struct {
		name   string
		host   string
		origin string
		want   bool
	}{
		{name: "no origin (curl)", host: "127.0.0.1:8080", want: true},
		{name: "browser same origin with port", host: "goa.local:8080", origin: "http://goa.local:8080", want: true},
		{name: "https same host", host: "goa.local", origin: "https://goa.local", want: true},
		{name: "case-insensitive host", host: "GoA.local:9", origin: "http://goa.local:9", want: true},
		{name: "other site", host: "goa.local:8080", origin: "http://evil.example", want: false},
		{name: "lookalike suffix", host: "goa.local", origin: "http://goa.local.evil.example", want: false},
		{name: "empty origin host", host: "goa.local", origin: "null", want: false},
	}
	for _, tc := range tests {
		r := httptest.NewRequest(http.MethodGet, "/ws", nil)
		r.Host = tc.host
		if tc.origin != "" {
			r.Header.Set("Origin", tc.origin)
		}
		if got := sameOrigin(r); got != tc.want {
			t.Errorf("%s: sameOrigin = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestNewNonce_IsUsableInAHeader(t *testing.T) {
	n := newNonce()
	if len(n) < 16 {
		t.Fatalf("nonce too short: %q", n)
	}
	if strings.ContainsAny(n, ` "'=;,`) {
		t.Fatalf("nonce needs header-safe characters: %q", n)
	}
	if _, err := strconv.Atoi(n); err == nil {
		t.Fatal("nonce must not be numeric")
	}
}

// webuiAuthNoneForTest is the no-auth config for guard construction in tests.
func webuiAuthNoneForTest() AuthConfig { return AuthConfig{} }
