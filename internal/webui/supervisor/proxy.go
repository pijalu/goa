// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package supervisor

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

// handleProxy fronts every child-session route. The session id arrives in
// the path (/s/<id>…) or the query (/ws?s=<id>); the request is handed to
// the child's reverse proxy untouched — the child speaks the ordinary webui
// protocol, WebSocket upgrades included.
func (s *Supervisor) handleProxy(w http.ResponseWriter, r *http.Request) {
	id := requestSessionID(r)
	if id == "" {
		http.NotFound(w, r)
		return
	}
	child := s.resolveFresh(r.Context(), id)
	if child == nil {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}
	s.proxyFor(child).ServeHTTP(w, r)
}

// requestSessionID extracts the session id from the routes a child serves:
// /s/<id>, /s/<id>/text|page, and the query forms /ws?s=, /events?s=,
// /input?s=, /key?s=, /resize?s=, /upload?s=.
func requestSessionID(r *http.Request) string {
	if strings.HasPrefix(r.URL.Path, "/s/") {
		rest := strings.TrimPrefix(r.URL.Path, "/s/")
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			rest = rest[:i]
		}
		if unescaped, err := url.PathUnescape(rest); err == nil {
			return unescaped
		}
		return rest
	}
	return r.URL.Query().Get("s")
}

// proxyFor returns (and caches per child) the reverse proxy that speaks to
// one child over its Unix socket.
func (s *Supervisor) proxyFor(child Child) http.Handler {
	if p, ok := child.(interface{ Proxy() http.Handler }); ok {
		return p.Proxy()
	}
	// A Child implementation without its own proxy (test fakes) gets one
	// that reports the child is not really reachable.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "session has no proxy", http.StatusBadGateway)
	})
}

// dialUnix dials a Unix socket with the timeout ReverseProxy expects.
func dialUnix(ctx context.Context, network, addr string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", addr)
}

// newChildProxy builds the reverse proxy for one child: HTTP over its Unix
// socket, with the child's own bearer token injected — the credentials the
// client presented belong to the SUPERVISOR; the child must never trust
// them, only the token it was born with.
func newChildProxy(sockPath, token string) http.Handler {
	target := &url.URL{Scheme: "http", Host: "goa-child"}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialUnix(ctx, network, sockPath)
		},
		// The child answers SSE streams and WebSocket upgrades; disable the
		// default timeouts that would murder a long-lived stream.
		ResponseHeaderTimeout: 0,
		IdleConnTimeout:       90 * time.Second,
	}
	rp := &httputil.ReverseProxy{
		Transport: transport,
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.Host = target.Host
			req.Header.Set("Authorization", "Bearer "+token)
		},
		// A dead child is a 502 with a reason, not a hang.
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, fmt.Sprintf("session unreachable: %v", err), http.StatusBadGateway)
		},
		// The supervisor is single-origin; its own error pages must not be
		// framed or sniffed any more than a session page would be.
		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Set("X-Content-Type-Options", "nosniff")
			return nil
		},
	}
	return rp
}
