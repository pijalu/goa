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
	Timeout int64 // milliseconds
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
