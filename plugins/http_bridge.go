// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxHTTPBody caps quota API responses so a misbehaving endpoint cannot
// exhaust memory through the JS bridge.
const maxHTTPBody = 10 << 20 // 10 MiB

// defaultHTTPTimeout applies when the caller does not pass timeoutMs.
const defaultHTTPTimeout = 30 * time.Second

// HTTPRequest describes an outgoing HTTP call from a plugin.
type HTTPRequest struct {
	Method  string
	URL     string
	Headers map[string]string
	Body    string
	Timeout time.Duration
}

// HTTPResponse is the structured result returned to the plugin.
type HTTPResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
	Error   string            `json:"error,omitempty"`
}

// HTTPBridge performs HTTP requests on behalf of plugins. All calls run on
// background goroutines (net/http is goroutine-safe); the JS caller blocks on
// the plugin runner, never on the TUI.
type HTTPBridge struct {
	client *http.Client
	// allowHTTP reports whether plain http:// URLs are permitted. Restricted
	// to loopback hosts so local model servers (llama.cpp, ollama) remain
	// reachable while arbitrary cleartext endpoints are refused.
	allowLoopbackHTTP bool
}

// NewHTTPBridge creates a bridge with a default transport.
func NewHTTPBridge() *HTTPBridge {
	return &HTTPBridge{
		client: &http.Client{
			Transport: &http.Transport{
				MaxIdleConns:        8,
				IdleConnTimeout:     90 * time.Second,
				TLSHandshakeTimeout: 10 * time.Second,
			},
		},
		allowLoopbackHTTP: true,
	}
}

// Do executes req and returns the structured response. Network and policy
// errors are reported in HTTPResponse.Error (Status stays 0) so plugins can
// pattern-match failures without Go error plumbing through goja.
func (b *HTTPBridge) Do(req HTTPRequest) HTTPResponse {
	if err := b.validateURL(req.URL); err != nil {
		return HTTPResponse{Error: err.Error()}
	}
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = http.MethodGet
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var body io.Reader
	if req.Body != "" {
		body = bytes.NewBufferString(req.Body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, req.URL, body)
	if err != nil {
		return HTTPResponse{Error: fmt.Sprintf("build request: %v", err)}
	}
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := b.client.Do(httpReq)
	if err != nil {
		return HTTPResponse{Error: err.Error()}
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxHTTPBody))
	if err != nil {
		return HTTPResponse{Status: resp.StatusCode, Error: fmt.Sprintf("read body: %v", err)}
	}
	return HTTPResponse{
		Status:  resp.StatusCode,
		Headers: flattenHeaders(resp.Header),
		Body:    string(data),
	}
}

// validateURL enforces https-only, except loopback http for local providers.
func (b *HTTPBridge) validateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid url: %v", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return nil
	case "http":
		if b.allowLoopbackHTTP && isLoopbackHost(u.Hostname()) {
			return nil
		}
		return fmt.Errorf("refusing insecure http:// url %q (https required)", raw)
	default:
		return fmt.Errorf("unsupported url scheme %q", u.Scheme)
	}
}

// isLoopbackHost reports whether host resolves to a loopback address.
func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// flattenHeaders converts http.Header to a single-valued map (first value
// wins) suitable for JSON transfer to JS.
func flattenHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, vals := range h {
		if len(vals) > 0 {
			out[strings.ToLower(k)] = vals[0]
		}
	}
	return out
}

// JSONBody marshals v to a JSON string for use as HTTPRequest.Body.
func JSONBody(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(data)
}
