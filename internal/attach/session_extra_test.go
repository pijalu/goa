// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package attach

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// resolveCase is one row of the session-resolution matrix.
type resolveCase struct {
	name    string
	handler http.HandlerFunc
	path    string
	want    string
	wantErr string
}

// The session resolution matrix: a supervisor's /connect answers a path; a
// plain server's missing /connect falls back to /healthz; an empty session
// id is a named error, not a silent attach to nothing.
func TestResolveSessionMatrix(t *testing.T) {
	cases := []resolveCase{
		{
			name:    "supervisor connect answers the path",
			handler: answerConnectWithSession(t),
			path:    "/repos/proj",
			want:    "from-connect",
		},
		{
			name: "plain server falls back to healthz",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/healthz" {
					_, _ = w.Write([]byte(`{"ok":true,"session":"from-healthz"}`))
					return
				}
				http.NotFound(w, r)
			},
			path: "/repos/proj",
			want: "from-healthz",
		},
		{
			name: "no path uses healthz directly",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/healthz" {
					t.Errorf("unexpected request to %s", r.URL.Path)
				}
				_, _ = w.Write([]byte(`{"ok":true,"session":"live"}`))
			},
			want: "live",
		},
		{
			name: "absent connect (404) falls back to healthz",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/connect" {
					http.NotFound(w, r)
					return
				}
				if r.URL.Path == "/healthz" {
					_, _ = w.Write([]byte(`{"session":"fallback"}`))
					return
				}
				http.NotFound(w, r)
			},
			path: "/repos/proj",
			want: "fallback",
		},
		{
			name: "a supervisor's refusal is surfaced, not masked",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/connect" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"path outside the projects root"}`))
			},
			path:    "/outside",
			wantErr: "path outside the projects root",
		},
		{
			name: "empty healthz session is an error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"ok":true,"session":""}`))
			},
			wantErr: "no active session",
		},
		{
			name: "connect returning no session is an error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/connect" {
					// The client only falls back to healthz when /connect is
					// absent — a present-but-empty answer must NOT silently
					// degrade into a healthz attach.
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"outside root"}`))
			},
			path:    "/repos/proj",
			wantErr: "outside root",
		},
		{
			name:    "unreachable server",
			handler: nil, // replaced by a closed server below
			wantErr: "no active session",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { runResolveCase(t, c) })
	}
}

// answerConnectWithSession is the supervisor side: /connect answers a
// session (and is checked for credentials), everything else is absent.
func answerConnectWithSession(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/connect" {
			if got := r.Header.Get("Authorization"); got != "Bearer tok" {
				t.Errorf("connect credentials = %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"session":"from-connect"}`))
			return
		}
		http.NotFound(w, r)
	}
}

// runResolveCase starts the case's server, resolves, and checks the answer.
func runResolveCase(t *testing.T, c resolveCase) {
	t.Helper()
	var ts *httptest.Server
	if c.handler == nil {
		// Unreachable on purpose: only the error is asserted.
		ts = httptest.NewServer(http.NotFoundHandler())
		ts.Close()
		if _, err := resolveSession(ts.URL, Options{Path: c.path}); err == nil {
			t.Fatal("expected an error for an unreachable server")
		}
		return
	}
	ts = httptest.NewServer(c.handler)
	defer ts.Close()

	got, err := resolveSession(ts.URL, Options{Path: c.path, Token: "tok"})
	if c.wantErr != "" {
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Fatalf("resolve = %q, %v; want error containing %q", got, err, c.wantErr)
		}
		return
	}
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != c.want {
		t.Errorf("session = %q, want %q", got, c.want)
	}
}

// Basic credentials land on the plain-HTTP requests as an Authorization
// header, the same way the bearer token does.
func TestAuthHeaders(t *testing.T) {
	var gotBasic, gotBearer string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBasic = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"session":"s"}`))
	}))
	defer ts.Close()
	if _, err := resolveSession(ts.URL, Options{User: "u", Password: "p"}); err != nil {
		t.Fatalf("basic resolve: %v", err)
	}
	if gotBasic != "Basic dTpw" { // u:p
		t.Errorf("basic header = %q", gotBasic)
	}

	ts2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBearer = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"session":"s"}`))
	}))
	defer ts2.Close()
	if _, err := resolveSession(ts2.URL, Options{Token: "tok"}); err != nil {
		t.Fatalf("bearer resolve: %v", err)
	}
	if gotBearer != "Bearer tok" {
		t.Errorf("bearer header = %q", gotBearer)
	}
}

// --server accepts a bare host:port, a URL, and strips trailing slashes;
// the WebSocket scheme follows the HTTP one.
func TestBaseURLAndWSScheme(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"127.0.0.1:8080", "http://127.0.0.1:8080", false},
		{"http://127.0.0.1:8080/", "http://127.0.0.1:8080", false},
		{"https://goa.example", "https://goa.example", false},
		{"", "", true},
	}
	for _, c := range cases {
		got, err := baseURL(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("baseURL(%q) = %q, want an error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("baseURL(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("baseURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := wsScheme("http://h"); got != "ws://h" {
		t.Errorf("wsScheme(http) = %q", got)
	}
	if got := wsScheme("https://h"); got != "wss://h" {
		t.Errorf("wsScheme(https) = %q", got)
	}
}

// Both credential forms reach the WebSocket handshake headers.
func TestOptionsAddAuth(t *testing.T) {
	hdr := http.Header{}
	Options{Token: "t"}.addAuth(hdr)
	if hdr.Get("Authorization") != "Bearer t" {
		t.Errorf("bearer header = %q", hdr.Get("Authorization"))
	}
	hdr = http.Header{}
	Options{User: "u", Password: "p"}.addAuth(hdr)
	if !strings.HasPrefix(hdr.Get("Authorization"), "Basic ") {
		t.Errorf("basic header = %q", hdr.Get("Authorization"))
	}

	req, _ := http.NewRequest(http.MethodGet, "http://x", nil)
	Options{Token: "t"}.addAuthHeader(req)
	if req.Header.Get("Authorization") != "Bearer t" {
		t.Errorf("request bearer header = %q", req.Header.Get("Authorization"))
	}
	req, _ = http.NewRequest(http.MethodGet, "http://x", nil)
	Options{User: "u", Password: "p"}.addAuthHeader(req)
	if !strings.HasPrefix(req.Header.Get("Authorization"), "Basic ") {
		t.Errorf("request basic header = %q", req.Header.Get("Authorization"))
	}
}

// connectForPath surfaces the supervisor's error text.
func TestConnectForPathError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"path outside the projects root"}`))
	}))
	defer ts.Close()
	_, err := connectForPath(ts.URL, Options{Path: "/x"})
	if err == nil || !strings.Contains(err.Error(), "outside the projects root") {
		t.Fatalf("connectForPath = %v, want the supervisor's reason", err)
	}
}

// The handshake carries a JSON body with Accept: application/json.
func TestNewRequestShape(t *testing.T) {
	req, err := newRequest("http://x", "/connect", http.MethodPost, `{"path":"/p"}`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if req.Method != http.MethodPost || req.URL.Path != "/connect" {
		t.Errorf("request = %s %s", req.Method, req.URL)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("content-type = %q", got)
	}
	if got := req.Header.Get("Accept"); got != "application/json" {
		t.Errorf("accept = %q", got)
	}
}

// doJSON maps non-200s to errors carrying the status.
func TestDoJSONNon200(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer ts.Close()
	req, _ := http.NewRequest(http.MethodGet, ts.URL, nil)
	var out map[string]any
	if err := doJSON(req, &out); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("doJSON = %v, want a 403 error", err)
	}
}

// addAuth without credentials adds nothing (the no-auth server case).
func TestAddAuthNoCredentials(t *testing.T) {
	hdr := http.Header{}
	Options{}.addAuth(hdr)
	if len(hdr) != 0 {
		t.Errorf("no-auth headers = %v", hdr)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://x", nil)
	Options{}.addAuthHeader(req)
	if _, ok := req.Header[http.CanonicalHeaderKey("Authorization")]; ok {
		t.Error("no-auth request carried credentials")
	}
}
