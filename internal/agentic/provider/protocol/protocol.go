// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package protocol

import (
	"fmt"
	"io"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

// Protocol converts between canonical model/context/options and provider wire
// format, and parses streaming responses.
type Protocol interface {
	API() schema.Api
	BuildRequest(model schema.Model, ctx schema.Context, opts schema.StreamOptions, profile schema.VariantProfile) ([]byte, error)
	RequestHeaders(model schema.Model, profile schema.VariantProfile) map[string]string
	ParseResponse(reader io.Reader, stream *schema.AssistantMessageEventStream) error
}

var registry = make(map[schema.Api]Protocol)

// Register registers a protocol for an API type.
func Register(p Protocol) {
	if p == nil {
		panic("protocol: Register(nil)")
	}
	api := p.API()
	if _, exists := registry[api]; exists {
		panic(fmt.Sprintf("protocol already registered for API %q", api))
	}
	registry[api] = p
}

// ForAPI returns the protocol registered for the given API, or nil.
func ForAPI(api schema.Api) Protocol {
	return registry[api]
}

// RegisteredAPIs returns all registered APIs.
func RegisteredAPIs() []schema.Api {
	apis := make([]schema.Api, 0, len(registry))
	for api := range registry {
		apis = append(apis, api)
	}
	return apis
}

// Clear removes all registered protocols. Used for testing.
func Clear() {
	for k := range registry {
		delete(registry, k)
	}
}
