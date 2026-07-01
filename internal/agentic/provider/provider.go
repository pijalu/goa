// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import "github.com/pijalu/goa/internal/agentic/provider/schema"

// GenericProvider implements ApiProvider for a single API type by delegating
// to GenericStream.
type GenericProvider struct {
	api schema.Api
}

// NewGenericProvider creates a generic provider for the given API.
func NewGenericProvider(api schema.Api) *GenericProvider {
	return &GenericProvider{api: api}
}

// API returns the provider's API type.
func (p *GenericProvider) API() schema.Api {
	return p.api
}

// Stream initiates a streaming request via the generic runtime.
func (p *GenericProvider) Stream(model Model, ctx Context, opts StreamOptions) (*AssistantMessageEventStream, error) {
	return GenericStream(model, ctx, opts)
}

// StreamSimple is a convenience wrapper around Stream that resolves reasoning
// level defaults through the model's thinking level map.
func (p *GenericProvider) StreamSimple(model Model, ctx Context, opts SimpleStreamOptions) (*AssistantMessageEventStream, error) {
	return GenericStream(model, ctx, BuildSimpleOptions(model, opts))
}
