// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal/agentic/provider/oauth"
	"github.com/pijalu/goa/internal/auth"
	"github.com/pijalu/goa/provider"
)

// TestPluginOAuthToken_ZaiServesBothCredentials pins the end of the chain: a
// /login:zai:oauth must be enough for a plugin to authenticate the Coding Plan
// reset surface.
//
// The bridge previously rejected every provider but openai/codex, so
// `goa.auth.oauthToken("zai")` errored and the quota plugin's reset fetcher had
// no way to obtain either credential — the reset view was unreachable without
// hand-pasting tokens into config. Both credentials must come back from this
// ONE call, because the reset API needs them as a pair.
func TestPluginOAuthToken_ZaiServesBothCredentials(t *testing.T) {
	store, err := auth.NewStore("")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.SetOAuth("zai", &oauth.Tokens{
		AccessToken: "business-token",
		ZcodeJWT:    "zcode-jwt",
		AccountID:   "u-1",
	}); err != nil {
		t.Fatalf("SetOAuth: %v", err)
	}

	got, err := pluginOAuthToken(context.Background(), store, "zai")
	if err != nil {
		t.Fatalf("pluginOAuthToken(zai): %v", err)
	}
	if got["accessToken"] != "business-token" {
		t.Errorf("accessToken = %v, want business-token", got["accessToken"])
	}
	if got["zcodeJwt"] != "zcode-jwt" {
		t.Errorf("zcodeJwt = %v, want zcode-jwt — the reset API needs it as Authorization", got["zcodeJwt"])
	}
	if got["accountId"] != "u-1" {
		t.Errorf("accountId = %v, want u-1", got["accountId"])
	}
}

// TestPluginOAuthToken_BigModelFamilyServed pins the CN family is reachable
// too: it shares the broker but stores under its own key, and a family that
// errored would make the CN Coding Plan account unfixable.
func TestPluginOAuthToken_BigModelFamilyServed(t *testing.T) {
	store, err := auth.NewStore("")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.SetOAuth("bigmodel", &oauth.Tokens{AccessToken: "bm-token", ZcodeJWT: "bm-jwt"}); err != nil {
		t.Fatalf("SetOAuth: %v", err)
	}
	got, err := pluginOAuthToken(context.Background(), store, "bigmodel")
	if err != nil {
		t.Fatalf("pluginOAuthToken(bigmodel): %v", err)
	}
	if got["accessToken"] != "bm-token" || got["zcodeJwt"] != "bm-jwt" {
		t.Errorf("bigmodel token = %+v, want the bm pair", got)
	}
}

// TestPluginOAuthToken_ZaiNoCrossFamilyFallback pins family isolation: a bigmodel
// login must NOT satisfy a zai request. They are different identities and the
// reset API validates against the family, so silently serving the other one
// would send the wrong credential and fail confusingly downstream.
func TestPluginOAuthToken_ZaiNoCrossFamilyFallback(t *testing.T) {
	store, err := auth.NewStore("")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.SetOAuth("bigmodel", &oauth.Tokens{AccessToken: "bm-token", ZcodeJWT: "bm-jwt"}); err != nil {
		t.Fatalf("SetOAuth: %v", err)
	}
	if got, err := pluginOAuthToken(context.Background(), store, "zai"); err == nil {
		t.Fatalf("zai resolved from a bigmodel-only login: %+v", got)
	}
}

// TestPluginOAuthToken_UnknownProviderStillRejected keeps the bridge
// fail-closed for providers with no OAuth at all.
func TestPluginOAuthToken_UnknownProviderStillRejected(t *testing.T) {
	store, err := auth.NewStore("")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if _, err := pluginOAuthToken(context.Background(), store, "anthropic"); err == nil {
		t.Fatal("anthropic must not resolve an OAuth token")
	}
}

// TestPluginProvidersMap_AuthStoreKeyExposed verifies a provider whose API key
// lives in the auth store (set via /login, not in ProviderConfig.APIKey) is
// exposed to plugins with that resolved key — otherwise the quota plugin sees
// no_api_key and the provider vanishes from /quota (z.ai #6).
func TestPluginProvidersMap_AuthStoreKeyExposed(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{
			{ID: "zai", Name: "Z.ai Coding", Provider: "zai", Endpoint: "https://api.z.ai/api/coding/paas/v4"},
		},
	}
	pm := provider.NewProviderManager(cfg)
	store, err := auth.NewStore("")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.SetAPIKey("zai", "zai-secret"); err != nil {
		t.Fatalf("SetAPIKey: %v", err)
	}
	pm.SetAuthStore(store)

	s := &subsystems{cfg: cfg, providerMgr: pm}
	m := pluginProvidersMap(s)

	entry, ok := m["zai"].(map[string]any)
	if !ok {
		t.Fatalf("zai provider missing from plugin providers map: %v", m)
	}
	if got := entry["apiKey"]; got != "zai-secret" {
		t.Errorf("apiKey = %v, want %q (auth store fallback)", got, "zai-secret")
	}
	if got := entry["provider"]; got != "zai" {
		t.Errorf("provider = %v, want %q", got, "zai")
	}
}

// TestPluginProvidersMap_CodingPlanCredentialsExposed verifies the z.ai Coding
// Plan RESET credentials a provider declares reach the plugin.
//
// The reset API is dual-auth: a zcode business JWT (Authorization) PLUS the
// family OAuth token (X-Bigmodel-Authorization). Both are read by the quota
// plugin from the provider config entry. While pluginProvidersMap emitted only
// the six structural fields, those keys could never be present, so
// resolveAuth() always failed with coding_plan_reset_zcode_jwt_required and
// /quota silently rendered no reset section even for an account holding
// resets (the bug this test pins).
func TestPluginProvidersMap_CodingPlanCredentialsExposed(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{
			{
				ID:       "zai",
				Name:     "Z.ai Coding",
				Provider: "zai",
				Endpoint: "https://api.z.ai/api/coding/paas/v4",
				Extra: map[string]any{
					"zcodeJwt":      "zjwt-1",
					"accessToken":   "zai-oauth-tok",
					"zcodejwttoken": "raw-extra-value",
				},
				Metadata: map[string]string{"zcodeJwt": "metadata-loses"},
			},
		},
	}
	s := &subsystems{cfg: cfg}
	entry, ok := pluginProvidersMap(s)["zai"].(map[string]any)
	if !ok {
		t.Fatalf("zai provider missing from plugin providers map")
	}
	if got := entry["zcodeJwt"]; got != "zjwt-1" {
		t.Errorf("zcodeJwt = %v, want %q (extra must reach the plugin)", got, "zjwt-1")
	}
	if got := entry["accessToken"]; got != "zai-oauth-tok" {
		t.Errorf("accessToken = %v, want %q (extra must reach the plugin)", got, "zai-oauth-tok")
	}
	// extra is one flat map: a snake_case spelling must not shadow the
	// canonical camelCase key the fetcher reads first.
	if got := entry["zcodejwttoken"]; got != "raw-extra-value" {
		t.Errorf("zcodejwttoken = %v, want the raw extra value", got)
	}
	// metadata is a separate, non-secret map and must not fabricate a
	// credential: an empty extra leaves the canonical key unset.
	empty := &subsystems{cfg: &config.Config{
		Providers: []config.ProviderConfig{{
			ID: "zai", Provider: "zai",
			Metadata: map[string]string{"zcodeJwt": "metadata-only"},
		}},
	}}
	entry2 := pluginProvidersMap(empty)["zai"].(map[string]any)
	if v, present := entry2["zcodeJwt"]; present {
		t.Errorf("zcodeJwt = %v, want absent: metadata must not supply a reset credential", v)
	}
}

// TestPluginProvidersMap_ExtraCannotShadowStructuralFields verifies extra keys
// merge UNDER the structural fields. A user extra named apiKey or endpoint must
// not rewrite the identity every fetcher resolves on.
func TestPluginProvidersMap_ExtraCannotShadowStructuralFields(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{
			ID: "zai", Name: "Z.ai Coding", Provider: "zai",
			Endpoint: "https://api.z.ai/api/coding/paas/v4",
			Extra: map[string]any{
				"apiKey":      "spoofed",
				"endpoint":    "https://evil.example",
				"zcodeJwt":    "zjwt-1",
				"accessToken": "tok",
			},
		}},
	}
	s := &subsystems{cfg: cfg}
	entry := pluginProvidersMap(s)["zai"].(map[string]any)
	if got := entry["endpoint"]; got != "https://api.z.ai/api/coding/paas/v4" {
		t.Errorf("endpoint = %v, want the configured endpoint (extra must not shadow it)", got)
	}
	if got := entry["apiKey"]; got == "spoofed" {
		t.Errorf("apiKey = %v, want real key resolution (extra must not shadow it)", got)
	}
	if got := entry["zcodeJwt"]; got != "zjwt-1" {
		t.Errorf("zcodeJwt = %v, want %q (extra must still merge)", got, "zjwt-1")
	}
}

// TestPluginProvidersMap_NoKeyAnywhere verifies a provider with no key in
// config or auth store still appears (with empty key) — the plugin then
// reports no_api_key rather than the entry being absent.
func TestPluginProvidersMap_NoKeyAnywhere(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{ID: "zai", Provider: "zai"}},
	}
	pm := provider.NewProviderManager(cfg)
	s := &subsystems{cfg: cfg, providerMgr: pm}
	m := pluginProvidersMap(s)
	entry, ok := m["zai"].(map[string]any)
	if !ok {
		t.Fatalf("zai provider missing: %v", m)
	}
	if got := entry["apiKey"]; got != "" {
		t.Errorf("apiKey = %v, want empty", got)
	}
}
