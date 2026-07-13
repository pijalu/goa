// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package auth

import (
	"time"

	oauth "github.com/pijalu/goa/internal/agentic/provider/oauth"
)

// Kind identifies the type of credential stored for a provider.
type Kind string

const (
	KindOAuth Kind = "oauth"
	KindAPIKey Kind = "apikey"
)

// Credential stores authentication material for a provider. Exactly one of
// Tokens or APISecret is populated depending on Kind.
type Credential struct {
	Provider  string         `json:"provider"`
	Kind      Kind           `json:"kind"`
	APISecret string         `json:"api_secret,omitempty"`
	Tokens    *oauth.Tokens  `json:"tokens,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

// NewOAuthCredential builds a credential backed by OAuth tokens.
func NewOAuthCredential(provider string, tokens *oauth.Tokens) Credential {
	return Credential{
		Provider:  provider,
		Kind:      KindOAuth,
		Tokens:    tokens,
		CreatedAt: time.Now(),
	}
}

// NewAPIKeyCredential builds a credential backed by an API key.
func NewAPIKeyCredential(provider, key string) Credential {
	return Credential{
		Provider:  provider,
		Kind:      KindAPIKey,
		APISecret: key,
		CreatedAt: time.Now(),
	}
}

// IsOAuth returns true if the credential is OAuth-based.
func (c Credential) IsOAuth() bool { return c.Kind == KindOAuth }

// IsAPIKey returns true if the credential is API-key-based.
func (c Credential) IsAPIKey() bool { return c.Kind == KindAPIKey }
