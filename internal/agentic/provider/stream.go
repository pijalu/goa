// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"fmt"

	"github.com/pijalu/goa/internal/agentic/provider/protocol"
	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

// stream initiates a streaming LLM request via the generic protocol runtime
// when a protocol is registered for the model's API. If no protocol is
// registered, it falls back to the legacy ApiProvider registry for test mocks
// and specialized providers not yet migrated to the protocol package.
func stream(model Model, ctx Context, opts StreamOptions) (*AssistantMessageEventStream, error) {
	if opts.APIKey == "" {
		opts.APIKey = GetEnvAPIKey(model.Provider)
	}
	if protocol.ForAPI(model.Api) != nil {
		return GenericStream(model, ctx, opts)
	}
	legacy, ok := GetApiProvider(model.Api)
	if !ok {
		return nil, fmt.Errorf("no provider registered for API type %q", model.Api)
	}
	return legacy.Stream(model, ctx, opts)
}

// Stream is the top-level entry point for LLM streaming.
func Stream(model Model, ctx Context, opts StreamOptions) (*AssistantMessageEventStream, error) {
	return stream(model, ctx, opts)
}

// StreamSimple is a convenience wrapper around Stream that handles
// thinking-level mapping automatically.
func StreamSimple(model Model, ctx Context, opts SimpleStreamOptions) (*AssistantMessageEventStream, error) {
	base := BuildSimpleOptions(model, opts)
	if base.APIKey == "" {
		base.APIKey = GetEnvAPIKey(model.Provider)
	}
	if protocol.ForAPI(model.Api) != nil {
		return GenericStream(model, ctx, base)
	}
	legacy, ok := GetApiProvider(model.Api)
	if !ok {
		return nil, fmt.Errorf("no provider registered for API type %q", model.Api)
	}
	return legacy.StreamSimple(model, ctx, opts)
}

// ensureSchemaAPI is a compile-time check that our Api aliases match schema.Api.
var _ = fmt.Sprintf
var _ schema.Api
