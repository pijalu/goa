// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"context"

	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	oauth "github.com/pijalu/goa/internal/agentic/provider/oauth"
	"github.com/pijalu/goa/internal/auth"
)

// resolveAPIKey resolves the API key for a provider id from the auth store
// (API key first, then OAuth access token), falling back to the provider
// catalog's environment variables when the store holds nothing. The env
// fallback comes LAST so an explicit credential always wins; it is what makes
// headless/CI use work with only (for example) AI_GATEWAY_API_KEY exported
// (bugs.md "Vercel AI Gateway: no way to add an API key").
func resolveAPIKey(store *auth.Store, providerID string) string {
	// Codex catalog provider shares the "openai" credential.
	if sid := codexStoreID(providerID); sid != "" {
		providerID = sid
	}
	if store != nil {
		if key, ok := store.GetAPIKey(providerID); ok && key != "" {
			return key
		}
		if tokens, ok := store.GetOAuth(providerID); ok {
			if key := oauthAccessToken(store, providerID, tokens); key != "" {
				return key
			}
		}
	}
	return envAPIKey(providerID)
}

// oauthAccessToken returns the OAuth access token for a stored credential,
// refreshing it when possible. Without a refresh token (or a known provider) we
// cannot refresh: return the current access token as-is.
func oauthAccessToken(store *auth.Store, providerID string, tokens *oauth.Tokens) string {
	if tokens == nil {
		return ""
	}
	prov := oauthProviderFor(providerID)
	if prov == nil || tokens.RefreshToken == "" || !tokens.IsExpired() {
		return tokens.AccessToken
	}
	ts := oauth.NewTokenSource(prov, tokens)
	return refreshAndPersist(context.Background(), prov, store, providerID, ts, tokens)
}

// envAPIKey resolves a key from the provider catalog's declared environment
// variables (the models.dev `env` names surfaced as ProviderDef.EnvKeys). A
// malformed value is treated as "no usable key": the wire path validates it
// again and reports the defect.
func envAPIKey(providerID string) string {
	key, err := agenticprovider.GetEnvAPIKey(agenticprovider.Provider(providerID))
	if err != nil {
		return ""
	}
	return key
}

// refreshAndPersist obtains a (possibly refreshed) access token from ts and
// writes the refreshed tokens back to the store so a rotated refresh token
// survives. It returns the access token; on refresh failure it falls back to
// the previously stored access token. Split out so it can be unit-tested with
// a fake provider (no network).
func refreshAndPersist(ctx context.Context, prov oauth.OAuthProvider, store *auth.Store, providerID string, ts *oauth.TokenSource, fallback *oauth.Tokens) string {
	token, err := ts.Token(ctx)
	if err != nil {
		return fallback.AccessToken
	}
	if refreshed := ts.Current(); refreshed != nil && refreshed.AccessToken != "" {
		toStore := *refreshed
		if toStore.RefreshToken == "" {
			toStore.RefreshToken = fallback.RefreshToken
		}
		_ = store.SetOAuth(providerID, &toStore)
	}
	return token
}

func oauthProviderFor(id string) oauth.OAuthProvider {
	switch id {
	case "copilot", "github":
		return oauth.NewGitHubCopilotOAuth()
	case "codex", "openai", "openai-codex":
		prov, err := oauth.NewOpenAICodexOAuth()
		if err != nil {
			return nil
		}
		return prov
	case "anthropic":
		// Anthropic OAT requires client credentials; no auto-refresh without config.
		return nil
	default:
		return nil
	}
}
