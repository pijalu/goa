// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package oauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

const (
	zaiTestPollToken    = "poll-token-abc"
	zaiTestOAuthToken   = "oauth-access-1"
	zaiTestZcodeJWT     = "zcode-jwt-1"
	zaiTestAuthorizeURL = "https://chat.z.ai/api/oauth/authorize?state=xyz"
)

// TestZaiLoginFailureDiagnostics replays terminal answers at each network
// boundary. Every public-flow failure must retain its actionable stage and
// cause without sacrificing the sentinel used by credential-resolution code.
func TestZaiLoginFailureDiagnostics(t *testing.T) {
	tests := []struct {
		name    string
		stage   string
		family  zaiFamily
		status  int
		body    string
		want    []string
		secrets []string
	}{
		{
			name: "zai broker init denied", stage: "init", family: zaiFamilyZai,
			body: `{"code":2007,"msg":"broker denied poll-token-abc"}`,
			want: []string{"broker initialization", "broker denied"}, secrets: []string{zaiTestPollToken},
		},
		{
			name: "broker init malformed", stage: "init", family: zaiFamilyZai,
			body: `{"code":`, want: []string{"broker initialization", "invalid response envelope"},
		},
		{
			name: "broker init HTTP failure", stage: "init", family: zaiFamilyZai, status: http.StatusBadGateway,
			body: `{"message":"broker temporarily unavailable"}`,
			want: []string{"broker initialization", "HTTP 502", "temporarily unavailable"},
		},
		{
			name: "broker poll denied", stage: "poll", family: zaiFamilyZai,
			body: `{"code":2007,"msg":"authorization session revoked"}`,
			want: []string{"authorization polling", "session revoked"},
		},
		{
			name: "browser authorization denied", stage: "poll", family: zaiFamilyZai,
			body: `{"code":0,"data":{"status":"failed"}}`,
			want: []string{"authorization polling", "authorization failed in the browser"},
		},
		{
			name: "broker poll malformed", stage: "poll", family: zaiFamilyZai,
			body: `{"code":`, want: []string{"authorization polling", "invalid response envelope"},
		},
		{
			name: "ready response missing OAuth token", stage: "poll", family: zaiFamilyZai,
			body: `{"code":0,"data":{"status":"ready","token":"zcode-jwt-1"}}`,
			want: []string{"authorization polling", "missing the family access token"},
		},
		{
			name: "ready response missing zcode JWT", stage: "poll", family: zaiFamilyZai,
			body: `{"code":0,"data":{"status":"ready","zai":{"access_token":"oauth-access-1"}}}`,
			want: []string{"authorization polling", "missing the zcode JWT"},
		},
		{
			name: "business exchange denied", stage: "business", family: zaiFamilyZai,
			body: `{"code":4001,"msg":"OAuth grant rejected"}`,
			want: []string{"business token exchange", "OAuth grant rejected"},
		},
		{
			name: "business exchange explicitly unsuccessful", stage: "business", family: zaiFamilyZai,
			body: `{"code":0,"success":false,"msg":"OAuth grant revoked"}`,
			want: []string{"business token exchange", "OAuth grant revoked"},
		},
		{
			name: "business exchange malformed", stage: "business", family: zaiFamilyZai,
			body: `{"code":`, want: []string{"business token exchange", "invalid response envelope"},
		},
		{
			name: "business exchange missing token", stage: "business", family: zaiFamilyZai,
			body: `{"code":0,"success":true,"data":{}}`,
			want: []string{"business token exchange", "missing the business access token"},
		},
		{
			name: "BigModel business exchange denied", stage: "business", family: zaiFamilyBigModel,
			body: `{"code":4001,"msg":"BigModel OAuth grant rejected"}`,
			want: []string{"business token exchange", "BigModel OAuth grant rejected"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newZaiStub(t)
			s.serveBroker()
			s.serveBusiness()
			installZaiFailure(t, s, tc.stage, tc.status, tc.body)
			cfg := s.config()
			cfg.pollToken = zaiTestPollToken
			cfg.notifyURL = func(string) {}

			tokens, err := loginZaiFamily(context.Background(), tc.family, cfg)
			if err == nil {
				t.Fatalf("loginZaiFamily succeeded, want failure; tokens = %+v", tokens)
			}
			assertZaiOAuthDiagnostic(t, err, tc.want, tc.secrets)
		})
	}
}

// installZaiFailure replaces exactly one stage with a terminal response while
// leaving the preceding stages on the normal httptest flow.
func installZaiFailure(t *testing.T, s *zaiStub, stage string, status int, body string) {
	t.Helper()
	if status == 0 {
		status = http.StatusOK
	}
	respond := func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
	switch stage {
	case "init":
		s.broker.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { respond(w) })
	case "poll":
		broker := s.broker.Config.Handler
		s.broker.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/oauth/cli/poll/") {
				respond(w)
				return
			}
			broker.ServeHTTP(w, r)
		})
	case "business":
		s.business.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { respond(w) })
	default:
		t.Fatalf("unknown failure stage %q", stage)
	}
}

func TestZaiLoginTransportFailureDiagnostics(t *testing.T) {
	for _, stage := range []string{"init", "poll", "business"} {
		t.Run(stage, func(t *testing.T) {
			s := newZaiStub(t)
			s.serveBroker()
			s.serveBusiness()
			cfg := s.config()
			cfg.pollToken = zaiTestPollToken
			cfg.notifyURL = func(string) {}
			secret := zaiStageSecret(stage)
			cfg.doer = func(req *http.Request) (*http.Response, error) {
				if zaiRequestStage(req.URL.Path) == stage {
					return nil, fmt.Errorf("dial https://user:url-password@example.com/login?token=url-secret: connection refused; credential %s", secret)
				}
				return http.DefaultClient.Do(req)
			}

			_, err := loginZai(context.Background(), cfg)
			if err == nil {
				t.Fatal("loginZai succeeded despite an injected transport failure")
			}
			assertZaiOAuthDiagnostic(t, err,
				[]string{zaiStageName(stage), "connection refused"},
				[]string{secret, "url-password", "url-secret"},
			)
		})
	}
}

func zaiRequestStage(path string) string {
	switch {
	case strings.HasSuffix(path, "/oauth/cli/init"):
		return "init"
	case strings.Contains(path, "/oauth/cli/poll/"):
		return "poll"
	case strings.HasSuffix(path, "/api/auth/z/login"):
		return "business"
	default:
		return ""
	}
}

func zaiStageSecret(stage string) string {
	switch stage {
	case "business":
		return zaiTestOAuthToken + " " + zaiTestZcodeJWT
	default:
		return zaiTestPollToken
	}
}

func zaiStageName(stage string) string {
	switch stage {
	case "init":
		return "broker initialization"
	case "poll":
		return "authorization polling"
	default:
		return "business token exchange"
	}
}

func TestZaiLoginBusinessFailureRedactsFlowSecrets(t *testing.T) {
	s := newZaiStub(t)
	s.serveBroker()
	s.serveBusiness()
	authorizeURLWithoutQuery := strings.SplitN(zaiTestAuthorizeURL, "?", 2)[0]
	s.business.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeZaiJSON(w, http.StatusOK, map[string]any{
			"code": 4001,
			"msg": "denied " + zaiTestPollToken + " " + zaiTestOAuthToken + " " +
				zaiTestZcodeJWT + " " + zaiTestAuthorizeURL + " " + authorizeURLWithoutQuery + "\x1b[2J\r\n",
		})
	})
	cfg := s.config()
	cfg.pollToken = zaiTestPollToken
	cfg.notifyURL = func(string) {}

	_, err := loginZai(context.Background(), cfg)
	if err == nil {
		t.Fatal("loginZai succeeded despite a denied business exchange")
	}
	assertZaiOAuthDiagnostic(t, err,
		[]string{"business token exchange", "denied", "[redacted]"},
		[]string{zaiTestPollToken, zaiTestOAuthToken, zaiTestZcodeJWT, zaiTestAuthorizeURL, "state=xyz"},
	)
	if strings.ContainsAny(err.Error(), "\x1b\r\n") {
		t.Errorf("error contains terminal control characters: %q", err)
	}
}

func TestZaiLoginCancellationRetainsDiagnosticAndSentinel(t *testing.T) {
	s := newZaiStub(t)
	s.serveBroker()
	s.serveBusiness()
	cfg := s.config()
	cfg.notifyURL = func(string) {}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := loginZai(ctx, cfg)
	if err == nil {
		t.Fatal("loginZai succeeded with an already-cancelled context")
	}
	assertZaiOAuthDiagnostic(t, err, []string{"broker initialization", "cancelled"}, nil)
}

func TestBigModelLoginReturnsBothCredentials(t *testing.T) {
	s := newZaiStub(t)
	s.family = "bigmodel"
	s.serveBroker()
	s.serveBusiness()
	cfg := s.config()
	cfg.notifyURL = func(string) {}

	tokens, err := loginZaiFamily(context.Background(), zaiFamilyBigModel, cfg)
	if err != nil {
		t.Fatalf("loginZaiFamily(bigmodel): %v", err)
	}
	if tokens.AccessToken != "business-token-1" || tokens.ZcodeJWT != zaiTestZcodeJWT {
		t.Errorf("tokens = %+v, want both BigModel business token and zcode JWT", tokens)
	}
	if s.providerSeen != "bigmodel" {
		t.Errorf("provider discriminator = %q, want bigmodel", s.providerSeen)
	}
}

func assertZaiOAuthDiagnostic(t *testing.T, err error, wants, secrets []string) {
	t.Helper()
	if !errors.Is(err, ErrZaiOAuthRequired) {
		t.Errorf("errors.Is(%v, ErrZaiOAuthRequired) = false", err)
	}
	got := err.Error()
	if got == ErrZaiOAuthRequired.Error() {
		t.Errorf("error exposes only the internal sentinel: %q", got)
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("error = %q, want actionable text %q", got, want)
		}
	}
	for _, secret := range secrets {
		if secret != "" && strings.Contains(got, secret) {
			t.Errorf("error contains secret %q: %q", secret, got)
		}
	}
}
