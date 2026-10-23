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

func writeZaiFilmJSON(w http.ResponseWriter, body map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}
