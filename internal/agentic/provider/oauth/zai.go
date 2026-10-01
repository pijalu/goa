// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package oauth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// z.ai / BigModel Coding Plan OAuth (ZCode parity).
//
// WHY THIS EXISTS
//
// The z.ai Coding Plan RESET surface (quota reset credits) is dual-authenticated
// and needs TWO credentials that a plain z.ai API key does not provide:
//
//	Authorization: Bearer <zcode business JWT>
//	X-Bigmodel-Authorization: <family business access token>   (raw, no Bearer)
//
// Before this flow there was no way for a goa user to obtain either: the OAuth
// bridge served only openai/codex, so `goa.auth.oauthToken("zai")` errored and
// the quota plugin's reset fetcher always failed with
// coding_plan_reset_zcode_jwt_required. Hand-pasting tokens into `extra:` was
// the only option. This flow makes `/login:zai:oauth` mint both, from ONE
// authorization.
//
// THE FLOW (two hops)
//
//  1. A BROKER round-trip against zcode.z.ai, polling — no local listener, so
//     it works headless/SSH where a browser callback cannot reach us:
//
//     POST {broker}/oauth/cli/init      Authorization: Bearer <pollToken>
//                                       {provider} → authorize_url, flow_id,
//                                       poll_interval_sec, expires_at
//     GET  {broker}/oauth/cli/poll/{id} Authorization: Bearer <pollToken>
//                                       → {status: pending|ready|failed}
//         on ready: data.token = the ZCODE JWT, data.zai.access_token = the
//         family OAuth access token.
//
//  2. A BUSINESS exchange that turns the OAuth access token into the token the
//     business APIs accept:
//
//     POST https://api.z.ai/api/auth/z/login  {"token": <oauth access token>}
//         → {code, success, data:{access_token, expires_in}}
//
// Both credentials are returned: the zcode JWT in Tokens.ZcodeJWT, the business
// token in Tokens.AccessToken. They are NOT interchangeable — the reset API
// validates each against its own identity, and there is deliberately NO
// cross-family fallback (a bigmodel token is a different identity and is
// rejected by the z.ai endpoint, and vice versa).
//
// FAMILY
//
// zai and bigmodel share the broker and the token endpoint, discriminated by an
// explicit `provider` field in the init body (not inferred from the redirect,
// which would mis-route the exchange). The business login differs per family:
// api.z.ai for zai, open.bigmodel.cn for bigmodel.

// zaiOAuthBrokerBaseURL is zcode's own OAuth broker. It is NOT the inference
// host (api.z.ai) and NOT the chat host (chat.z.ai): the broker is what issues
// the flow, the business login is what mints the token the APIs accept.
const zaiOAuthBrokerBaseURL = "https://zcode.z.ai/api/v1"

// Default z.ai endpoints. Overridable by env so tests and self-hosted mirrors
// can retarget without a rebuild.
const (
	zaiBusinessLoginURL      = "https://api.z.ai/api/auth/z/login"
	bigmodelBusinessLoginURL = "https://open.bigmodel.cn/api/auth/z/login"
)

// ErrZaiOAuthRequired is the stable sentinel every z.ai login failure collapses
// to. Callers (the quota plugin's credential resolution) branch on it rather
// than on message text.
var ErrZaiOAuthRequired = fmt.Errorf("zai_oauth_required")

// zaiFamily identifies which Coding Plan family a login targets.
type zaiFamily string

const (
	zaiFamilyZai      zaiFamily = "zai"
	zaiFamilyBigModel zaiFamily = "bigmodel"
)

// Name returns the family as the provider id used by the auth store and the
// plugin OAuth bridge ("zai" / "bigmodel").
func (f zaiFamily) Name() string { return string(f) }

// businessLoginURL is the family-specific business token exchange.
func (f zaiFamily) businessLoginURL() string {
	if f == zaiFamilyBigModel {
		return envOrDefault("GOA_BIGMODEL_BUSINESS_LOGIN_URL", bigmodelBusinessLoginURL)
	}
	return envOrDefault("GOA_ZAI_BUSINESS_LOGIN_URL", zaiBusinessLoginURL)
}

// zaiHTTPDoer abstracts HTTP calls for tests.
type zaiHTTPDoer func(req *http.Request) (*http.Response, error)

// zaiFlowConfig carries the injectable dependencies of the z.ai login flow.
// Nil fields fall back to production defaults.
type zaiFlowConfig struct {
	doer zaiHTTPDoer
	// brokerBaseURL overrides zaiOAuthBrokerBaseURL in tests (httptest).
	brokerBaseURL string
	// businessLoginURL overrides the family business login in tests.
	businessLoginURL string
	// pollToken is the client-minted bearer the broker correlates the flow by.
	// Empty mints a fresh one.
	pollToken string
	// pollInterval overrides the broker-advised interval in tests.
	pollInterval time.Duration
	// loginTimeout bounds the whole login. Zero uses zaiDefaultLoginTimeout.
	loginTimeout time.Duration
	// notifyURL receives the authorize URL for display (and browser opening by
	// the host). Nil prints to stdout.
	notifyURL func(url string)
	// openURL, when set, is invoked with the authorize URL so the host can open
	// a browser. Failures are non-fatal.
	openURL func(url string)
}

const (
	// zaiDefaultLoginTimeout bounds a login that never completes. The broker
	// caps a flow at ~5 minutes; this is the client-side backstop.
	zaiDefaultLoginTimeout = 6 * time.Minute
	// zaiMinPollInterval floors the broker's poll_interval_sec: a 0 or 1s
	// advice is honored, but a missing/zero value must not become a hot loop.
	zaiMinPollInterval = time.Second
	// zaiResponseLimit caps response bodies (defensive: a hostile/broken server
	// must not be able to exhaust memory).
	zaiResponseLimit = 1 << 20
)

// ZaiUIOpts lets a host (the TUI) bridge the interactive bits of the z.ai login
// into its own UI. All fields optional; nil falls back to stdout.
type ZaiUIOpts struct {
	// NotifyURL receives the authorize URL for display.
	NotifyURL func(url string)
	// OpenURL, when set, opens the authorization URL in a browser.
	OpenURL func(url string)
}

// LoginZai performs the z.ai Coding Plan OAuth login with stdout output.
func LoginZai(ctx context.Context) (*Tokens, error) {
	return loginZai(ctx, defaultZaiFlowConfig())
}

// LoginZaiUI is LoginZai with host-bridged UI callbacks.
func LoginZaiUI(ctx context.Context, ui ZaiUIOpts) (*Tokens, error) {
	cfg := defaultZaiFlowConfig()
	if ui.NotifyURL != nil {
		cfg.notifyURL = ui.NotifyURL
	}
	if ui.OpenURL != nil {
		cfg.openURL = ui.OpenURL
	}
	return loginZai(ctx, cfg)
}

// LoginBigModel performs the BigModel (CN) Coding Plan OAuth login. It shares
// the broker with z.ai but targets its own business host, because the business
// token is family-scoped.
func LoginBigModel(ctx context.Context) (*Tokens, error) {
	return loginZaiFamily(ctx, zaiFamilyBigModel, defaultZaiFlowConfig())
}

// LoginBigModelUI is LoginBigModel with host-bridged UI callbacks.
func LoginBigModelUI(ctx context.Context, ui ZaiUIOpts) (*Tokens, error) {
	cfg := defaultZaiFlowConfig()
	if ui.NotifyURL != nil {
		cfg.notifyURL = ui.NotifyURL
	}
	if ui.OpenURL != nil {
		cfg.openURL = ui.OpenURL
	}
	return loginZaiFamily(ctx, zaiFamilyBigModel, cfg)
}

func defaultZaiFlowConfig() *zaiFlowConfig {
	return &zaiFlowConfig{
		notifyURL: func(u string) { fmt.Printf("Open this URL in your browser:\n%s\n", u) },
	}
}

func (cfg *zaiFlowConfig) httpDoer() zaiHTTPDoer {
	if cfg != nil && cfg.doer != nil {
		return cfg.doer
	}
	return http.DefaultClient.Do
}

func (cfg *zaiFlowConfig) brokerURL() string {
	if cfg != nil && cfg.brokerBaseURL != "" {
		return strings.TrimRight(cfg.brokerBaseURL, "/")
	}
	if v := envOrDefault("GOA_ZCODE_OAUTH_URL", ""); v != "" {
		return strings.TrimRight(v, "/")
	}
	return zaiOAuthBrokerBaseURL
}

func (cfg *zaiFlowConfig) businessURL(family zaiFamily) string {
	if cfg != nil && cfg.businessLoginURL != "" {
		return cfg.businessLoginURL
	}
	return family.businessLoginURL()
}

func (cfg *zaiFlowConfig) flowTimeout() time.Duration {
	if cfg != nil && cfg.loginTimeout > 0 {
		return cfg.loginTimeout
	}
	return zaiDefaultLoginTimeout
}

func (cfg *zaiFlowConfig) notify(u string) {
	if cfg != nil && cfg.notifyURL != nil {
		cfg.notifyURL(u)
		return
	}
	fmt.Printf("Open this URL in your browser:\n%s\n", u)
}

// loginZai runs the z.ai family login.
func loginZai(ctx context.Context, cfg *zaiFlowConfig) (*Tokens, error) {
	return loginZaiFamily(ctx, zaiFamilyZai, cfg)
}

// loginZaiFamily is the whole flow: broker init → user authorizes → poll →
// business exchange. It returns both credentials or an error; it never returns
// a partially-usable token set, because holding an OAuth token the business
// APIs reject is worse than holding none (it would look authenticated).
func loginZaiFamily(ctx context.Context, family zaiFamily, cfg *zaiFlowConfig) (*Tokens, error) {
	ctx, cancel := context.WithTimeout(ctx, cfg.flowTimeout())
	defer cancel()

	pollToken := cfg.pollToken
	if pollToken == "" {
		var err error
		pollToken, err = zaiPollToken()
		if err != nil {
			return nil, err
		}
	}

	ready, err := zaiStartFlow(ctx, cfg, family, pollToken)
	if err != nil {
		return nil, err
	}

	// Surface the URL BEFORE polling: the user needs it now, and the poll may
	// run for minutes. The host may also open it in a browser.
	cfg.notify(ready.AuthorizeURL)
	if cfg != nil && cfg.openURL != nil {
		cfg.openURL(ready.AuthorizeURL)
	}

	outcome, err := zaiPollUntilReady(ctx, cfg, ready, pollToken)
	if err != nil {
		return nil, err
	}

	// The business token is what the business APIs (including the Coding Plan
	// reset surface) actually accept — the OAuth token is only an intermediate.
	businessToken, err := zaiExchangeBusinessToken(ctx, cfg, family, outcome.OAuthToken)
	if err != nil {
		return nil, err
	}

	tokens := &Tokens{
		AccessToken: businessToken,
		ZcodeJWT:    outcome.ZcodeJWT,
		AccountID:   outcome.UserID,
		TokenType:   "Bearer",
	}
	if outcome.UserName != "" {
		tokens.AccountName = outcome.UserName
	}
	// The token's lifetime comes from the READY payload's expires_in, NOT
	// from the init payload's expires_at: the latter is the deadline for
	// completing AUTHORIZATION, not the lifetime of the issued token. Using
	// it here would mark a perfectly fresh token expired whenever the user
	// took their time in the browser.
	if outcome.ExpiresIn > 0 {
		tokens.ExpiresAt = time.Now().Add(time.Duration(outcome.ExpiresIn) * time.Second)
	}
	return tokens, nil
}

// zaiInitData is the broker's init payload (the envelope's data member).
type zaiInitData struct {
	AuthorizeURL    string  `json:"authorize_url"`
	FlowID          string  `json:"flow_id"`
	PollIntervalSec float64 `json:"poll_interval_sec"`
	ExpiresAt       int64   `json:"expires_at"`
}

// zaiPendingFlow is an opened login transaction awaiting browser authorization.
type zaiPendingFlow struct {
	AuthorizeURL string
	FlowID       string
	// PollInterval is the broker-advised wait between polls, in seconds.
	PollInterval float64
	ExpiresAt    int64
}

// zaiStartFlow opens a login transaction and returns the authorize URL the user
// must open. It does NOT wait for authorization.
func zaiStartFlow(ctx context.Context, cfg *zaiFlowConfig, family zaiFamily, pollToken string) (*zaiPendingFlow, error) {
	body, _ := json.Marshal(map[string]string{"provider": family.Name()})
	req, err := http.NewRequestWithContext(ctx, "POST", cfg.brokerURL()+"/oauth/cli/init", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+pollToken)

	data, err := zaiRequest(ctx, cfg, req, "z.ai oauth init")
	if err != nil {
		return nil, err
	}
	var env struct {
		Data zaiInitData `json:"data"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("z.ai oauth init: parse response: %w", err)
	}
	init := env.Data
	if init.FlowID == "" || init.AuthorizeURL == "" {
		return nil, fmt.Errorf("z.ai oauth init: response missing flow_id/authorize_url")
	}
	return &zaiPendingFlow{
		AuthorizeURL: init.AuthorizeURL,
		FlowID:       init.FlowID,
		PollInterval: init.PollIntervalSec,
		ExpiresAt:    init.ExpiresAt,
	}, nil
}

// zaiPollUntilReady polls the broker until the user finishes authorizing, the
// flow expires, or ctx ends, then returns the completed outcome.
//
// The loop body is only the wait/timer; deciding what a poll answer MEANS lives
// in zaiPollDecision, so the state machine is testable on its own.
func zaiPollUntilReady(ctx context.Context, cfg *zaiFlowConfig, init *zaiPendingFlow, pollToken string) (*zaiPollOutcome, error) {
	interval := zaiPollInterval(cfg, init)
	// A zero first wait avoids delaying the first poll; the user may already
	// have authorized by the time they read the URL.
	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		if err := ctxErr(ctx); err != nil {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctxErr(ctx)
		case <-timer.C:
		}

		if flowExpired(init) {
			return nil, fmt.Errorf("z.ai oauth: the login request expired before it was approved — start again with /login:zai:oauth")
		}

		outcome, err := zaiPollOnce(ctx, cfg, init.FlowID, pollToken)
		if err != nil {
			return nil, err
		}
		done, err := zaiPollDecision(outcome)
		if err != nil {
			return nil, err
		}
		if done {
			return outcome, nil
		}
		timer.Reset(interval)
	}
}

// zaiPollDecision interprets one poll answer: done=true ends the wait loop,
// done=false means "still pending, poll again". An error is terminal.
//
// The ready case validates BOTH credentials up front. The zcode JWT is not
// optional: without it the Coding Plan reset surface cannot authenticate, and
// reporting success would hide that until the user ran /quota and saw nothing.
func zaiPollDecision(outcome *zaiPollOutcome) (bool, error) {
	switch outcome.status {
	case "pending":
		return false, nil
	case "failed":
		return false, fmt.Errorf("z.ai oauth: authorization failed in the browser")
	case "ready":
		if outcome.OAuthToken == "" {
			return false, fmt.Errorf("z.ai oauth: ready response missing the family access token")
		}
		if outcome.ZcodeJWT == "" {
			return false, fmt.Errorf("z.ai oauth: ready response missing the zcode JWT")
		}
		return true, nil
	default:
		return false, fmt.Errorf("z.ai oauth: unknown poll status %q", outcome.status)
	}
}

// flowExpired reports whether the broker's authorizable window has closed. The
// deadline comes from the INIT payload (expires_at) and is distinct from the
// issued token's own lifetime (expires_in on the ready payload) — conflating
// them would either abandon a live flow or keep polling a dead one.
func flowExpired(init *zaiPendingFlow) bool {
	return init != nil && init.ExpiresAt > 0 && time.Now().After(time.Unix(init.ExpiresAt, 0))
}

// ctxErr maps a finished context onto a message the user can act on: a
// deadline means they took too long, anything else means they cancelled.
func ctxErr(ctx context.Context) error {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return fmt.Errorf("z.ai oauth: timed out waiting for browser authorization")
	case ctx.Err() != nil:
		return fmt.Errorf("z.ai oauth: cancelled")
	default:
		return nil
	}
}

// zaiPollOutcome is one poll answer, normalized across families.
type zaiPollOutcome struct {
	status     string
	ZcodeJWT   string
	OAuthToken string
	UserID     string
	UserName   string
	// ExpiresIn is the issued token's lifetime in seconds (0 when the broker
	// sends none). Distinct from the init payload's flow ExpiresAt.
	ExpiresIn int64
}

func zaiPollOnce(ctx context.Context, cfg *zaiFlowConfig, flowID, pollToken string) (*zaiPollOutcome, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", cfg.brokerURL()+"/oauth/cli/poll/"+url.PathEscape(flowID), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+pollToken)

	data, err := zaiRequest(ctx, cfg, req, "z.ai oauth poll")
	if err != nil {
		return nil, err
	}
	var env struct {
		Data struct {
			Status string `json:"status"`
			Token  string `json:"token"`
			User   struct {
				UserID string `json:"user_id"`
				Name   string `json:"name"`
				Email  string `json:"email"`
			} `json:"user"`
			// The family access token nests under the provider id
			// ("zai" / "bigmodel"), so the decoder captures whichever family the
			// flow was opened with.
			Zai struct {
				AccessToken string `json:"access_token"`
			} `json:"zai"`
			BigModel struct {
				AccessToken string `json:"access_token"`
			} `json:"bigmodel"`
			RefreshToken string `json:"refresh_token"`
			ExpiresIn    int64  `json:"expires_in"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("z.ai oauth poll: parse response: %w", err)
	}
	out := &zaiPollOutcome{
		status:    env.Data.Status,
		ZcodeJWT:  env.Data.Token,
		UserID:    env.Data.User.UserID,
		UserName:  env.Data.User.Name,
		ExpiresIn: env.Data.ExpiresIn,
	}
	if out.UserName == "" {
		out.UserName = env.Data.User.Email
	}
	if env.Data.Zai.AccessToken != "" {
		out.OAuthToken = env.Data.Zai.AccessToken
	} else {
		out.OAuthToken = env.Data.BigModel.AccessToken
	}
	return out, nil
}

// zaiExchangeBusinessToken trades the OAuth access token for the business token
// the business APIs accept. Every failure collapses to ErrZaiOAuthRequired: the
// caller must not be able to mistake a denied exchange for a usable login.
func zaiExchangeBusinessToken(ctx context.Context, cfg *zaiFlowConfig, family zaiFamily, oauthToken string) (string, error) {
	if strings.TrimSpace(oauthToken) == "" {
		return "", ErrZaiOAuthRequired
	}
	body, _ := json.Marshal(map[string]string{"token": oauthToken})
	req, err := http.NewRequestWithContext(ctx, "POST", cfg.businessURL(family), strings.NewReader(string(body)))
	if err != nil {
		return "", ErrZaiOAuthRequired
	}
	req.Header.Set("Content-Type", "application/json")

	data, err := zaiRequest(ctx, cfg, req, "z.ai business login")
	if err != nil {
		return "", ErrZaiOAuthRequired
	}
	var env struct {
		Code    *int   `json:"code"`
		Success *bool  `json:"success"`
		Msg     string `json:"msg"`
		Data    struct {
			AccessToken string `json:"access_token"`
			CamelToken  string `json:"accessToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return "", ErrZaiOAuthRequired
	}
	if !zaiBusinessCodeOK(env.Code) || (env.Success != nil && !*env.Success) {
		return "", ErrZaiOAuthRequired
	}
	token := strings.TrimSpace(env.Data.AccessToken)
	if token == "" {
		token = strings.TrimSpace(env.Data.CamelToken)
	}
	if token == "" {
		return "", ErrZaiOAuthRequired
	}
	return token, nil
}

// zaiBusinessCodeOK accepts the business envelope's success convention. The
// endpoint is inconsistent about sending `code` at all, so ABSENT is success
// and only an explicit non-zero (or false) is failure.
func zaiBusinessCodeOK(code *int) bool {
	if code == nil {
		return true
	}
	return *code == 0 || *code == 200
}

// zaiRequest performs one JSON request and unwraps the {code,msg,data}
// envelope, surfacing the server's message on a business error.
func zaiRequest(ctx context.Context, cfg *zaiFlowConfig, req *http.Request, what string) ([]byte, error) {
	resp, err := cfg.httpDoer()(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%s: cancelled", what)
		}
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, zaiResponseLimit))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s: HTTP %d: %s", what, resp.StatusCode, zaiServerMessage(raw))
	}
	var env struct {
		Code *int   `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("%s: invalid response envelope", what)
	}
	if env.Code != nil && *env.Code != 0 {
		msg := strings.TrimSpace(env.Msg)
		if msg == "" {
			msg = fmt.Sprintf("business error %d", *env.Code)
		}
		return nil, fmt.Errorf("%s: %s", what, msg)
	}
	return raw, nil
}

// zaiServerMessage extracts a human message from an error body, preferring the
// envelope's msg so the user sees the server's own words.
func zaiServerMessage(raw []byte) string {
	var env struct {
		Msg     string `json:"msg"`
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err == nil {
		for _, candidate := range []string{env.Msg, env.Message, env.Error} {
			if s := strings.TrimSpace(candidate); s != "" {
				return s
			}
		}
	}
	if s := strings.TrimSpace(string(raw)); s != "" {
		if len(s) > 200 {
			s = s[:200] + "…"
		}
		return s
	}
	return "no response body"
}

// zaiPollInterval resolves the wait between polls: the broker's advice, floored
// so a missing or absurd value cannot become a hot loop.
func zaiPollInterval(cfg *zaiFlowConfig, init *zaiPendingFlow) time.Duration {
	if cfg != nil && cfg.pollInterval > 0 {
		return cfg.pollInterval
	}
	if init != nil && init.PollInterval >= 1 {
		advised := time.Duration(init.PollInterval * float64(time.Second))
		if advised > zaiMinPollInterval {
			return advised
		}
	}
	return zaiMinPollInterval
}

// zaiPollToken mints the client-side correlation token the broker authenticates
// init/poll with. 32 random bytes, hex — high entropy because it is the only
// thing binding a poll response to the flow that requested it.
func zaiPollToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate oauth poll token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// envOrDefault reads an env override, falling back to def when unset/blank.
func envOrDefault(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}
