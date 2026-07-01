// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"fmt"
	"sync"
)

// ---------------------------------------------------------------------------
// ApiProvider interface
// ---------------------------------------------------------------------------

// ApiProvider is the interface every provider backend must implement.
// Each provider handles the wire protocol for one or more API types and
// converts between the canonical Model/Context/StreamOptions and the
// provider-specific request/response format.
type ApiProvider interface {
	// API returns the API type this provider handles.
	// A single provider struct may handle multiple APIs by checking the
	// Model.Api field at runtime.
	API() Api

	// Stream initiates a streaming LLM request and returns an event stream.
	// The caller iterates over events (text deltas, thinking deltas, tool
	// calls, etc.) and must wait on the stream's Result() for the final
	// accumulated message.
	Stream(model Model, ctx Context, opts StreamOptions) (*AssistantMessageEventStream, error)

	// StreamSimple is convenience wrapper around Stream that handles
	// thinking-level mapping automatically. The thinking level is applied
	// according to the model's ThinkingLevelMap.
	StreamSimple(model Model, ctx Context, opts SimpleStreamOptions) (*AssistantMessageEventStream, error)
}

// ---------------------------------------------------------------------------
// Provider Registry
// ---------------------------------------------------------------------------

var (
	registry   = make(map[Api]ApiProvider)
	registryMu sync.RWMutex
)

// RegisterApiProvider registers a provider for the given API type.
// Panics if a provider is already registered for the same API type.
func RegisterApiProvider(p ApiProvider) {
	registryMu.Lock()
	defer registryMu.Unlock()

	api := p.API()
	if _, exists := registry[api]; exists {
		panic(fmt.Sprintf("provider already registered for API type %q", api))
	}
	registry[api] = p
}

// GetApiProvider returns the provider registered for the given API type.
// Returns nil, false if no provider is registered.
func GetApiProvider(api Api) (ApiProvider, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	p, ok := registry[api]
	return p, ok
}

// ClearApiProviders removes all registered providers. Used for testing.
func ClearApiProviders() {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = make(map[Api]ApiProvider)
}

// RegisteredAPIs returns a list of all registered API identifiers.
func RegisteredAPIs() []Api {
	registryMu.RLock()
	defer registryMu.RUnlock()
	apis := make([]Api, 0, len(registry))
	for api := range registry {
		apis = append(apis, api)
	}
	return apis
}
