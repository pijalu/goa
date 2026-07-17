// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package transport

import (
	"context"
	"io"
)

// TransportRequest describes an HTTP/WebSocket request to execute.
type TransportRequest struct {
	Method  string
	URL     string
	Headers map[string]string
	Body    []byte
	// Timeout bounds the connection phase only, in milliseconds: dial, TLS
	// handshake, request send, and the wait for the first response header.
	// It deliberately does NOT bound response-body reads — a streaming LLM
	// response may legitimately take longer than any fixed wall clock on slow
	// local models. Stalled bodies are guarded by the idle-timeout reader in
	// the provider runtime, not by this field.
	Timeout int64
}

// TransportResponse describes a raw transport response.
type TransportResponse struct {
	StatusCode int
	Headers    map[string]string
	Body       io.ReadCloser
}

// Transport executes requests.
type Transport interface {
	Do(ctx context.Context, req *TransportRequest) (*TransportResponse, error)
}

var defaultTransport Transport = &HTTPTransport{}

// Default returns the default transport.
func Default() Transport {
	return defaultTransport
}

// SetDefault sets the default transport. Used for testing.
func SetDefault(t Transport) {
	defaultTransport = t
}
