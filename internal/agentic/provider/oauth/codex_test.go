// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package oauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// mintTestJWT builds an unsigned JWT carrying the given account id under the
// OpenAI auth claim path.
func mintTestJWT(t *testing.T, accountID string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload := map[string]any{
		codexJWTClaimPath: map[string]string{"chatgpt_account_id": accountID},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	body := base64.RawURLEncoding.EncodeToString(raw)
	return header + "." + body + ".sig"
}

func TestExtractCodexAccountID(t *testing.T) {
	tests := []struct {
		name  string
		token string
		want  string
	}{
		{name: "valid jwt", token: mintTestJWT(t, "acct-123"), want: "acct-123"},
		{name: "not a jwt", token: "opaque-token", want: ""},
		{name: "two segments", token: "a.b", want: ""},
		{name: "bad payload b64", token: "a.!!!.b", want: ""},
		{name: "payload not json", token: "a." + base64.RawURLEncoding.EncodeToString([]byte("nope")) + ".b", want: ""},
		{name: "missing claim path", token: func() string {
			raw, _ := json.Marshal(map[string]any{"sub": "x"})
			return "h." + base64.RawURLEncoding.EncodeToString(raw) + ".s"
		}(), want: ""},
		{name: "claim wrong shape", token: func() string {
			raw, _ := json.Marshal(map[string]any{codexJWTClaimPath: "string-instead"})
			return "h." + base64.RawURLEncoding.EncodeToString(raw) + ".s"
		}(), want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractCodexAccountID(tt.token); got != tt.want {
				t.Errorf("extractCodexAccountID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseCodexAuthorizationInput(t *testing.T) {
	tests := []struct{ name, in, code, state string }{
		{name: "empty", in: "  ", code: "", state: ""},
		{name: "bare code", in: "abc123", code: "abc123", state: ""},
		{name: "full url", in: "http://localhost:1455/auth/callback?code=c1&state=s1", code: "c1", state: "s1"},
		{name: "code#state", in: "c2#s2", code: "c2", state: "s2"},
		{name: "query string", in: "code=c3&state=s3", code: "c3", state: "s3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, state := parseCodexAuthorizationInput(tt.in)
			if code != tt.code || state != tt.state {
				t.Errorf("parse(%q) = (%q,%q), want (%q,%q)", tt.in, code, state, tt.code, tt.state)
			}
		})
	}
}

func TestCodexState(t *testing.T) {
	a, err := codexState()
	if err != nil {
		t.Fatalf("codexState: %v", err)
	}
	b, _ := codexState()
	if len(a) != 32 {
		t.Errorf("state length = %d, want 32 hex chars", len(a))
	}
	if a == b {
		t.Error("two states must differ")
	}
}

// codexTestServer returns an httptest server plus a config pointing at it.
func codexTestServer(t *testing.T, handler http.Handler) (*httptest.Server, *codexFlowConfig) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	cfg := &codexFlowConfig{authBaseURL: srv.URL, skipCallbackServer: true}
	return srv, cfg
}

// tokenOKHandler serves a valid token response with an account-id JWT.
func tokenOKHandler(t *testing.T, wantGrant string, accountID string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		if got := r.Form.Get("grant_type"); got != wantGrant {
			t.Errorf("grant_type = %q, want %q", got, wantGrant)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":%q,"refresh_token":"rt-1","expires_in":3600}`, mintTestJWT(t, accountID))
	}
}

func TestExchangeCodexCode_Success(t *testing.T) {
	srv, cfg := codexTestServer(t, tokenOKHandler(t, "authorization_code", "acct-1"))
	_ = srv
	tokens, err := exchangeCodexCode(context.Background(), cfg, "code-1", "verifier-1", codexRedirectURI)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if tokens.AccessToken == "" || tokens.RefreshToken != "rt-1" {
		t.Errorf("unexpected tokens: %+v", tokens)
	}
	if tokens.AccountID != "acct-1" {
		t.Errorf("AccountID = %q, want acct-1", tokens.AccountID)
	}
	if tokens.IsExpired() {
		t.Error("fresh token must not be expired")
	}
}

func TestExchangeCodexCode_ErrorStatus(t *testing.T) {
	_, cfg := codexTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	_, err := exchangeCodexCode(context.Background(), cfg, "c", "v", codexRedirectURI)
	if err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("want 400 error, got %v", err)
	}
}

func TestExchangeCodexCode_MissingFields(t *testing.T) {
	_, cfg := codexTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"access_token":"x"}`)
	}))
	_, err := exchangeCodexCode(context.Background(), cfg, "c", "v", codexRedirectURI)
	if err == nil || !strings.Contains(err.Error(), "missing fields") {
		t.Fatalf("want missing-fields error, got %v", err)
	}
}

func TestCodexRefresh_SendsRefreshGrant(t *testing.T) {
	// Refresh uses the package-level codexTokenURL; reroute via transport.
	srv := httptest.NewServer(tokenOKHandler(t, "refresh_token", "acct-9"))
	defer srv.Close()

	orig := http.DefaultClient
	http.DefaultClient = rerouteClient(t, srv.URL)
	defer func() { http.DefaultClient = orig }()

	prov, err := NewOpenAICodexOAuth()
	if err != nil {
		t.Fatalf("NewOpenAICodexOAuth: %v", err)
	}
	tokens, err := prov.Refresh(context.Background(), "rt-old")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if tokens.AccountID != "acct-9" {
		t.Errorf("AccountID = %q, want acct-9", tokens.AccountID)
	}
	if tokens.RefreshToken != "rt-1" {
		t.Errorf("RefreshToken = %q, want rotated rt-1", tokens.RefreshToken)
	}
}

// rerouteClient returns an http.Client whose transport rewrites every request
// to the test server host, preserving path and query.
func rerouteClient(t *testing.T, target string) *http.Client {
	t.Helper()
	targetURL, err := url.Parse(target)
	if err != nil {
		t.Fatalf("parse target: %v", err)
	}
	return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req = req.Clone(req.Context())
		req.URL.Scheme = targetURL.Scheme
		req.URL.Host = targetURL.Host
		return http.DefaultTransport.RoundTrip(req)
	})}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCodexDeviceAuth_Start(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/accounts/deviceauth/usercode" {
			http.NotFound(w, r)
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["client_id"] != codexClientID {
			t.Errorf("client_id = %q", body["client_id"])
		}
		fmt.Fprint(w, `{"device_auth_id":"da-1","user_code":"ABCD-EFGH","interval":5}`)
	})
	_, cfg := codexTestServer(t, handler)
	dev, err := startCodexDeviceAuth(context.Background(), cfg)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if dev.DeviceAuthID != "da-1" || dev.UserCode != "ABCD-EFGH" || dev.IntervalSeconds != 5 {
		t.Errorf("unexpected device auth: %+v", dev)
	}
}

func TestCodexDeviceAuth_StartStringInterval(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"device_auth_id":"da-2","user_code":"WXYZ-1234","interval":"3"}`)
	})
	_, cfg := codexTestServer(t, handler)
	dev, err := startCodexDeviceAuth(context.Background(), cfg)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if dev.IntervalSeconds != 3 {
		t.Errorf("interval = %d, want 3 (string form)", dev.IntervalSeconds)
	}
}

func TestCodexDeviceAuth_NotEnabled(t *testing.T) {
	_, cfg := codexTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	_, err := startCodexDeviceAuth(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "not enabled") {
		t.Fatalf("want not-enabled error, got %v", err)
	}
}

func TestCodexDeviceAuth_InvalidResponse(t *testing.T) {
	_, cfg := codexTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"user_code":"X"}`)
	}))
	_, err := startCodexDeviceAuth(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("want invalid-response error, got %v", err)
	}
}

func TestCodexDevicePoll_PendingThenComplete(t *testing.T) {
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/accounts/deviceauth/usercode", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"device_auth_id":"da-1","user_code":"ABCD-EFGH","interval":0}`)
	})
	mux.HandleFunc("/api/accounts/deviceauth/token", func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		fmt.Fprint(w, `{"authorization_code":"auth-code-1","code_verifier":"v-1"}`)
	})
	mux.HandleFunc("/oauth/token", tokenOKHandler(t, "authorization_code", "acct-dev").ServeHTTP)

	_, cfg := codexTestServer(t, mux)
	var notified CodexDeviceAuth
	cfg.notifyDevice = func(a CodexDeviceAuth) { notified = a }

	tokens, err := loginCodexDevice(context.Background(), cfg)
	if err != nil {
		t.Fatalf("device login: %v", err)
	}
	if notified.UserCode != "ABCD-EFGH" {
		t.Errorf("notify not called with user code, got %+v", notified)
	}
	if tokens.AccountID != "acct-dev" {
		t.Errorf("AccountID = %q, want acct-dev", tokens.AccountID)
	}
	if calls.Load() < 3 {
		t.Errorf("expected at least 3 poll attempts, got %d", calls.Load())
	}
}

func TestCodexDevicePoll_PendingErrorCode(t *testing.T) {
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/accounts/deviceauth/usercode", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"device_auth_id":"da-1","user_code":"U","interval":0}`)
	})
	mux.HandleFunc("/api/accounts/deviceauth/token", func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 2 {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"deviceauth_authorization_pending"}`)
			return
		}
		fmt.Fprint(w, `{"authorization_code":"ac","code_verifier":"cv"}`)
	})
	mux.HandleFunc("/oauth/token", tokenOKHandler(t, "authorization_code", "a").ServeHTTP)
	_, cfg := codexTestServer(t, mux)
	if _, err := loginCodexDevice(context.Background(), cfg); err != nil {
		t.Fatalf("device login: %v", err)
	}
}

func TestCodexDevicePoll_SlowDownBumpsInterval(t *testing.T) {
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/accounts/deviceauth/usercode", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"device_auth_id":"da-1","user_code":"U","interval":0}`)
	})
	mux.HandleFunc("/api/accounts/deviceauth/token", func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 2 {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"slow_down"}`)
			return
		}
		fmt.Fprint(w, `{"authorization_code":"ac","code_verifier":"cv"}`)
	})
	mux.HandleFunc("/oauth/token", tokenOKHandler(t, "authorization_code", "a").ServeHTTP)
	_, cfg := codexTestServer(t, mux)

	start := time.Now()
	if _, err := loginCodexDevice(context.Background(), cfg); err != nil {
		t.Fatalf("device login: %v", err)
	}
	// First poll after >=1s floor, then slow_down bumps interval by 5s before
	// the second poll: total must exceed 6s.
	if elapsed := time.Since(start); elapsed < 6*time.Second {
		t.Errorf("slow_down not honored: elapsed %v < 6s", elapsed)
	}
}

func TestCodexDevicePoll_Cancel(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/accounts/deviceauth/usercode", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"device_auth_id":"da-1","user_code":"U","interval":5}`)
	})
	_, cfg := codexTestServer(t, mux)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := loginCodexDevice(ctx, cfg)
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("want cancelled error, got %v", err)
	}
}

func TestCodexDevicePoll_FailureStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/accounts/deviceauth/usercode", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"device_auth_id":"da-1","user_code":"U","interval":0}`)
	})
	mux.HandleFunc("/api/accounts/deviceauth/token", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"server_error"}`)
	})
	_, cfg := codexTestServer(t, mux)
	_, err := loginCodexDevice(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("want 500 failure, got %v", err)
	}
}

func TestCodexBrowserFlow_ManualPaste(t *testing.T) {
	var gotURL string
	_, cfg := codexTestServer(t, tokenOKHandler(t, "authorization_code", "acct-web"))
	cfg.notifyURL = func(u string) { gotURL = u }
	// notifyURL fires before promptManualCode in loginCodexBrowser (same
	// goroutine), so gotURL is safely visible here.
	cfg.promptManualCode = func() (string, bool) {
		return "http://localhost:1455/auth/callback?code=pasted-1&state=" + mustStateFromURL(t, gotURL), true
	}
	tokens, err := loginCodexBrowser(context.Background(), cfg)
	if err != nil {
		t.Fatalf("browser login: %v", err)
	}
	if tokens.AccountID != "acct-web" {
		t.Errorf("AccountID = %q, want acct-web", tokens.AccountID)
	}
	// Auth URL must carry the PKCE + client params.
	u, err := url.Parse(gotURL)
	if err != nil {
		t.Fatalf("parse auth url: %v", err)
	}
	q := u.Query()
	for _, key := range []string{"client_id", "code_challenge", "state", "redirect_uri", "scope"} {
		if q.Get(key) == "" {
			t.Errorf("auth url missing %q", key)
		}
	}
	if q.Get("client_id") != codexClientID {
		t.Errorf("client_id = %q", q.Get("client_id"))
	}
	if q.Get("code_challenge_method") != "S256" {
		t.Errorf("challenge method = %q", q.Get("code_challenge_method"))
	}
}

// mustStateFromURL extracts the state param from an authorize URL.
func mustStateFromURL(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	state := u.Query().Get("state")
	if state == "" {
		t.Fatal("auth url has no state")
	}
	return state
}

func TestCodexBrowserFlow_ManualPasteStateMismatch(t *testing.T) {
	_, cfg := codexTestServer(t, tokenOKHandler(t, "authorization_code", "x"))
	cfg.promptManualCode = func() (string, bool) {
		return "code-1#wrong-state", true
	}
	_, err := loginCodexBrowser(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "state mismatch") {
		t.Fatalf("want state-mismatch error, got %v", err)
	}
}

func TestCodexBrowserFlow_Cancel(t *testing.T) {
	_, cfg := codexTestServer(t, tokenOKHandler(t, "authorization_code", "x"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := loginCodexBrowser(ctx, cfg)
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("want cancelled error, got %v", err)
	}
}

// freePort returns an available TCP port on 127.0.0.1.
func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

func TestCodexBrowserFlow_CallbackServer(t *testing.T) {
	// Real listener on an ephemeral port: exercises startCodexCallbackServer.
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	var gotCode string
	mux.Handle("/oauth/token", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotCode = r.Form.Get("code")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":%q,"refresh_token":"rt","expires_in":3600}`, mintTestJWT(t, "acct-cb"))
	}))

	callbackAddr := freePort(t)
	cfg := &codexFlowConfig{authBaseURL: srv.URL, callbackAddr: callbackAddr}
	// Channel synchronizes the auth URL across goroutines (no data race).
	urlCh := make(chan string, 1)
	cfg.notifyURL = func(u string) { urlCh <- u }

	type result struct {
		tokens *Tokens
		err    error
	}
	done := make(chan result, 1)
	go func() {
		tokens, err := loginCodexBrowser(context.Background(), cfg)
		done <- result{tokens, err}
	}()

	var authURL string
	select {
	case authURL = <-urlCh:
	case <-time.After(5 * time.Second):
		t.Fatal("auth url never surfaced")
	}
	state := mustStateFromURL(t, authURL)
	resp, err := http.Get("http://" + callbackAddr + "/auth/callback?code=callback-code&state=" + url.QueryEscape(state))
	if err != nil {
		t.Fatalf("callback request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("callback status = %d", resp.StatusCode)
	}

	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("browser login: %v", res.err)
		}
		if res.tokens.AccountID != "acct-cb" {
			t.Errorf("AccountID = %q", res.tokens.AccountID)
		}
		if gotCode != "callback-code" {
			t.Errorf("exchanged code = %q", gotCode)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("login did not complete after callback")
	}
}

func TestCodexBrowserFlow_CallbackStateMismatch(t *testing.T) {
	callbackAddr := freePort(t)
	_, cfg := codexTestServer(t, tokenOKHandler(t, "authorization_code", "x"))
	cfg.callbackAddr = callbackAddr
	cfg.skipCallbackServer = false // exercise the real listener
	urlCh := make(chan string, 1)
	cfg.notifyURL = func(u string) { urlCh <- u }

	done := make(chan error, 1)
	go func() {
		_, err := loginCodexBrowser(context.Background(), cfg)
		done <- err
	}()

	select {
	case <-urlCh:
	case <-time.After(5 * time.Second):
		t.Fatal("auth url never surfaced")
	}
	resp, err := http.Get("http://" + callbackAddr + "/auth/callback?code=c&state=wrong")
	if err != nil {
		t.Fatalf("callback request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("callback status = %d, want 400", resp.StatusCode)
	}
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "state mismatch") {
			t.Fatalf("want state-mismatch error, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("login did not fail after bad callback")
	}
}

func TestNewOpenAICodexOAuth_Interface(t *testing.T) {
	prov, err := NewOpenAICodexOAuth()
	if err != nil {
		t.Fatalf("NewOpenAICodexOAuth: %v", err)
	}
	var _ OAuthProvider = prov
	if prov.Name() != "openai" {
		t.Errorf("Name = %q, want openai", prov.Name())
	}
	if _, err := prov.AuthURL(context.Background()); err == nil {
		t.Error("AuthURL must error (per-attempt flow)")
	}
	if _, err := prov.Exchange(context.Background(), "code"); err == nil {
		t.Error("Exchange must error (flow-internal)")
	}
	ts, err := prov.TokenSource(context.Background(), &Tokens{AccessToken: "x"})
	if err != nil || ts == nil {
		t.Errorf("TokenSource: %v", err)
	}
}
