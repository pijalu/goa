// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"context"

	oauth "github.com/pijalu/goa/internal/agentic/provider/oauth"
	"github.com/pijalu/goa/internal/auth"
)

func resolveAPIKey(store *auth.Store, providerID string) string {
	// Codex catalog provider shares the "openai" credential.
	if sid := codexStoreID(providerID); sid != "" {
		providerID = sid
	}
	if key, ok := store.GetAPIKey(providerID); ok {
		return key
	}
	tokens, ok := store.GetOAuth(providerID)
	if !ok {
		return ""
	}
	// Without a refresh token (or a known provider), we cannot refresh: return
	// the current access token as-is.
	prov := oauthProviderFor(providerID)
	if prov == nil || tokens.RefreshToken == "" || !tokens.IsExpired() {
		return tokens.AccessToken
	}
	ts := oauth.NewTokenSource(prov, tokens)
	return refreshAndPersist(context.Background(), prov, store, providerID, ts, tokens)
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
