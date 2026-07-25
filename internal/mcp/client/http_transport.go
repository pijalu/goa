// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// legacyFallbackError signals the server does not speak Streamable HTTP, so the
// caller should retry with the legacy HTTP+SSE transport.
type legacyFallbackError struct{ reason string }

func (e *legacyFallbackError) Error() string { return e.reason }

func isLegacyFallback(err error) bool {
	_, ok := err.(*legacyFallbackError)
	return ok
}

// roundTrip sends one JSON-RPC request and returns its result. In streamable
// mode the HTTP response carries the result (as JSON or an SSE stream); in
// legacy mode the POST is fire-and-forget (202 Accepted) and the result arrives
// on the SSE stream, demultiplexed by request id.
func (c *HTTPClient) roundTrip(ctx context.Context, method string, params any, legacy bool) (json.RawMessage, error) {
	if c.closed.Load() {
		return nil, fmt.Errorf("client closed")
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	id := int(c.id.Add(1))
	body, err := marshalRequest(id, method, params)
	if err != nil {
		return nil, err
	}

	if legacy {
		return c.roundTripLegacy(ctx, id, body)
	}
	return c.roundTripStreamable(ctx, id, body)
}

// roundTripStreamable POSTs to the single endpoint and reads the result from
// the HTTP response body (JSON or SSE).
func (c *HTTPClient) roundTripStreamable(ctx context.Context, id int, body []byte) (json.RawMessage, error) {
	req, err := c.newRequest(ctx, http.MethodPost, c.url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mcp http request: %w", err)
	}
	defer resp.Body.Close()

	c.captureSessionID(resp)

	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		return nil, &legacyFallbackError{reason: "streamable endpoint returned " + resp.Status}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("mcp http status %s: %s", resp.Status, readBodySnippet(resp.Body))
	}

	ct := resp.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "text/event-stream") {
		return c.readStreamableSSE(ctx, id, resp.Body)
	}
	return decodeSingleResponse(resp.Body, id)
}

// roundTripLegacy POSTs to the legacy message endpoint and waits for the
// matching response on the SSE stream.
func (c *HTTPClient) roundTripLegacy(ctx context.Context, id int, body []byte) (json.RawMessage, error) {
	c.mu.Lock()
	postURL := c.legacyPostURL
	c.mu.Unlock()
	if postURL == "" {
		return nil, fmt.Errorf("legacy SSE endpoint not established")
	}

	wait := make(chan rpcResponse, 1)
	c.mu.Lock()
	c.pending[id] = wait
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	req, err := c.newRequest(ctx, http.MethodPost, postURL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mcp legacy post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("mcp legacy post status %s: %s", resp.Status, readBodySnippet(resp.Body))
	}

	select {
	case res := <-wait:
		if res.Error != nil {
			return nil, res.Error
		}
		return res.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// openLegacyStream opens the SSE GET stream and starts the reader goroutine.
func (c *HTTPClient) openLegacyStream(ctx context.Context) error {
	req, err := c.newRequest(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("mcp sse connect: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return fmt.Errorf("mcp sse status %s: %s", resp.Status, readBodySnippet(resp.Body))
	}
	c.captureSessionID(resp)

	c.streamWg.Add(1)
	go func() {
		defer c.streamWg.Done()
		defer resp.Body.Close()
		c.readLegacySSE(ctx, resp.Body)
	}()
	return nil
}

// readLegacySSE consumes legacy SSE events: the "endpoint" event yields the
// POST URL; "message" events carry JSON-RPC responses/notifications.
func (c *HTTPClient) readLegacySSE(ctx context.Context, body io.Reader) {
	sc := newSSEScanner(body)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		ev, err := sc.next()
		if err != nil {
			c.failAllPendingHTTP(err)
			return
		}
		switch ev.Event {
		case "endpoint":
			c.deliverEndpoint(string(ev.Data))
		case "message", "":
			c.dispatchFrame(ev.Data)
		}
	}
}

// readStreamableSSE reads a streamable HTTP SSE response until the result for
// the given request id arrives.
func (c *HTTPClient) readStreamableSSE(ctx context.Context, id int, body io.Reader) (json.RawMessage, error) {
	sc := newSSEScanner(body)
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		ev, err := sc.next()
		if err != nil {
			return nil, err
		}
		var resp rpcResponse
		if jerr := json.Unmarshal(ev.Data, &resp); jerr != nil {
			c.dispatchFrame(ev.Data) // may be a notification
			continue
		}
		if resp.ID == id {
			if resp.Error != nil {
				return nil, resp.Error
			}
			return resp.Result, nil
		}
		// A response/notification for another id: forward for dispatch.
		c.dispatchFrame(ev.Data)
	}
}

// dispatchFrame routes an inbound JSON-RPC frame to its waiter or the
// notification handler.
func (c *HTTPClient) dispatchFrame(frame []byte) {
	var resp rpcResponse
	if err := json.Unmarshal(frame, &resp); err != nil {
		return
	}
	if resp.ID == 0 {
		c.notifyHTTP(frame)
		return
	}
	c.mu.Lock()
	ch, ok := c.pending[resp.ID]
	c.mu.Unlock()
	if ok {
		select {
		case ch <- resp:
		default:
		}
	}
}

func (c *HTTPClient) deliverEndpoint(ep string) {
	select {
	case c.endpointCh <- ep:
	default:
	}
}

func (c *HTTPClient) notifyHTTP(frame []byte) {
	c.mu.Lock()
	h := c.notifier
	c.mu.Unlock()
	if h == nil {
		return
	}
	var notif struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(frame, &notif); err != nil {
		return
	}
	h(notif.Method, notif.Params)
}

func (c *HTTPClient) failAllPendingHTTP(err error) {
	res := rpcResponse{Error: asRPCError(err)}
	c.mu.Lock()
	pending := c.pending
	c.pending = make(map[int]chan rpcResponse)
	c.mu.Unlock()
	for _, ch := range pending {
		select {
		case ch <- res:
		default:
		}
	}
}

// newRequest builds an HTTP request with configured headers and session id.
func (c *HTTPClient) newRequest(ctx context.Context, method, url string, body []byte) (*http.Request, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return nil, err
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	c.mu.Lock()
	sid := c.sessionID
	c.mu.Unlock()
	if sid != "" {
		req.Header.Set("Mcp-Session-Id", sid)
	}
	return req, nil
}

func (c *HTTPClient) captureSessionID(resp *http.Response) {
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.mu.Lock()
		c.sessionID = sid
		c.mu.Unlock()
	}
}

// marshalRequest encodes a JSON-RPC request frame.
func marshalRequest(id int, method string, params any) ([]byte, error) {
	data, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return nil, err
	}
	return data, nil
}

// decodeSingleResponse decodes a JSON body holding one JSON-RPC response.
func decodeSingleResponse(body io.Reader, wantID int) (json.RawMessage, error) {
	var resp rpcResponse
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode mcp response: %w", err)
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	return resp.Result, nil
}

// readBodySnippet reads a bounded prefix of a body for error messages.
func readBodySnippet(body io.Reader) string {
	const max = 512
	data, _ := io.ReadAll(io.LimitReader(body, max))
	return strings.TrimSpace(string(data))
}
