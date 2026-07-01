// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider/transport"
)

// NewStreamingHTTPClient creates an *http.Client suitable for streaming LLM
// requests. Unlike a plain http.Client{Timeout: ...} the overall body-read
// timeout is NOT set — a streaming response may take minutes or longer.
//
// Timeouts are applied only to the connection-establishment phase:
//   - Dial:          30s
//   - TLS handshake: 15s
//   - Response headers (first byte): 30s
//
// The request must already carry a context (via NewRequestWithContext) so
// that cancellation is propagated promptly.
func NewStreamingHTTPClient() *http.Client {
	return &http.Client{
		// No Timeout — the body read can last as long as the stream.
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			ExpectContinueTimeout: 0,
		},
	}
}

// NewHTTPClientWithTimeout creates an *http.Client with a fixed overall
// timeout (including response body). Use this for non-streaming requests.
func NewHTTPClientWithTimeout(timeout time.Duration) *http.Client {
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &http.Client{Timeout: timeout}
}

func init() {
	// Install a streaming-aware HTTP client as the default transport so the
	// generic runtime can be overridden by tests without losing production
	// connection timeouts.
	transport.SetDefault(&transport.HTTPTransport{Client: NewStreamingHTTPClient()})
}

// CloseStreamOnCancel blocks until ctx is done and then terminates the stream
// with the context error. It must be launched as a goroutine immediately
// after creating the stream:
//
//	go CloseStreamOnCancel(ctx, stream)
//
// Without this guard, context cancellation (e.g. Ctrl+C) may not unblock the
// SSE parser goroutine promptly if it is between reads.
func CloseStreamOnCancel(ctx context.Context, stream *AssistantMessageEventStream) {
	<-ctx.Done()
	stream.CloseWithError(ctx.Err())
}
