// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	oauth "github.com/pijalu/goa/internal/agentic/provider/oauth"
)

func TestListModels_UsesAuthStoreAPIKey(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data":   []map[string]any{{"id": "gpt-test"}},
		})
	}))
	defer server.Close()

	store := mustAuthStore(t)
	_ = store.SetAPIKey("openai", "stored-listmodels-key")

	cfg := &config.Config{
		ActiveProvider: "openai",
		Providers: []config.ProviderConfig{
			{ID: "openai", Endpoint: server.URL + "/v1"},
		},
	}
	pm := NewProviderManager(cfg)
	pm.SetAuthStore(store)

	models, err := pm.ListModels("openai")
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 1 || models[0].ID != "gpt-test" {
		t.Fatalf("unexpected models: %+v", models)
	}
	if gotAuth != "Bearer stored-listmodels-key" {
		t.Errorf("Authorization = %q, want Bearer stored-listmodels-key", gotAuth)
	}
}

// fakeOAuthProvider is a minimal OAuthProvider for testing refresh+persist.
type fakeOAuthProvider struct{ refreshed *oauth.Tokens }

func (f *fakeOAuthProvider) Name() string                                          { return "fake" }
func (f *fakeOAuthProvider) AuthURL(ctx context.Context) (string, error)           { return "", nil }
func (f *fakeOAuthProvider) Exchange(ctx context.Context, code string) (*oauth.Tokens, error) {
	return f.refreshed, nil
}
func (f *fakeOAuthProvider) Refresh(ctx context.Context, refreshToken string) (*oauth.Tokens, error) {
	return f.refreshed, nil
}
func (f *fakeOAuthProvider) TokenSource(ctx context.Context, tokens *oauth.Tokens) (*oauth.TokenSource, error) {
	return oauth.NewTokenSource(f, tokens), nil
}

// TestRefreshAndPersist_PersistsRotatedRefreshToken verifies a refreshed token
// (with a rotated refresh token) is written back to the store (E2).
func TestRefreshAndPersist_PersistsRotatedRefreshToken(t *testing.T) {
	store := mustAuthStore(t)
	original := &oauth.Tokens{
		AccessToken:  "old-access",
		RefreshToken: "old-refresh",
		ExpiresAt:    time.Now().Add(-time.Hour), // expired -> forces refresh
	}
	if err := store.SetOAuth("copilot", original); err != nil {
		t.Fatalf("set oauth: %v", err)
	}

	prov := &fakeOAuthProvider{refreshed: &oauth.Tokens{
		AccessToken:  "new-access",
		RefreshToken: "new-refresh",
		ExpiresAt:    time.Now().Add(time.Hour),
	}}
	ts, _ := prov.TokenSource(context.Background(), original)
	got := refreshAndPersist(context.Background(), prov, store, "copilot", ts, original)
	if got != "new-access" {
		t.Errorf("token = %q, want new-access", got)
	}
	stored, ok := store.GetOAuth("copilot")
	if !ok {
		t.Fatal("no tokens stored after refresh")
	}
	if stored.AccessToken != "new-access" || stored.RefreshToken != "new-refresh" {
		t.Errorf("stored = %+v, want rotated tokens", stored)
	}
}

// TestRefreshAndPersist_KeepsRefreshTokenWhenOmitted verifies that when the
// refreshed payload omits a refresh token, the previous one is retained (so
// future refreshes still work).
func TestRefreshAndPersist_KeepsRefreshTokenWhenOmitted(t *testing.T) {
	store := mustAuthStore(t)
	original := &oauth.Tokens{
		AccessToken:  "old-access",
		RefreshToken: "keep-refresh",
		ExpiresAt:    time.Now().Add(-time.Hour),
	}
	_ = store.SetOAuth("copilot", original)

	prov := &fakeOAuthProvider{refreshed: &oauth.Tokens{
		AccessToken: "new-access",
		// no RefreshToken in payload
		ExpiresAt: time.Now().Add(time.Hour),
	}}
	ts, _ := prov.TokenSource(context.Background(), original)
	_ = refreshAndPersist(context.Background(), prov, store, "copilot", ts, original)
	stored, _ := store.GetOAuth("copilot")
	if stored.RefreshToken != "keep-refresh" {
		t.Errorf("refresh token = %q, want keep-refresh (retained)", stored.RefreshToken)
	}
	if stored.AccessToken != "new-access" {
		t.Errorf("access token = %q, want new-access", stored.AccessToken)
	}
}
