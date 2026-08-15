// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"context"
	"fmt"
	"sync"

	"github.com/pijalu/goa/internal/agentic/provider/protocol"
	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

// StreamRequest is the fully-resolved request handed to the StreamInterceptor
// chain. It is materialized after the request hooks and the protocol builder
// have run, so interceptors see — and may modify — the exact URL, headers, and
// body that will be sent before the transport executes.
type StreamRequest struct {
	Model   schema.Model
	Context schema.Context
	Options schema.StreamOptions
	Profile schema.VariantProfile
	Headers map[string]string
	Body    []byte
	URL     string

	// OnResponse, when non-nil, is invoked by the terminal handler after the
	// transport round-trip with the response status and headers, before the
	// body is parsed. Interceptors install observers here (composing on any
	// previous value) to observe responses; the MetricsInterceptor preserves
	// the historical StreamOptions.OnResponse callback by installing it here.
	OnResponse func(status int, headers map[string]string)
}

// StreamHandler executes one streaming LLM request and returns its event
// stream. The terminal handler runs the transport and parses the response;
// interceptors wrap it.
type StreamHandler func(ctx context.Context, req *StreamRequest) (*schema.AssistantMessageEventStream, error)

// StreamInterceptor wraps a StreamHandler with waterfall semantics: each
// wrapper sees the resolved request, may modify it before delegating to next,
// and may observe or wrap the returned event stream. This is the goa analogue
// of dsh's `llm/stream` waterfall event, documented as the mount point "for
// caching, logging, or routing" (packages/llm/llm/README.md §Events).
type StreamInterceptor func(next StreamHandler) StreamHandler

// ApplyStreamInterceptors composes interceptors in order: the first
// interceptor is the outermost wrapper, the last runs closest to the terminal
// handler.
func ApplyStreamInterceptors(handler StreamHandler, interceptors ...StreamInterceptor) StreamHandler {
	for i := len(interceptors) - 1; i >= 0; i-- {
		handler = interceptors[i](handler)
	}
	return handler
}

// canonicalStreamInterceptors is the chain applied to every protocol-backed
// streaming request (first = outermost). The built-in consumers replace the
// historical ad-hoc wraps: cache forensics (inline recording in the generic
// runtime) and metrics (the StreamOptions.OnResponse observation callback).
// Later consumers (e.g. purpose headers, CA2/CA3) register via
// RegisterStreamInterceptor.
var canonicalStreamInterceptors = []StreamInterceptor{
	CacheForensicsInterceptor,
	MetricsInterceptor,
}

var (
	streamInterceptorsMu   sync.Mutex
	streamInterceptorsList = append([]StreamInterceptor(nil), canonicalStreamInterceptors...)
)

// RegisterStreamInterceptor appends an interceptor to the canonical chain
// applied to every protocol-backed streaming request. First registered =
// outermost. Registering a nil interceptor panics.
func RegisterStreamInterceptor(fn StreamInterceptor) {
	if fn == nil {
		panic("provider: RegisterStreamInterceptor(nil)")
	}
	streamInterceptorsMu.Lock()
	defer streamInterceptorsMu.Unlock()
	streamInterceptorsList = append(streamInterceptorsList, fn)
}

// StreamInterceptors returns a snapshot of the canonical interceptor chain.
func StreamInterceptors() []StreamInterceptor {
	streamInterceptorsMu.Lock()
	defer streamInterceptorsMu.Unlock()
	out := make([]StreamInterceptor, len(streamInterceptorsList))
	copy(out, streamInterceptorsList)
	return out
}

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
