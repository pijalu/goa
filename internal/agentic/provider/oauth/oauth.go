// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// OAuthProvider handles OAuth authentication for LLM providers.
type OAuthProvider interface {
	// Name returns the provider name.
	Name() string
	// AuthURL returns the URL to redirect the user to for authorization.
	AuthURL(ctx context.Context) (string, error)
	// Exchange exchanges an authorization code for tokens.
	Exchange(ctx context.Context, code string) (*Tokens, error)
	// Refresh refreshes an access token using a refresh token.
	Refresh(ctx context.Context, refreshToken string) (*Tokens, error)
	// TokenSource returns a token source that auto-refreshes.
	TokenSource(ctx context.Context, tokens *Tokens) (*TokenSource, error)
}

// Tokens holds OAuth token data.
type Tokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	TokenType    string    `json:"token_type"`
}

// IsExpired returns true if the token is expired or expires within 5 minutes.
func (t *Tokens) IsExpired() bool {
	if t.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(t.ExpiresAt.Add(-5 * time.Minute))
}

// TokenSource provides auto-refreshing tokens.
type TokenSource struct {
	mu       sync.Mutex
	provider OAuthProvider
	tokens   *Tokens
}

// NewTokenSource creates a new token source.
func NewTokenSource(provider OAuthProvider, tokens *Tokens) *TokenSource {
	return &TokenSource{provider: provider, tokens: tokens}
}

// Token returns a valid access token, refreshing if necessary.
func (ts *TokenSource) Token(ctx context.Context) (string, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if !ts.tokens.IsExpired() {
		return ts.tokens.AccessToken, nil
	}

	if ts.tokens.RefreshToken == "" {
		return "", fmt.Errorf("token expired and no refresh token available")
	}

	newTokens, err := ts.provider.Refresh(ctx, ts.tokens.RefreshToken)
	if err != nil {
		return "", fmt.Errorf("token refresh failed: %w", err)
	}
	ts.tokens = newTokens
	return newTokens.AccessToken, nil
}

// ---------------------------------------------------------------------------
// PKCE utilities
// ---------------------------------------------------------------------------

// PKCEParams holds PKCE code challenge parameters.
type PKCEParams struct {
	CodeVerifier  string `json:"code_verifier"`
	CodeChallenge string `json:"code_challenge"`
}

// GeneratePKCE creates PKCE challenge parameters.
func GeneratePKCE() (*PKCEParams, error) {
	verifier := make([]byte, 32)
	if _, err := rand.Read(verifier); err != nil {
		return nil, fmt.Errorf("generate verifier: %w", err)
	}
	codeVerifier := base64.RawURLEncoding.EncodeToString(verifier)

	// S256 code challenge
	challengeHash := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(challengeHash[:])

	return &PKCEParams{
		CodeVerifier:  codeVerifier,
		CodeChallenge: codeChallenge,
	}, nil
}

// ---------------------------------------------------------------------------
// Anthropic OAuth (OAT — OAuth for Tools)
// ---------------------------------------------------------------------------

// AnthropicOATConfig configures Anthropic's OAuth for Tools (OAT) flow.
type AnthropicOATConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	Scopes       []string
}

// AnthropicOATProvider implements OAuthProvider for Anthropic OAT.
type AnthropicOATProvider struct {
	config AnthropicOATConfig
	pkce   *PKCEParams
}

// NewAnthropicOATProvider creates a new Anthropic OAT provider.
func NewAnthropicOATProvider(config AnthropicOATConfig) (*AnthropicOATProvider, error) {
	pkce, err := GeneratePKCE()
	if err != nil {
		return nil, err
	}
	return &AnthropicOATProvider{config: config, pkce: pkce}, nil
}

func (p *AnthropicOATProvider) Name() string { return "anthropic" }

func (p *AnthropicOATProvider) AuthURL(ctx context.Context) (string, error) {
	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {p.config.ClientID},
		"redirect_uri":          {p.config.RedirectURI},
		"code_challenge":        {p.pkce.CodeChallenge},
		"code_challenge_method": {"S256"},
	}
	if len(p.config.Scopes) > 0 {
		params["scope"] = []string{strings.Join(p.config.Scopes, " ")}
	}
	return "https://auth.anthropic.com/authorize?" + params.Encode(), nil
}

func (p *AnthropicOATProvider) Exchange(ctx context.Context, code string) (*Tokens, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"code_verifier": {p.pkce.CodeVerifier},
		"redirect_uri":  {p.config.RedirectURI},
		"client_id":     {p.config.ClientID},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://auth.anthropic.com/token", strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token exchange: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("token exchange failed (%d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token,omitempty"`
		ExpiresIn    int    `json:"expires_in"`
		TokenType    string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse token response: %w", err)
	}

	return &Tokens{
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(result.ExpiresIn) * time.Second),
		TokenType:    result.TokenType,
	}, nil
}

func (p *AnthropicOATProvider) Refresh(ctx context.Context, refreshToken string) (*Tokens, error) {
	data := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {p.config.ClientID},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://auth.anthropic.com/token", strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token refresh: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("token refresh failed (%d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token,omitempty"`
		ExpiresIn    int    `json:"expires_in"`
		TokenType    string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse refresh response: %w", err)
	}

	return &Tokens{
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(result.ExpiresIn) * time.Second),
		TokenType:    result.TokenType,
	}, nil
}

func (p *AnthropicOATProvider) TokenSource(ctx context.Context, tokens *Tokens) (*TokenSource, error) {
	return NewTokenSource(p, tokens), nil
}

// ---------------------------------------------------------------------------
// GitHub Copilot OAuth (device code flow)
// ---------------------------------------------------------------------------

// GitHubCopilotOAuth implements OAuth for GitHub Copilot via device code flow.
type GitHubCopilotOAuth struct {
	ClientID string
}

// NewGitHubCopilotOAuth creates a new GitHub Copilot OAuth handler.
func NewGitHubCopilotOAuth() *GitHubCopilotOAuth {
	return &GitHubCopilotOAuth{ClientID: "Iv1.b507a3bf17c2a12c"}
}

// DeviceCodeResponse holds the device code flow response.
type DeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	Interval        int    `json:"interval"`
	ExpiresIn       int    `json:"expires_in"`
}

// RequestDeviceCode starts the device code flow.
func (g *GitHubCopilotOAuth) RequestDeviceCode(ctx context.Context) (*DeviceCodeResponse, error) {
	data := url.Values{
		"client_id": {g.ClientID},
		"scope":     {"read:user", "copilot"},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://github.com/login/device/code", strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("device code request: %w", err)
	}
	defer resp.Body.Close()

	var result DeviceCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parse device code: %w", err)
	}
	return &result, nil
}

// PollForToken polls for the access token after the user authorizes.
func (g *GitHubCopilotOAuth) PollForToken(ctx context.Context, deviceCode string, interval int) (*Tokens, error) {
	data := url.Values{
		"client_id":   {g.ClientID},
		"device_code": {deviceCode},
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
	}

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(interval) * time.Second):
		}

		req, err := http.NewRequestWithContext(ctx, "POST", "https://github.com/login/oauth/access_token", strings.NewReader(data.Encode()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("poll token: %w", err)
		}

		var result struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token,omitempty"`
			TokenType    string `json:"token_type"`
			ExpiresIn    int    `json:"expires_in"`
			Error        string `json:"error,omitempty"`
		}
		json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()

		if result.AccessToken != "" {
			return &Tokens{
				AccessToken:  result.AccessToken,
				RefreshToken: result.RefreshToken,
				ExpiresAt:    time.Now().Add(time.Duration(result.ExpiresIn) * time.Second),
				TokenType:    result.TokenType,
			}, nil
		}

		switch result.Error {
		case "authorization_pending":
			continue
		case "slow_down":
			interval += 5
			continue
		default:
			return nil, fmt.Errorf("device code error: %s", result.Error)
		}
	}
}

// ---------------------------------------------------------------------------
// OpenAI Codex OAuth
// ---------------------------------------------------------------------------

// OpenAICodexOAuth implements OAuth for OpenAI Codex.
type OpenAICodexOAuth struct {
	ClientID string
	pkce     *PKCEParams
}

// NewOpenAICodexOAuth creates a new OpenAI Codex OAuth handler.
func NewOpenAICodexOAuth() (*OpenAICodexOAuth, error) {
	pkce, err := GeneratePKCE()
	if err != nil {
		return nil, err
	}
	return &OpenAICodexOAuth{ClientID: "codex", pkce: pkce}, nil
}

func (o *OpenAICodexOAuth) Exchange(ctx context.Context, code string) (*Tokens, error) {
	data := url.Values{
		"client_id":     {o.ClientID},
		"code":          {code},
		"code_verifier": {o.pkce.CodeVerifier},
	}
	req, err := http.NewRequestWithContext(ctx, "POST", "https://github.com/login/oauth/access_token", strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("codex token exchange: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &Tokens{AccessToken: result.AccessToken, TokenType: result.TokenType}, nil
}

func (g *GitHubCopilotOAuth) Name() string { return "github" }

func (g *GitHubCopilotOAuth) AuthURL(ctx context.Context) (string, error) {
	return "", fmt.Errorf("GitHub Copilot uses device-code flow; use RequestDeviceCode")
}

func (g *GitHubCopilotOAuth) Exchange(ctx context.Context, code string) (*Tokens, error) {
	return nil, fmt.Errorf("GitHub Copilot uses device-code flow; use RequestDeviceCode and PollForToken")
}

func (g *GitHubCopilotOAuth) Refresh(ctx context.Context, refreshToken string) (*Tokens, error) {
	return nil, fmt.Errorf("GitHub Copilot does not support refresh tokens")
}

func (g *GitHubCopilotOAuth) TokenSource(ctx context.Context, tokens *Tokens) (*TokenSource, error) {
	return NewTokenSource(g, tokens), nil
}

func (o *OpenAICodexOAuth) Name() string { return "codex" }

func (o *OpenAICodexOAuth) AuthURL(ctx context.Context) (string, error) {
	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {o.ClientID},
		"code_challenge":        {o.pkce.CodeChallenge},
		"code_challenge_method": {"S256"},
	}
	return "https://github.com/login/oauth/authorize?" + params.Encode(), nil
}

func (o *OpenAICodexOAuth) Refresh(ctx context.Context, refreshToken string) (*Tokens, error) {
	return nil, fmt.Errorf("OpenAI Codex does not support refresh tokens")
}

func (o *OpenAICodexOAuth) TokenSource(ctx context.Context, tokens *Tokens) (*TokenSource, error) {
	return NewTokenSource(o, tokens), nil
}

