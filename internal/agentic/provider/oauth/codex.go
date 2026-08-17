// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package oauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OpenAI Codex (ChatGPT OAuth) constants. Mirrors the reference implementation
// in pi (packages/ai/src/auth/oauth/openai-codex.ts).
const (
	codexClientID         = "app_EMoamEEZ73f0CkXaXp7hrann"
	codexAuthBaseURL      = "https://auth.openai.com"
	codexAuthorizeURL     = codexAuthBaseURL + "/oauth/authorize"
	codexTokenURL         = codexAuthBaseURL + "/oauth/token"
	codexRedirectURI      = "http://localhost:1455/auth/callback"
	codexDeviceUserCodeURL = codexAuthBaseURL + "/api/accounts/deviceauth/usercode"
	codexDeviceTokenURL    = codexAuthBaseURL + "/api/accounts/deviceauth/token"
	// CodexDeviceVerificationURI is the page the user visits to approve a
	// device-code login.
	CodexDeviceVerificationURI = codexAuthBaseURL + "/codex/device"
	codexDeviceRedirectURI     = codexAuthBaseURL + "/deviceauth/callback"
	codexDeviceTimeout         = 15 * time.Minute
	codexScope                 = "openid profile email offline_access"
	codexJWTClaimPath          = "https://api.openai.com/auth"
	codexCallbackAddr          = "127.0.0.1:1455"
)

// CodexDeviceAuth holds the pending device-authorization session returned by
// the OpenAI device-auth usercode endpoint.
type CodexDeviceAuth struct {
	DeviceAuthID    string
	UserCode        string
	IntervalSeconds int
}

// LoginCodexBrowser runs the browser (authorization code + PKCE + localhost
// callback) Codex login with production defaults: prints the auth URL via the
// default notifier and prompts on stdin for a manual paste fallback.
func LoginCodexBrowser(ctx context.Context) (*Tokens, error) {
	return loginCodexBrowser(ctx, defaultCodexFlowConfig())
}

// LoginCodexDevice runs the device-code Codex login with production defaults.
func LoginCodexDevice(ctx context.Context) (*Tokens, error) {
	return loginCodexDevice(ctx, defaultCodexFlowConfig())
}

// defaultCodexFlowConfig wires interactive defaults: the auth URL is printed
// to stdout, and a manual-paste prompt reads from stdin.
func defaultCodexFlowConfig() *codexFlowConfig {
	return &codexFlowConfig{
		notifyURL: func(u string) {
			fmt.Printf("Open this URL in your browser:\n%s\n", u)
		},
		notifyDevice: func(a CodexDeviceAuth) {
			fmt.Printf("Open %s and enter code: %s\n", CodexDeviceVerificationURI, a.UserCode)
			fmt.Println("Waiting for authorization...")
		},
	}
}

// codexDeviceCode is the successful poll result: an authorization code plus
// the PKCE verifier minted server-side for the device flow.
type codexDeviceCode struct {
	AuthorizationCode string
	CodeVerifier      string
}

// codexHTTPDoer abstracts HTTP calls for tests.
type codexHTTPDoer func(req *http.Request) (*http.Response, error)

// codexFlowConfig carries the injectable dependencies of the Codex OAuth
// flows. Nil fields fall back to production defaults.
type codexFlowConfig struct {
	doer codexHTTPDoer
	// authBaseURL overrides codexAuthBaseURL in tests (httptest).
	authBaseURL string
	// callbackAddr overrides the local callback listener address in tests.
	callbackAddr string
	// skipCallbackServer disables the local listener (unit tests drive the
	// manual paste path only).
	skipCallbackServer bool
	// openURL, when set, is invoked with the authorization URL so the host can
	// open a browser. Failures are non-fatal.
	openURL func(url string)
	// promptManualCode asks the user to paste a code/redirect URL. It must
	// return ok=false when the user cancels. Nil disables manual paste.
	promptManualCode func() (string, bool)
	// notifyURL is invoked with the authorization URL for display.
	notifyURL func(url string)
	// notifyDevice is invoked with the device authorization info for display.
	notifyDevice func(auth CodexDeviceAuth)
}

func (cfg *codexFlowConfig) httpDoer() codexHTTPDoer {
	if cfg != nil && cfg.doer != nil {
		return cfg.doer
	}
	return http.DefaultClient.Do
}

func (cfg *codexFlowConfig) baseURL() string {
	if cfg != nil && cfg.authBaseURL != "" {
		return cfg.authBaseURL
	}
	return codexAuthBaseURL
}

// ---------------------------------------------------------------------------
// Small helpers
// ---------------------------------------------------------------------------

func codexState() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate state: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// parseCodexAuthorizationInput accepts a bare code, a full redirect URL, a
// "code#state" pair, or a query string, mirroring pi's parseAuthorizationInput.
func parseCodexAuthorizationInput(input string) (code, state string) {
	value := strings.TrimSpace(input)
	if value == "" {
		return "", ""
	}
	if u, err := url.Parse(value); err == nil && u.IsAbs() {
		return u.Query().Get("code"), u.Query().Get("state")
	}
	if strings.Contains(value, "#") {
		parts := strings.SplitN(value, "#", 2)
		return parts[0], parts[1]
	}
	if strings.Contains(value, "code=") {
		if params, err := url.ParseQuery(value); err == nil {
			return params.Get("code"), params.Get("state")
		}
	}
	return value, ""
}

// extractCodexAccountID decodes the access-token JWT and returns the
// chatgpt_account_id claim nested under the OpenAI auth claim path. Returns ""
// when the token is malformed or the claim is absent.
func extractCodexAccountID(accessToken string) string {
	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// Access tokens commonly omit padding but tolerate either encoding.
		if payload, err = base64.URLEncoding.DecodeString(parts[1]); err != nil {
			return ""
		}
	}
	var claims map[string]json.RawMessage
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	raw, ok := claims[codexJWTClaimPath]
	if !ok {
		return ""
	}
	var authClaim struct {
		ChatGPTAccountID string `json:"chatgpt_account_id"`
	}
	if err := json.Unmarshal(raw, &authClaim); err != nil {
		return ""
	}
	return authClaim.ChatGPTAccountID
}

// codexTokenResponse is the token endpoint payload.
type codexTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

// readCodexTokenResponse validates and converts a token endpoint response.
func readCodexTokenResponse(resp *http.Response, operation string) (*Tokens, error) {
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenAI Codex token %s failed (%d): %s", operation, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var parsed codexTokenResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("OpenAI Codex token %s: parse response: %w", operation, err)
	}
	if parsed.AccessToken == "" || parsed.RefreshToken == "" || parsed.ExpiresIn <= 0 {
		return nil, fmt.Errorf("OpenAI Codex token %s response missing fields: %s", operation, strings.TrimSpace(string(body)))
	}
	tokens := &Tokens{
		AccessToken:  parsed.AccessToken,
		RefreshToken: parsed.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(parsed.ExpiresIn) * time.Second),
		TokenType:    "Bearer",
		AccountID:    extractCodexAccountID(parsed.AccessToken),
	}
	return tokens, nil
}

// ---------------------------------------------------------------------------
// OpenAI Codex OAuth provider (token management)
// ---------------------------------------------------------------------------

// Refresh exchanges a Codex refresh token for a fresh token set, preserving
// the account id from the new access token.
func (o *OpenAICodexOAuth) Refresh(ctx context.Context, refreshToken string) (*Tokens, error) {
	data := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {codexClientID},
	}
	req, err := http.NewRequestWithContext(ctx, "POST", codexTokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("OpenAI Codex token refresh error: %w", err)
	}
	return readCodexTokenResponse(resp, "refresh")
}

// ---------------------------------------------------------------------------
// Browser (authorization code + PKCE + localhost callback) flow
// ---------------------------------------------------------------------------

// codexCallbackResult carries the code captured by the local callback server.
type codexCallbackResult struct {
	code string
	err  error
}

// startCodexCallbackServer listens on the callback address and returns the
// first authorization code whose state matches, plus the address actually
// bound (relevant when the port is ephemeral). The returned channel yields
// exactly one result; the server shuts down afterwards or on ctx cancel.
func startCodexCallbackServer(ctx context.Context, cfg *codexFlowConfig, state string) (<-chan codexCallbackResult, string, func(), error) {
	if cfg != nil && cfg.skipCallbackServer {
		ch := make(chan codexCallbackResult, 1)
		return ch, "", func() {}, nil
	}
	addr := codexCallbackAddr
	if cfg != nil && cfg.callbackAddr != "" {
		addr = cfg.callbackAddr
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, "", nil, fmt.Errorf("oauth callback listener: %w", err)
	}

	resultCh := make(chan codexCallbackResult, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		switch {
		case query.Get("state") != state:
			writeCodexCallbackPage(w, http.StatusBadRequest, "State mismatch.")
			resultCh <- codexCallbackResult{err: fmt.Errorf("oauth callback state mismatch")}
		case query.Get("code") == "":
			writeCodexCallbackPage(w, http.StatusBadRequest, "Missing authorization code.")
			resultCh <- codexCallbackResult{err: fmt.Errorf("oauth callback missing code")}
		default:
			writeCodexCallbackPage(w, http.StatusOK, "OpenAI authentication completed. You can close this window.")
			resultCh <- codexCallbackResult{code: query.Get("code")}
		}
	})
	server := &http.Server{Handler: mux}
	go func() { _ = server.Serve(listener) }()
	go func() {
		<-ctx.Done()
		_ = server.Close()
	}()

	shutdown := func() { _ = server.Close() }
	return resultCh, listener.Addr().String(), shutdown, nil
}

func writeCodexCallbackPage(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(w, "<!doctype html><html><head><title>OpenAI Codex Login</title></head><body><p>%s</p></body></html>", message)
}

// codexAuthURL builds the authorize URL for the browser flow.
func codexAuthURL(cfg *codexFlowConfig, pkce *PKCEParams, state string) (string, error) {
	u, err := url.Parse(cfg.baseURL() + "/oauth/authorize")
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", codexClientID)
	q.Set("redirect_uri", codexRedirectURI)
	q.Set("scope", codexScope)
	q.Set("code_challenge", pkce.CodeChallenge)
	q.Set("code_challenge_method", "S256")
	q.Set("state", state)
	q.Set("id_token_add_organizations", "true")
	q.Set("codex_cli_simplified_flow", "true")
	q.Set("originator", "goa")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// loginCodexBrowser runs the authorization-code flow: local callback listener,
// optional browser open, manual paste fallback, then code exchange.
func loginCodexBrowser(ctx context.Context, cfg *codexFlowConfig) (*Tokens, error) {
	if cfg == nil {
		cfg = &codexFlowConfig{}
	}
	pkce, err := GeneratePKCE()
	if err != nil {
		return nil, err
	}
	state, err := codexState()
	if err != nil {
		return nil, err
	}
	authURL, err := codexAuthURL(cfg, pkce, state)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	resultCh, _, shutdown, err := startCodexCallbackServer(ctx, cfg, state)
	if err != nil {
		return nil, err
	}
	defer shutdown()

	if cfg.notifyURL != nil {
		cfg.notifyURL(authURL)
	}
	if cfg.openURL != nil {
		cfg.openURL(authURL)
	}

	code, err := waitCodexCode(ctx, cfg, resultCh, state)
	if err != nil {
		return nil, err
	}
	return exchangeCodexCode(ctx, cfg, code, pkce.CodeVerifier, codexRedirectURI)
}

// waitCodexCode waits for either the local callback or a manual paste,
// whichever delivers a valid code first.
func waitCodexCode(ctx context.Context, cfg *codexFlowConfig, resultCh <-chan codexCallbackResult, state string) (string, error) {
	manualCh := make(chan string, 1)
	if cfg.promptManualCode != nil {
		go func() {
			input, ok := cfg.promptManualCode()
			if ok {
				manualCh <- input
			}
		}()
	}
	for {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("login cancelled")
		case res := <-resultCh:
			if res.err != nil {
				return "", res.err
			}
			return res.code, nil
		case input := <-manualCh:
			return codeFromManualInput(input, state)
		}
	}
}

// codeFromManualInput validates a pasted code/redirect-URL against the
// expected state and extracts the authorization code.
func codeFromManualInput(input, state string) (string, error) {
	code, pastedState := parseCodexAuthorizationInput(input)
	if pastedState != "" && pastedState != state {
		return "", fmt.Errorf("state mismatch")
	}
	if code == "" {
		return "", fmt.Errorf("missing authorization code")
	}
	return code, nil
}

// exchangeCodexCode swaps an authorization code for tokens at the token endpoint.
func exchangeCodexCode(ctx context.Context, cfg *codexFlowConfig, code, verifier, redirectURI string) (*Tokens, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {codexClientID},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {redirectURI},
	}
	req, err := http.NewRequestWithContext(ctx, "POST", cfg.baseURL()+"/oauth/token", strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := cfg.httpDoer()(req)
	if err != nil {
		return nil, fmt.Errorf("OpenAI Codex token exchange error: %w", err)
	}
	return readCodexTokenResponse(resp, "exchange")
}

// ---------------------------------------------------------------------------
// Device-code flow
// ---------------------------------------------------------------------------

// startCodexDeviceAuth requests a device authorization session.
func startCodexDeviceAuth(ctx context.Context, cfg *codexFlowConfig) (*CodexDeviceAuth, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", cfg.baseURL()+"/api/accounts/deviceauth/usercode",
		strings.NewReader(fmt.Sprintf(`{"client_id":%q}`, codexClientID)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := cfg.httpDoer()(req)
	if err != nil {
		// A cancelled context surfaces as a request error from net/http.
		if ctx.Err() != nil {
			return nil, fmt.Errorf("login cancelled")
		}
		return nil, fmt.Errorf("OpenAI Codex device code request error: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("OpenAI Codex device code login is not enabled for this server: use browser login or verify the server URL")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenAI Codex device code request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed struct {
		DeviceAuthID string `json:"device_auth_id"`
		UserCode     string `json:"user_code"`
		Interval     any    `json:"interval"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse device code response: %w", err)
	}
	interval, ok := codexParseInterval(parsed.Interval)
	if parsed.DeviceAuthID == "" || parsed.UserCode == "" || !ok || interval < 0 {
		return nil, fmt.Errorf("invalid OpenAI Codex device code response: %s", strings.TrimSpace(string(body)))
	}
	return &CodexDeviceAuth{
		DeviceAuthID:    parsed.DeviceAuthID,
		UserCode:        parsed.UserCode,
		IntervalSeconds: interval,
	}, nil
}

// codexParseInterval accepts both numeric and string interval encodings.
func codexParseInterval(v any) (int, bool) {
	switch t := v.(type) {
	case float64:
		return int(t), true
	case string:
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(t), "%d", &n); err != nil {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}

// codexPollOnce performs a single device-token poll and classifies the outcome.
func codexPollOnce(ctx context.Context, cfg *codexFlowConfig, device *CodexDeviceAuth) (*codexDeviceCode, bool, error) {
	payload := fmt.Sprintf(`{"device_auth_id":%q,"user_code":%q}`, device.DeviceAuthID, device.UserCode)
	req, err := http.NewRequestWithContext(ctx, "POST", cfg.baseURL()+"/api/accounts/deviceauth/token", strings.NewReader(payload))
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := cfg.httpDoer()(req)
	if err != nil {
		return nil, false, fmt.Errorf("OpenAI Codex device auth poll error: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode == http.StatusOK {
		var parsed struct {
			AuthorizationCode string `json:"authorization_code"`
			CodeVerifier      string `json:"code_verifier"`
		}
		if err := json.Unmarshal(body, &parsed); err != nil || parsed.AuthorizationCode == "" || parsed.CodeVerifier == "" {
			return nil, false, fmt.Errorf("invalid OpenAI Codex device auth token response: %s", strings.TrimSpace(string(body)))
		}
		return &codexDeviceCode{AuthorizationCode: parsed.AuthorizationCode, CodeVerifier: parsed.CodeVerifier}, false, nil
	}

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
		return nil, true, nil
	}

	// Classify JSON error bodies: deviceauth_authorization_pending => pending,
	// slow_down => pending with increased interval (caller applies +5s).
	switch codexDeviceErrorCode(body) {
	case "deviceauth_authorization_pending":
		return nil, true, nil
	case "slow_down":
		return nil, true, errCodexSlowDown
	}
	return nil, false, fmt.Errorf("OpenAI Codex device auth failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
}

// codexDeviceErrorCode extracts the error code from a device-token error body.
// The error field may be a plain string or an object with a code field.
func codexDeviceErrorCode(body []byte) string {
	var errBody struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &errBody) != nil || len(errBody.Error) == 0 {
		return ""
	}
	var codeStr string
	if json.Unmarshal(errBody.Error, &codeStr) == nil {
		return codeStr
	}
	var obj struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(errBody.Error, &obj) == nil {
		return obj.Code
	}
	return ""
}

// errCodexSlowDown marks a slow_down response; the poll loop treats it as
// pending while bumping the interval (RFC 8628 §3.5).
var errCodexSlowDown = fmt.Errorf("slow_down")

// pollCodexDeviceAuth polls until the user authorizes, the deadline passes, or
// the context is cancelled.
func pollCodexDeviceAuth(ctx context.Context, cfg *codexFlowConfig, device *CodexDeviceAuth) (*codexDeviceCode, error) {
	interval := time.Duration(device.IntervalSeconds) * time.Second
	if interval < time.Second {
		interval = time.Second
	}
	deadline := time.Now().Add(codexDeviceTimeout)
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("login cancelled")
		case <-time.After(interval):
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("device flow timed out")
		}
		code, pending, err := codexPollOnce(ctx, cfg, device)
		switch {
		case err == errCodexSlowDown:
			interval += 5 * time.Second
		case err != nil:
			return nil, err
		case !pending:
			return code, nil
		}
	}
}

// loginCodexDevice runs the full device-code login: request user code, notify,
// poll, then exchange the returned authorization code (device redirect URI).
func loginCodexDevice(ctx context.Context, cfg *codexFlowConfig) (*Tokens, error) {
	if cfg == nil {
		cfg = &codexFlowConfig{}
	}
	device, err := startCodexDeviceAuth(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if cfg.notifyDevice != nil {
		cfg.notifyDevice(*device)
	}
	code, err := pollCodexDeviceAuth(ctx, cfg, device)
	if err != nil {
		return nil, err
	}
	return exchangeCodexCode(ctx, cfg, code.AuthorizationCode, code.CodeVerifier, codexDeviceRedirectURI)
}
