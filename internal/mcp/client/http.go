// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// protocolVersion is the MCP protocol revision Goa negotiates. 2024-11-05 is
// the last revision supported by both the legacy HTTP+SSE transport and the
// Streamable HTTP transport, so it maximizes server compatibility.
const protocolVersion = "2024-11-05"

// HTTPOptions configures an HTTP MCP client.
type HTTPOptions struct {
	// Headers are sent with every request (e.g. Authorization).
	Headers map[string]string
	// Timeout bounds each request. Zero uses a sane default.
	Timeout time.Duration
	// HTTPClient overrides the http.Client (mainly for tests).
	HTTPClient *http.Client
}

// HTTPClient connects to a remote MCP server. It prefers the Streamable HTTP
// transport (single endpoint, POST of JSON-RPC, response as JSON or SSE) and
// falls back to the legacy HTTP+SSE transport (GET opens an SSE stream whose
// first "endpoint" event yields the POST URL) when the server rejects the
// streamable handshake.
type HTTPClient struct {
	url     string
	headers map[string]string
	timeout time.Duration
	hc      *http.Client

	id     atomic.Int32
	closed atomic.Bool

	mu        sync.Mutex
	sessionID string
	legacy    bool
	// legacyPostURL is the message endpoint learned from the SSE "endpoint"
	// event in legacy mode.
	legacyPostURL string
	// endpointCh delivers the legacy POST endpoint once the SSE stream opens.
	endpointCh chan string
	// pending maps request id -> waiter for legacy/streamed responses.
	pending map[int]chan rpcResponse
	// streamCancel stops the background SSE reader (legacy or streamable GET).
	streamCancel context.CancelFunc
	streamWg     sync.WaitGroup
	notifier     NotificationHandler
}

// NewHTTPClient creates an HTTP MCP client for the given endpoint.
func NewHTTPClient(url string, opts HTTPOptions) *HTTPClient {
	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{}
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &HTTPClient{
		url:        url,
		headers:    opts.Headers,
		timeout:    timeout,
		hc:         hc,
		endpointCh: make(chan string, 1),
		pending:    make(map[int]chan rpcResponse),
	}
}

// SetNotificationHandler registers a handler for server notifications.
func (c *HTTPClient) SetNotificationHandler(h NotificationHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.notifier = h
}

// Initialize performs the MCP handshake, selecting the transport.
func (c *HTTPClient) Initialize(ctx context.Context) error {
	initParams := map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "goa", "version": "0.1"},
	}
	if err := c.initializeStreamable(ctx, initParams); err != nil {
		if !isLegacyFallback(err) {
			return err
		}
		if lerr := c.initializeLegacy(ctx, initParams); lerr != nil {
			return fmt.Errorf("streamable: %v; legacy: %w", err, lerr)
		}
	}
	return nil
}

// initializeStreamable attempts the Streamable HTTP handshake.
func (c *HTTPClient) initializeStreamable(ctx context.Context, params map[string]any) error {
	_, err := c.roundTrip(ctx, "initialize", params, false)
	return err
}

// initializeLegacy opens the legacy HTTP+SSE transport.
func (c *HTTPClient) initializeLegacy(ctx context.Context, params map[string]any) error {
	streamCtx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	c.legacy = true
	c.streamCancel = cancel
	c.mu.Unlock()

	if err := c.openLegacyStream(streamCtx); err != nil {
		cancel()
		return err
	}
	// Wait for the endpoint event with the caller's deadline.
	select {
	case ep := <-c.endpointCh:
		c.mu.Lock()
		c.legacyPostURL = ep
		c.mu.Unlock()
	case <-ctx.Done():
		cancel()
		return ctx.Err()
	case <-time.After(c.timeout):
		cancel()
		return fmt.Errorf("timed out waiting for SSE endpoint event")
	}
	_, err := c.roundTrip(ctx, "initialize", params, true)
	return err
}

// ListTools returns the tools exposed by the server.
func (c *HTTPClient) ListTools(ctx context.Context) ([]ToolInfo, error) {
	res, err := c.roundTrip(ctx, "tools/list", map[string]any{}, c.isLegacy())
	if err != nil {
		return nil, err
	}
	var out struct {
		Tools []ToolInfo `json:"tools"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return nil, fmt.Errorf("parse tools/list: %w", err)
	}
	return out.Tools, nil
}

// CallTool invokes a tool.
func (c *HTTPClient) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	res, err := c.roundTrip(ctx, "tools/call", map[string]any{"name": name, "arguments": args}, c.isLegacy())
	if err != nil {
		return "", err
	}
	var out mcpResult
	if err := json.Unmarshal(res, &out); err != nil {
		return "", fmt.Errorf("parse tool result: %w", err)
	}
	if out.IsError {
		return "", fmt.Errorf("tool error: %s", concatContent(out.Content))
	}
	return concatContent(out.Content), nil
}

// Close shuts down the client and any background stream.
func (c *HTTPClient) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	c.mu.Lock()
	if c.streamCancel != nil {
		c.streamCancel()
	}
	pending := c.pending
	c.pending = make(map[int]chan rpcResponse)
	c.mu.Unlock()
	for _, ch := range pending {
		select {
		case ch <- rpcResponse{Error: asRPCError(context.Canceled)}:
		default:
		}
	}
	c.streamWg.Wait()
	return nil
}

// isLegacy reports whether the legacy transport was negotiated.
func (c *HTTPClient) isLegacy() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.legacy
}
