// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// --- test doubles -----------------------------------------------------------

// zaiStub is an httptest server standing in for the three z.ai/zcode endpoints
// the login flow talks to: the CLI OAuth broker, the Z.AI business login, and
// (for the credential-shape test) nothing else.
type zaiStub struct {
	broker   *httptest.Server
	business *httptest.Server

	// initCalls counts POST /oauth/cli/init requests.
	initCalls int
	// pollCalls counts GET /oauth/cli/poll/{flowID} requests.
	pollCalls int
	// businessCalls counts POST /api/auth/z/login requests.
	businessCalls int

	// pollTokenSeen is the bearer token the broker saw on init/poll.
	pollTokenSeen string
	// businessTokenSeen is the "token" field the business login received.
	businessTokenSeen string
	// providerSeen is the provider discriminator the broker received.
	providerSeen string

	// readyAfter is how many pending polls precede the ready answer.
	readyAfter int
	// expiresIn is the expires_in the ready answer carries (0 = omit).
	expiresIn int
	// businessCode forces a non-zero business code on the business login.
	businessCode int
}

func newZaiStub(t *testing.T) *zaiStub {
	t.Helper()
	s := &zaiStub{readyAfter: 1}
	s.broker = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeZaiJSON(w, http.StatusOK, map[string]any{"code": 0, "msg": "ok"})
	}))
	s.business = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeZaiJSON(w, http.StatusOK, map[string]any{"code": 0, "data": map[string]any{}})
	}))
	t.Cleanup(s.broker.Close)
	t.Cleanup(s.business.Close)
	return s
}

// serveBroker routes /oauth/cli/init and /oauth/cli/poll/{id} with a working
// init + ready-after-N-polls exchange.
func (s *zaiStub) serveBroker() {
	s.broker.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.pollTokenSeen = r.Header.Get("Authorization")
		switch {
		case r.URL.Path == "/oauth/cli/init":
			s.initCalls++
			var body struct {
				Provider string `json:"provider"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			s.providerSeen = body.Provider
			writeZaiJSON(w, http.StatusOK, map[string]any{
				"code": 0,
				"data": map[string]any{
					"authorize_url":     "https://chat.z.ai/api/oauth/authorize?state=xyz",
					"expires_at":        time.Now().Add(5 * time.Minute).Unix(),
					"flow_id":           "flow-1",
					"poll_interval_sec": 1,
				},
			})
		case strings.HasPrefix(r.URL.Path, "/oauth/cli/poll/"):
			s.pollCalls++
			if s.pollCalls <= s.readyAfter {
				writeZaiJSON(w, http.StatusOK, map[string]any{
					"code": 0, "data": map[string]any{"status": "pending"},
				})
				return
			}
			data := map[string]any{
				"status":      "ready",
				"token":       "zcode-jwt-1",
				"user":        map[string]any{"user_id": "u-1", "name": "Tester"},
				s.familyKey(): map[string]any{"access_token": "oauth-access-1"},
			}
			if s.expiresIn > 0 {
				data["expires_in"] = s.expiresIn
			}
			writeZaiJSON(w, http.StatusOK, map[string]any{"code": 0, "data": data})
		default:
			writeZaiJSON(w, http.StatusNotFound, map[string]any{"code": 404, "msg": "no route"})
		}
	})
}

// familyKey is the provider discriminator this stub answers for.
func (s *zaiStub) familyKey() string { return "zai" }

// serveBusiness routes the Z.AI business login to a valid access_token.
func (s *zaiStub) serveBusiness() {
	s.business.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.businessCalls++
		var body struct {
			Token string `json:"token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.businessTokenSeen = body.Token
		if s.businessCode != 0 {
			writeZaiJSON(w, http.StatusOK, map[string]any{
				"code": s.businessCode, "msg": "denied", "success": false,
			})
			return
		}
		writeZaiJSON(w, http.StatusOK, map[string]any{
			"code":    0,
			"success": true,
			"data":    map[string]any{"access_token": "business-token-1", "expires_in": 3600},
		})
	})
}

// config returns a flow config pointed at the stubs. The z.ai CLI flow polls
// rather than receiving a browser redirect, so there is no local listener to
// configure.
func (s *zaiStub) config() *zaiFlowConfig {
	return &zaiFlowConfig{
		doer:             http.DefaultClient.Do,
		brokerBaseURL:    s.broker.URL,
		businessLoginURL: s.business.URL + "/api/auth/z/login",
		pollInterval:     time.Millisecond,
		loginTimeout:     5 * time.Second,
	}
}

func writeZaiJSON(w http.ResponseWriter, status int, body map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// --- flow tests -------------------------------------------------------------

// TestZaiLogin_ReturnsBothCredentials is the load-bearing assertion: one login
// must yield BOTH credentials the Coding Plan reset API needs — the zcode
// business JWT (Authorization) and the family business token
// (X-Bigmodel-Authorization). Before this flow existed, a goa user had no way
// to obtain either, so the reset surface could never authenticate.
func TestZaiLogin_ReturnsBothCredentials(t *testing.T) {
	s := newZaiStub(t)
	s.serveBroker()
	s.serveBusiness()

	cfg := s.config()
	cfg.notifyURL = func(string) {}
	tokens, err := loginZai(ctxBackground(), cfg)
	if err != nil {
		t.Fatalf("loginZai: %v", err)
	}
	if tokens.AccessToken != "business-token-1" {
		t.Errorf("AccessToken = %q, want the Z.AI business token %q", tokens.AccessToken, "business-token-1")
	}
	if tokens.ZcodeJWT != "zcode-jwt-1" {
		t.Errorf("ZcodeJWT = %q, want %q from data.token", tokens.ZcodeJWT, "zcode-jwt-1")
	}
	if tokens.AccountID != "u-1" {
		t.Errorf("AccountID = %q, want %q from user.user_id", tokens.AccountID, "u-1")
	}
}

// TestZaiLogin_PollsUntilReady pins the device-code-style contract: the flow
// must poll (pending → ready) rather than assume a single answer.
func TestZaiLogin_PollsUntilReady(t *testing.T) {
	s := newZaiStub(t)
	s.serveBroker()
	s.serveBusiness()
	s.readyAfter = 3

	cfg := s.config()
	cfg.notifyURL = func(string) {}
	if _, err := loginZai(ctxBackground(), cfg); err != nil {
		t.Fatalf("loginZai: %v", err)
	}
	if s.initCalls != 1 {
		t.Errorf("init calls = %d, want exactly 1", s.initCalls)
	}
	if s.pollCalls < 4 {
		t.Errorf("poll calls = %d, want >= 4 (3 pending + 1 ready)", s.pollCalls)
	}
}

// TestZaiLogin_SendsPollTokenAndProvider pins the broker contract: the poll
// token is a Bearer on every call and the family discriminator is explicit.
func TestZaiLogin_SendsPollTokenAndProvider(t *testing.T) {
	s := newZaiStub(t)
	s.serveBroker()
	s.serveBusiness()

	cfg := s.config()
	cfg.pollToken = "poll-token-abc"
	cfg.notifyURL = func(string) {}
	if _, err := loginZai(ctxBackground(), cfg); err != nil {
		t.Fatalf("loginZai: %v", err)
	}
	if !strings.Contains(s.pollTokenSeen, "poll-token-abc") {
		t.Errorf("broker Authorization = %q, want the poll token as Bearer", s.pollTokenSeen)
	}
	if s.providerSeen != "zai" {
		t.Errorf("provider discriminator = %q, want %q", s.providerSeen, "zai")
	}
}

// TestZaiLogin_BusinessLoginReceivesOAuthAccessToken pins that the business
// token is minted from the OAuth access token — NOT from the zcode JWT. The
// two are different identities; sending the wrong one is what the reset API
// rejects.
func TestZaiLogin_BusinessLoginReceivesOAuthAccessToken(t *testing.T) {
	s := newZaiStub(t)
	s.serveBroker()
	s.serveBusiness()

	cfg := s.config()
	cfg.notifyURL = func(string) {}
	if _, err := loginZai(ctxBackground(), cfg); err != nil {
		t.Fatalf("loginZai: %v", err)
	}
	if s.businessTokenSeen != "oauth-access-1" {
		t.Errorf("business login token = %q, want the OAuth access token %q", s.businessTokenSeen, "oauth-access-1")
	}
	if s.businessCalls != 1 {
		t.Errorf("business login calls = %d, want exactly 1", s.businessCalls)
	}
}

// TestZaiLogin_BusinessLoginDeniedFails pins that a rejected business exchange
// is a hard failure: the flow must never report success while holding an
// OAuth token that the business APIs reject.
func TestZaiLogin_BusinessLoginDeniedFails(t *testing.T) {
	s := newZaiStub(t)
	s.serveBroker()
	s.serveBusiness()
	s.businessCode = 4001

	cfg := s.config()
	cfg.notifyURL = func(string) {}
	tokens, err := loginZai(ctxBackground(), cfg)
	if err == nil {
		t.Fatalf("loginZai succeeded with a denied business exchange: %+v", tokens)
	}
	if !strings.Contains(err.Error(), "zai_oauth_required") {
		t.Errorf("error = %v, want the zai_oauth_required sentinel", err)
	}
}

// TestZaiLogin_ExpiresInCarried pins the expiry mapping: expires_in is seconds
// from the broker, stored as an absolute time so IsExpired works.
func TestZaiLogin_ExpiresInCarried(t *testing.T) {
	s := newZaiStub(t)
	s.serveBroker()
	s.serveBusiness()
	s.expiresIn = 7200

	cfg := s.config()
	cfg.notifyURL = func(string) {}
	tokens, err := loginZai(ctxBackground(), cfg)
	if err != nil {
		t.Fatalf("loginZai: %v", err)
	}
	if tokens.ExpiresAt.IsZero() {
		t.Fatal("ExpiresAt is zero, want the broker expires_in mapped")
	}
	if d := time.Until(tokens.ExpiresAt); d < time.Hour || d > 2*time.Hour+time.Minute {
		t.Errorf("ExpiresAt in %v, want ~2h", d)
	}
}

// TestZaiLogin_NotifyURLCarriesAuthorizeLink verifies the browser handoff: the
// flow must surface the authorize_url the broker issued (that is the only URL
// the user can actually open).
func TestZaiLogin_NotifyURLCarriesAuthorizeLink(t *testing.T) {
	s := newZaiStub(t)
	s.serveBroker()
	s.serveBusiness()

	var got string
	cfg := s.config()
	cfg.notifyURL = func(u string) { got = u }
	if _, err := loginZai(ctxBackground(), cfg); err != nil {
		t.Fatalf("loginZai: %v", err)
	}
	if !strings.Contains(got, "chat.z.ai/api/oauth/authorize") {
		t.Errorf("notify URL = %q, want the broker authorize_url", got)
	}
}

// TestZaiLogin_PollFailureSurfacesBrokerMessage pins error fidelity: a business
// error code from the broker must surface its message, not a bare "failed".
func TestZaiLogin_PollFailureSurfacesBrokerMessage(t *testing.T) {
	s := newZaiStub(t)
	s.serveBroker()
	s.serveBusiness()
	// Re-route the broker to a business error on init.
	s.broker.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeZaiJSON(w, http.StatusOK, map[string]any{"code": 2007, "msg": "login expired"})
	})

	cfg := s.config()
	cfg.notifyURL = func(string) {}
	_, err := loginZai(ctxBackground(), cfg)
	if err == nil {
		t.Fatal("loginZai succeeded against a failing broker")
	}
	if !strings.Contains(err.Error(), "login expired") {
		t.Errorf("error = %v, want the broker message surfaced", err)
	}
}

// TestZaiLogin_ContextCancelStops pins that a cancelled login returns promptly
// instead of polling until the flow timeout.
func TestZaiLogin_ContextCancelStops(t *testing.T) {
	s := newZaiStub(t)
	// Always pending.
	s.broker.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/cli/init" {
			writeZaiJSON(w, http.StatusOK, map[string]any{
				"code": 0,
				"data": map[string]any{
					"authorize_url": "https://chat.z.ai/api/oauth/authorize",
					"expires_at":    time.Now().Add(time.Minute).Unix(),
					"flow_id":       "flow-1", "poll_interval_sec": 60,
				},
			})
			return
		}
		writeZaiJSON(w, http.StatusOK, map[string]any{
			"code": 0, "data": map[string]any{"status": "pending"},
		})
	})

	ctx, cancel := context.WithTimeout(ctxBackground(), 150*time.Millisecond)
	defer cancel()
	cfg := s.config()
	cfg.loginTimeout = 5 * time.Second
	cfg.notifyURL = func(string) {}
	start := time.Now()
	if _, err := loginZai(ctx, cfg); err == nil {
		t.Fatal("loginZai succeeded against a permanently-pending broker")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("loginZai took %v to notice cancellation, want prompt return", elapsed)
	}
}

func ctxBackground() context.Context { return context.Background() }

// --- credential plumbing ----------------------------------------------------

// TestTokens_ZcodeJOURoundTrips pins that the zcode JWT survives the auth
// store's JSON round-trip. Without this the JWT would be lost on restart and
// the reset surface would silently degrade.
func TestTokens_ZcodeJOURoundTrips(t *testing.T) {
	raw := `{"access_token":"biz","zcode_jwt":"jwt-1","account_id":"u-1"}`
	var tokens Tokens
	if err := json.Unmarshal([]byte(raw), &tokens); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if tokens.ZcodeJWT != "jwt-1" {
		t.Fatalf("ZcodeJWT = %q, want %q", tokens.ZcodeJWT, "jwt-1")
	}
	out, err := json.Marshal(&tokens)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), `"zcode_jwt":"jwt-1"`) {
		t.Errorf("re-marshalled = %s, want the zcode_jwt field preserved", out)
	}
}

// --- poll state machine ------------------------------------------------------

// TestZaiPollDecision covers the poll state machine's non-ready transitions:
// pending keeps waiting, and the other answers either complete or name what is
// wrong. The ready-path validations have their own test below.
func TestZaiPollDecision(t *testing.T) {
	for _, tc := range []struct {
		name     string
		outcome  *zaiPollOutcome
		wantErr  string
		wantDone bool
	}{
		{name: "pending keeps waiting", outcome: &zaiPollOutcome{status: "pending"}},
		{name: "ready completes", outcome: &zaiPollOutcome{status: "ready", OAuthToken: "tok", ZcodeJWT: "jwt"}, wantDone: true},
		{name: "browser rejection", outcome: &zaiPollOutcome{status: "failed"}, wantErr: "failed in the browser"},
		{name: "unknown status", outcome: &zaiPollOutcome{status: "wat"}, wantErr: `unknown poll status "wat"`},
	} {
		t.Run(tc.name, func(t *testing.T) { assertPollDecision(t, tc.outcome, tc.wantDone, tc.wantErr) })
	}
}

// assertPollDecision checks one poll answer against its expectation, keeping the
// table loop free of branching so the case matrix stays readable.
func assertPollDecision(t *testing.T, outcome *zaiPollOutcome, wantDone bool, wantErr string) {
	t.Helper()
	done, err := zaiPollDecision(outcome)
	if wantErr != "" {
		if err == nil || !strings.Contains(err.Error(), wantErr) {
			t.Fatalf("error = %v, want it to mention %q", err, wantErr)
		}
		return
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if done != wantDone {
		t.Errorf("done = %v, want %v", done, wantDone)
	}
}

// TestZaiPollDecision_ReadyNeedsBothCredentials pins that a ready answer
// missing EITHER half is rejected at login. A half-usable credential would
// surface much later as an unexplained /quota failure, long after the login
// reported success.
func TestZaiPollDecision_ReadyNeedsBothCredentials(t *testing.T) {
	for _, tc := range []struct {
		name    string
		outcome *zaiPollOutcome
		wantErr string
	}{
		{
			name:    "no access token",
			outcome: &zaiPollOutcome{status: "ready", ZcodeJWT: "jwt"},
			wantErr: "missing the family access token",
		},
		{
			name:    "no zcode jwt",
			outcome: &zaiPollOutcome{status: "ready", OAuthToken: "tok"},
			wantErr: "missing the zcode JWT",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			done, err := zaiPollDecision(tc.outcome)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.wantErr)
			}
			if done {
				t.Error("done = true on an incomplete credential")
			}
		})
	}
}

// TestFlowExpired_DistinguishesDeadlineFromLifetime pins the two clocks apart.
// expires_at (init) is the authorizable window; expires_in (ready) is the token
// lifetime. Treating the flow deadline as the token expiry made a fresh token
// look expired whenever the user took their time in the browser.
func TestFlowExpired_DistinguishesDeadlineFromLifetime(t *testing.T) {
	live := &zaiPendingFlow{ExpiresAt: time.Now().Add(time.Minute).Unix()}
	if flowExpired(live) {
		t.Error("a live flow must not read as expired")
	}
	dead := &zaiPendingFlow{ExpiresAt: time.Now().Add(-time.Second).Unix()}
	if !flowExpired(dead) {
		t.Error("a past expires_at must read as expired")
	}
	if flowExpired(&zaiPendingFlow{}) {
		t.Error("a flow with no deadline must never expire on its own")
	}
	if flowExpired(nil) {
		t.Error("a nil flow must not panic or expire")
	}
}

// TestZaiPollInterval_FloorsBrokerAdvice pins that a broker advising 0 (or
// sending nothing usable) cannot turn the poll into a hot loop.
func TestZaiPollInterval_FloorsBrokerAdvice(t *testing.T) {
	for _, advised := range []float64{0, -1, 0.4} {
		got := zaiPollInterval(nil, &zaiPendingFlow{PollInterval: advised})
		if got < zaiMinPollInterval {
			t.Errorf("advised %v → %v, want >= %v", advised, got, zaiMinPollInterval)
		}
	}
	// A longer advice is honored (the broker knows its own rate limits).
	long := zaiPollInterval(nil, &zaiPendingFlow{PollInterval: 5})
	if long != 5*time.Second {
		t.Errorf("advised 5s → %v, want 5s", long)
	}
}
