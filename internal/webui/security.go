// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

// Exposure guards: the loopback rule, response hardening, cross-origin refusal
// and body limits (spec §10).
//
// These are deliberately small, independent middlewares rather than one
// "security" handler: each is testable on its own, and the server composes
// exactly one of each, in a fixed order, for every request — a hardening step
// that only some routes take is not hardening.

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// MaxRequestBytes caps a request body. The upload path is the largest thing a
// browser legitimately sends (MaxUploadBytes); JSON and form bodies are orders
// of magnitude smaller, so one ceiling over the whole surface is enough — and
// being applied by middleware it also covers a body read before the handler's
// own parsing.
const MaxRequestBytes = MaxUploadBytes

// CheckExposure refuses a configuration that would put an unauthenticated
// remote control on the network.
//
// The rule is one sentence: no auth means loopback. Binding 0.0.0.0 without
// credentials hands anyone on the LAN a keyboard attached to a live agent, so
// that requires an explicit --insecure-no-auth — the operator has to type the
// word "insecure" to get the insecure thing.
func CheckExposure(addr string, auth AuthConfig, insecureNoAuth bool) error {
	if insecureNoAuth {
		return nil
	}
	mode, err := ParseAuthMode(string(auth.Mode))
	if err != nil {
		return err
	}
	if mode != AuthNone {
		return nil
	}
	if IsLoopbackAddr(addr) {
		return nil
	}
	return fmt.Errorf("refusing to serve %s without authentication: "+
		"set --server-auth (basic|token), or pass --insecure-no-auth to accept "+
		"that anyone who can reach this address can drive the agent", addr)
}

// IsLoopbackAddr reports whether addr binds only to the local machine. An empty
// host ("0.0.0.0:8080", ":8080") is every interface and therefore not loopback;
// "localhost" is accepted by name because that is how it is written in a flag.
func IsLoopbackAddr(addr string) bool {
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// nonceCtxKey carries the per-request CSP nonce from the header middleware to
// the renderer that has to put it on the page's own inline <style>/<script>.
type nonceCtxKey struct{}

// nonceOf reads the nonce the current request was rendered with.
func nonceOf(r *http.Request) string {
	n, _ := r.Context().Value(nonceCtxKey{}).(string)
	return n
}

// newNonce mints a per-response CSP nonce. 16 bytes of crypto/rand is enough
// that guessing one is not a strategy; the value only has to be unguessable
// within the life of a single document.
func newNonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing means the process is already in trouble; a
		// predictable nonce would silently weaken the CSP, so say so loudly
		// instead of pretending the header is meaningful.
		panic(fmt.Sprintf("webui: crypto/rand unavailable: %v", err))
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// securityHeaders answers every response with the browser-side policy: a
// nonce-based CSP (no 'unsafe-inline', so an injected <script> tag cannot run),
// plus the sniffing, framing and referrer rules that keep the session screen
// from leaking sideways.
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonce := newNonce()
		h := w.Header()
		h.Set("Content-Security-Policy", cspHeader(nonce))
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		// No CORS: this surface is single-origin by design, so no response
		// may ever carry an access-control-allow-* header. Stripping them
		// here means a future handler cannot loosen the policy by accident.
		stripCORS(h)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), nonceCtxKey{}, nonce)))
	})
}

// cspHeader builds the policy for one document. Everything the page needs is
// same-origin; the two nonces cover its own inline theme block and session
// bootstrap, and everything else is denied outright (no plugins, no base-uri
// hijack, no framing, no form posting anywhere but here).
func cspHeader(nonce string) string {
	return strings.Join([]string{
		"default-src 'none'",
		"script-src 'self' 'nonce-" + nonce + "'",
		"style-src 'self' 'nonce-" + nonce + "'",
		"img-src 'self' data:",
		"connect-src 'self'",
		"base-uri 'none'",
		"form-action 'self'",
		"frame-ancestors 'none'",
		"object-src 'none'",
	}, "; ")
}

// stripCORS removes any CORS grant from a response header set.
func stripCORS(h http.Header) {
	for k := range h {
		if strings.HasPrefix(strings.ToLower(k), "access-control-") {
			h.Del(k)
		}
	}
}

// originGuard refuses cross-origin state changes.
//
// A request without an Origin header is a native client (curl, an SSH tunnel, a
// test) and cannot be steered by another page, so it passes. A browser that
// does send one is held to same-origin: this is the CSRF boundary, and with a
// SameSite=Strict cookie it is the second of the two walls that matter.
func (s *Server) originGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !safeMethod(r.Method) && !sameOrigin(r) {
			http.Error(w, "cross-origin request refused", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// safeMethod reports whether a method is read-only by HTTP definition.
func safeMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	}
	return false
}

// bodyLimit caps request bodies before any handler can read one.
//
// The per-handler caps (postMaxBytes, MaxUploadBytes) stay: they document what
// each endpoint legitimately accepts. This one is the transport-level floor —
// it also covers a handler that forgets its own, and it makes the ceiling a
// server-wide property rather than a convention.
func (s *Server) bodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil {
			next.ServeHTTP(w, r)
			return
		}
		// Read the ceiling per request so it is a live server property rather
		// than a value frozen when the chain was built.
		limit := s.maxRequestBytes
		if limit <= 0 {
			limit = MaxRequestBytes
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		next.ServeHTTP(w, r)
	})
}

// authGate enforces the configured scheme, turning its verdict into 401/429.
func (s *Server) authGate(next http.Handler) http.Handler {
	auth := s.auth
	if auth == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The login page is the one door that must work without a cookie.
		if r.URL.Path == auth.LoginPath() && auth.LoginPath() != "" {
			next.ServeHTTP(w, r)
			return
		}
		err := auth.Authenticate(r)
		switch {
		case err == nil:
			auth.RecordSuccess(r)
			next.ServeHTTP(w, r)
		default:
			if err == ErrLockout {
				writeLockedOut(w, auth)
				return
			}
			auth.RecordFailure(r)
			// The attempt that trips the lockout is answered with 429 itself:
			// the client learns the door closed, and Retry-After says when it
			// reopens.
			if auth.LockedOut(r) {
				writeLockedOut(w, auth)
				return
			}
			auth.Challenge(w, r)
		}
	})
}

// writeLockedOut answers with 429 and the remaining wait.
func writeLockedOut(w http.ResponseWriter, auth Authenticator) {
	w.Header().Set("Retry-After", strconv.Itoa(auth.RetryAfterSeconds()))
	http.Error(w, "too many failed authentication attempts", http.StatusTooManyRequests)
}

// loginPage is the server-rendered form behind token auth. It is a page like any
// other, with the same CSP, so it adds no new attack surface.
const loginPage = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="no-referrer">
<title>goa — sign in</title>
<link rel="stylesheet" href="/assets/app.css">
</head>
<body class="login">
  <h1>goa</h1>
  <form method="post" action="/login">
    <input type="hidden" name="next" value="{{.Next}}">
    <label for="token">Access token</label>
    <input id="token" name="token" type="password" autocomplete="current-password" autofocus required>
    <button type="submit">Sign in</button>
  </form>
</body>
</html>`

// handleLoginGet renders the sign-in form.
func (s *Server) handleLoginGet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte(strings.ReplaceAll(loginPage, "{{.Next}}", htmlAttr(safeNext(r.URL.Query().Get("next"))))))
}

// handleLoginPost exchanges the access token for the session cookie.
//
// Success sets an HttpOnly, SameSite=Strict cookie (Secure whenever the request
// was already TLS) and redirects back; the page never sees the secret again,
// which is the whole reason the cookie exists. Failure counts against the
// lockout, so guessing costs the guesser time.
func (s *Server) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}
	got := r.PostFormValue("token")
	if s.auth == nil || got == "" || !secretEqual(got, s.authToken()) {
		if s.auth != nil {
			if s.auth.LockedOut(r) {
				writeLockedOut(w, s.auth)
				return
			}
			s.auth.RecordFailure(r)
			if s.auth.LockedOut(r) {
				writeLockedOut(w, s.auth)
				return
			}
		}
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}
	s.auth.RecordSuccess(r)
	http.SetCookie(w, &http.Cookie{
		Name:     AuthCookieName,
		Value:    got,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int((24 * time.Hour).Seconds()),
	})
	http.Redirect(w, r, safeNext(r.PostFormValue("next")), http.StatusSeeOther)
}

// authToken exposes the configured bearer token to the login handler. It is the
// one place the secret is readable at request time, and only ever to compare.
func (s *Server) authToken() string {
	if t, ok := s.auth.(*tokenAuth); ok {
		return t.cfg.Token
	}
	return ""
}

// safeNext sanitises a redirect target: a local absolute path only. An
// attacker-supplied "//evil.example", "https://evil.example" or "/\evil.example"
// would turn the login form into an open redirect — browsers normalise the
// backslash form to "//evil.example" — and those values all arrive as query
// parameters.
func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") ||
		strings.ContainsAny(next, `\\`) {
		return "/"
	}
	return next
}

// htmlAttr escapes a value for an HTML attribute context.
func htmlAttr(s string) string {
	return strings.NewReplacer(
		"&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;",
	).Replace(s)
}
