// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package schema

// AuthMethod selects how credentials are resolved.
type AuthMethod string

const (
	AuthMethodAPIKey AuthMethod = "api_key"
	AuthMethodOAuth  AuthMethod = "oauth"
	AuthMethodNone   AuthMethod = "none"
)

// HeaderRule describes a header to inject based on profile rules.
type HeaderRule struct {
	Name   string `json:"name"`
	Value  string `json:"value,omitempty"`
	EnvVar string `json:"env_var,omitempty"`
	Prefix string `json:"prefix,omitempty"`
	IfSet  string `json:"if_set,omitempty"`
}

// AuthConfig describes how to authenticate requests for a variant profile.
type AuthConfig struct {
	Method        AuthMethod   `json:"method,omitempty"`
	EnvVars       []string     `json:"env_vars,omitempty"`
	Header        string       `json:"header,omitempty"`
	Prefix        string       `json:"prefix,omitempty"`
	TokenPrefix   string       `json:"token_prefix,omitempty"`
	OAuthIdentity []HeaderRule `json:"oauth_identity,omitempty"`
	BearerPrefix  string       `json:"bearer_prefix,omitempty"`
	Required      bool         `json:"required,omitempty"`
	// PerAPI overrides the header/prefix for a specific wire API (keyed by
	// schema.Api string, e.g. "anthropic-messages"). Used by gateway
	// providers (opencode zen/go) that serve several wire formats under one
	// provider identity, where the anthropic /messages surface expects
	// "x-api-key" while the chat/responses surfaces expect Bearer.
	PerAPI map[string]APIAuth `json:"per_api,omitempty"`
}

// APIAuth is a per-API auth header/prefix override. Empty fields inherit
// the parent AuthConfig value.
type APIAuth struct {
	Header string `json:"header,omitempty"`
	Prefix string `json:"prefix,omitempty"`
}
