// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/core"
	oauth "github.com/pijalu/goa/internal/agentic/provider/oauth"
	"github.com/pijalu/goa/tui"
)

const (
	zaiFilmOAuthToken = "visible-oauth-token-123456"
	zaiFilmZcodeJWT   = "visible-zcode-jwt-123456"
)

// zaiFilmstripFlow uses the production OAuth implementation and URL writer but
// leaves OpenURL nil so the test never launches a real browser.
type zaiFilmstripFlow struct{}

func (*zaiFilmstripFlow) Run(ctx context.Context, w uiWriter, _ prompter) (*oauth.Tokens, error) {
	return oauth.LoginZaiUI(ctx, oauth.ZaiUIOpts{
		NotifyURL: func(u string) { w.Writef("Open this URL to sign in:\n%s\n", u) },
	})
}

// TestLoginZaiOAuthURL_LongBrokerURLFilmstrip drives the production z.ai URL
// writer with the live broker's approximately 200-column authorization URL.
// Short fixture URLs fit the panel and miss the truncation/wrapping path that
// made the reported URL row blank.
func TestLoginZaiOAuthURL_LongBrokerURLFilmstrip(t *testing.T) {
	h := newLoginLoopHarness(t)
	const authorizeURL = "https://chat.z.ai/api/oauth/authorize?client_id=client_P8X5CMWmlaRO9gyO-KSqtg&redirect_uri=zcode%3A%2F%2Foauth%2Fcallback%3Fflow_id%3Dflow-visible&state=0123456789abcdef0123456789abcdef&response_type=code"
	if len(authorizeURL) <= h.term.w-4 {
		t.Fatalf("fixture URL length = %d, want > panel inner width %d", len(authorizeURL), h.term.w-4)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	flow := &zaiBrowserFlow{
		loginUI: func(ctx context.Context, opts oauth.ZaiUIOpts) (*oauth.Tokens, error) {
			opts.NotifyURL(authorizeURL)
			close(started)
			select {
			case <-release:
				return &oauth.Tokens{AccessToken: "business-token", ZcodeJWT: "zcode-jwt"}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	}
	cmd := &LoginCommand{Store: h.store, flowFactory: func(string) oauthFlow { return flow }}
	runDone := make(chan error, 1)
	go func() { runDone <- cmd.Run(h.ctx, []string{"zai", "oauth"}) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("z.ai URL notification did not start")
	}

	waitForFrame(t, h.engine, "Waiting for authorization...")
	h.engine.RenderNow()
	frame := h.engine.AgentFrame()
	film := tui.NewFilmstrip()
	film.Capture("long-authorization-url", frame, "")
	visible := strings.Join(frame.Visible, "")
	visible = strings.NewReplacer("│", "", "╭", "", "╮", "", "╰", "", "╯", "", "─", "").Replace(visible)
	visible = strings.Join(strings.Fields(visible), "")
	if !strings.Contains(visible, authorizeURL) {
		t.Fatalf("complete authorization URL missing from visible terminal:\n%s", film.Render())
	}

	unblock()
	if err := <-runDone; err != nil {
		t.Fatalf("LoginCommand.Run: %v", err)
	}
	waitForCondition(t, func() bool { return h.store.HasAuth("zai") })
	t.Logf("z.ai long authorization URL filmstrip:\n%s", film.Render())
}

// TestLoginZaiOAuthBusinessCode200_Filmstrip proves the complete login command
// succeeds with the reported business envelope, persists both credentials, and
// never renders "Operation successful" as a failure flash.
func TestLoginZaiOAuthBusinessCode200_Filmstrip(t *testing.T) {
	h := newLoginLoopHarness(t)
	server := newZaiBusinessCode200Server(t)
	t.Setenv("GOA_ZCODE_OAUTH_URL", server.URL)
	t.Setenv("GOA_ZAI_BUSINESS_LOGIN_URL", server.URL+"/api/auth/z/login")
	cmd := &LoginCommand{
		Store:       h.store,
		flowFactory: func(string) oauthFlow { return &zaiFilmstripFlow{} },
	}
	runDone := make(chan error, 1)
	go func() { runDone <- cmd.Run(h.ctx, []string{"zai", "oauth"}) }()
	if err := <-runDone; err != nil {
		t.Fatalf("LoginCommand.Run: %v", err)
	}

	waitForCondition(t, func() bool { return h.store.HasAuth("zai") })
	credential, ok := h.store.GetOAuth("zai")
	if !ok {
		t.Fatal("successful business code 200 login stored no z.ai credential")
	}
	if credential.AccessToken != "business-token-200" || credential.ZcodeJWT != zaiFilmZcodeJWT {
		t.Errorf("stored credential = %+v, want business token and zcode JWT", credential)
	}
	waitForFrame(t, h.engine, "OAuth login for zai succeeded.")
	h.engine.RenderNow()
	frame := h.engine.AgentFrame()
	if visibleHas(frame, "OAuth login for zai failed") || visibleHas(frame, "Operation successful") {
		t.Errorf("successful exchange produced a failure flash:\n%s", frame.Dump())
	}
	film := tui.NewFilmstrip()
	film.Capture("business-code-200-success", frame, "")
	t.Logf("z.ai business code 200 filmstrip:\n%s", film.Render())
}

// TestLoginZaiOAuthFailure_Filmstrip drives the real broker → poll → business
// flow through LoginCommand's asynchronous path. The business exchange waits
// on a gate so the test can prove that the visible terminal stays responsive,
// then releases a secret-bearing denial and inspects the actual flash frames.
func TestLoginZaiOAuthFailure_Filmstrip(t *testing.T) {
	h := newLoginLoopHarness(t)
	film := tui.NewFilmstrip()
	businessStarted := make(chan struct{})
	releaseBusiness := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseBusiness) }) }
	server := newZaiFilmServer(t, businessStarted, releaseBusiness)
	// Release the gated handler before closing its server if an assertion fails.
	t.Cleanup(release)
	t.Setenv("GOA_ZCODE_OAUTH_URL", server.URL)
	t.Setenv("GOA_ZAI_BUSINESS_LOGIN_URL", server.URL+"/api/auth/z/login")

	cmd := &LoginCommand{
		Store:       h.store,
		flowFactory: func(string) oauthFlow { return &zaiFilmstripFlow{} },
	}
	args := []string{"zai", "oauth"}
	if hint := core.AsyncHintOf(cmd, args); hint == "" {
		t.Fatal("z.ai OAuth must use the host's asynchronous command path")
	}
	runDone := make(chan error, 1)
	go func() { runDone <- cmd.Run(h.ctx, args) }()
	if err := <-runDone; err != nil {
		t.Fatalf("LoginCommand.Run: %v", err)
	}

	select {
	case <-businessStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("business exchange did not start")
	}
	assertEngineResponsive(t, h.engine)
	h.engine.RenderNow()
	film.Capture("business-exchange-pending", h.engine.AgentFrame(), "")

	release()
	waitForFrame(t, h.engine, "business token exchange")
	waitForFrame(t, h.engine, "business denied")
	assertEngineResponsive(t, h.engine)
	h.engine.RenderNow()
	failureFrame := h.engine.AgentFrame()
	film.Capture("sanitized-business-denial", failureFrame, "")

	if visibleHas(failureFrame, "failed: oauth flow: zai_oauth_required") {
		t.Errorf("visible flash still exposes only the raw sentinel:\n%s", failureFrame.Dump())
	}
	for _, secret := range []string{zaiFilmOAuthToken, zaiFilmZcodeJWT} {
		if visibleHas(failureFrame, secret) {
			t.Errorf("visible flash contains credential %q:\n%s", secret, failureFrame.Dump())
		}
	}
	if h.store.HasAuth("zai") {
		t.Error("failed login stored a partial z.ai credential")
	}
	if len(film.Frames()) != 2 {
		t.Fatalf("filmstrip captured %d frames, want pending and failure", len(film.Frames()))
	}
	t.Logf("z.ai OAuth failure filmstrip:\n%s", film.Render())
}

func newZaiFilmServer(t *testing.T, businessStarted chan<- struct{}, releaseBusiness <-chan struct{}) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/oauth/cli/init":
			writeZaiFilmJSON(w, map[string]any{
				"code": 0,
				"data": map[string]any{
					"authorize_url":     "https://chat.z.ai/api/oauth/authorize",
					"expires_at":        time.Now().Add(time.Minute).Unix(),
					"flow_id":           "flow-visible",
					"poll_interval_sec": 1,
				},
			})
		case strings.HasPrefix(r.URL.Path, "/oauth/cli/poll/"):
			writeZaiFilmJSON(w, map[string]any{
				"code": 0,
				"data": map[string]any{
					"status": "ready",
					"token":  zaiFilmZcodeJWT,
					"zai":    map[string]any{"access_token": zaiFilmOAuthToken},
				},
			})
		case r.URL.Path == "/api/auth/z/login":
			close(businessStarted)
			<-releaseBusiness
			var body struct {
				Token string `json:"token"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			writeZaiFilmJSON(w, map[string]any{
				"code": 4001,
				"msg":  "business denied for " + body.Token + " and " + zaiFilmZcodeJWT,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func newZaiBusinessCode200Server(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/oauth/cli/init":
			writeZaiFilmJSON(w, map[string]any{
				"code": 0,
				"data": map[string]any{
					"authorize_url":     "https://chat.z.ai/api/oauth/authorize",
					"expires_at":        time.Now().Add(time.Minute).Unix(),
					"flow_id":           "flow-success-200",
					"poll_interval_sec": 1,
				},
			})
		case strings.HasPrefix(r.URL.Path, "/oauth/cli/poll/"):
			writeZaiFilmJSON(w, map[string]any{
				"code": 0,
				"data": map[string]any{
					"status": "ready",
					"token":  zaiFilmZcodeJWT,
					"zai":    map[string]any{"access_token": zaiFilmOAuthToken},
				},
			})
		case r.URL.Path == "/api/auth/z/login":
			writeZaiFilmJSON(w, map[string]any{
				"code":    200,
				"success": true,
				"msg":     "Operation successful",
				"data":    map[string]any{"access_token": "business-token-200"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func writeZaiFilmJSON(w http.ResponseWriter, body map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}
