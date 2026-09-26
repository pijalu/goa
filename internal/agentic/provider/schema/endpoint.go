// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package schema

import "fmt"

// NoEndpointError reports that no request URL could be resolved for a model:
// the provider configuration carries no endpoint (or base URL) and the provider
// catalog declares no default base URL for that provider.
//
// Goa refuses to guess. Falling back to the OpenAI host for a provider that is
// not OpenAI is how a gateway's vendor-namespaced model id (for example
// "stealth/pixel-canary" on the Vercel AI Gateway) ended up at
// api.openai.com/v1/chat/completions and was rejected with "invalid model id"
// instead of reaching the gateway (bugs.md "Vercel AI Gateway: no way to add an
// API key", export 2026-09-26-113044).
type NoEndpointError struct {
	// Provider is the model's provider identity ("" when unset).
	Provider string
	// Model is the requested model id, echoed so the user can tell which
	// request was refused.
	Model string
}

// Error implements error.
func (e *NoEndpointError) Error() string {
	if e == nil {
		return "no endpoint configured for the provider"
	}
	prov := e.Provider
	if prov == "" {
		prov = "(unset)"
	}
	return fmt.Sprintf(
		"provider %q has no endpoint for model %q — set endpoint (or base_url) on the provider in config, or use a provider the catalog knows a base URL for; "+
			"Goa will not send the request to another vendor's API", prov, e.Model)
}
