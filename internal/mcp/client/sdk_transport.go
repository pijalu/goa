// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package client

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// StdioOptions configures a stdio MCP server child process.
type StdioOptions struct {
	// Cwd is the child working directory. Empty uses the current directory.
	Cwd string
	// Env is merged over the process environment. Nil inherits the environment.
	Env map[string]string
}

// HTTPOptions configures a remote MCP server connection.
type HTTPOptions struct {
	// HTTPClient overrides the http.Client (mainly for tests). Nil uses a default.
	HTTPClient *http.Client
	// Headers are sent with every request (e.g. Authorization).
	Headers map[string]string
	// Timeout bounds each request. Zero uses the SDK default.
	Timeout time.Duration
}

// NewStdioClient creates a client for a local MCP server spawned as a child
// process over stdio. The options configure the child's working directory and
// environment. It replaces the previous hand-rolled stdio JSON-RPC client with
// the official go-sdk's CommandTransport.
func NewStdioClient(command string, args []string, opts ...StdioOptions) Client {
	cmd := exec.Command(command, args...)
	setProcGroup(cmd)
	if len(opts) > 0 {
		o := opts[0]
		if o.Cwd != "" {
			cmd.Dir = o.Cwd
		}
		if o.Env != nil {
			cmd.Env = mergeEnv(o.Env)
		}
	}
	return &sdkTransportClient{
		transport: &sdk.CommandTransport{Command: cmd},
		cmd:       cmd,
	}
}

// NewHTTPClient creates a client for a remote MCP server. It uses the
// Streamable HTTP transport; when the server does not speak it, Connect falls
// back to the legacy HTTP+SSE transport (OpenCode's two-transport strategy).
func NewHTTPClient(url string, opts HTTPOptions) Client {
	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{}
	}
	if len(opts.Headers) > 0 {
		hc = &http.Client{Transport: headerRoundTripper{base: hc.Transport, headers: opts.Headers}}
	}
	return &sdkTransportClient{
		url:        url,
		httpClient: hc,
	}
}

// headerRoundTripper injects configured headers into every outgoing request.
type headerRoundTripper struct {
	base    http.RoundTripper
	headers map[string]string
}

func (h headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	base := h.base
	if base == nil {
		base = http.DefaultTransport
	}
	clone := req.Clone(req.Context())
	for k, v := range h.headers {
		clone.Header.Set(k, v)
	}
	return base.RoundTrip(clone)
}

// sdkTransportClient is a Client whose transport is chosen at Initialize time
// (stdio is fixed; remote tries Streamable HTTP then SSE). It wraps the shared
// sdkClient session adapter.
type sdkTransportClient struct {
	sdkClient

	// stdio: fixed transport.
	transport sdk.Transport
	// cmd is the child process for stdio transports (nil for remote).
	cmd *exec.Cmd

	// remote: endpoint + http client for transport construction.
	url        string
	httpClient *http.Client
}

// Initialize connects the session, selecting the transport.
func (c *sdkTransportClient) Initialize(ctx context.Context) error {
	if c.transport != nil {
		return c.connect(ctx, c.transport)
	}
	return c.connectRemote(ctx)
}

// connectRemote tries Streamable HTTP first, then legacy SSE on failure.
func (c *sdkTransportClient) connectRemote(ctx context.Context) error {
	streamable := &sdk.StreamableClientTransport{Endpoint: c.url, HTTPClient: c.httpClient}
	if err := c.connect(ctx, streamable); err == nil {
		return nil
	}
	// Fallback to legacy SSE transport.
	sse := &sdk.SSEClientTransport{Endpoint: c.url, HTTPClient: c.httpClient}
	if err := c.connect(ctx, sse); err != nil {
		return fmt.Errorf("mcp remote connect (streamable and sse) failed: %w", err)
	}
	return nil
}

// Close shuts down the client. For stdio transports it also kills any
// remaining descendant processes (the SDK's Close only terminates the
// direct child; grandchildren spawned by the server would be orphaned).
func (c *sdkTransportClient) Close() error {
	err := c.sdkClient.Close()
	if c.cmd != nil && c.cmd.Process != nil {
		killProcessTree(c.cmd.Process.Pid)
	}
	return err
}

// mergeEnv returns os.Environ() overridden by the given key/value pairs.
func mergeEnv(over map[string]string) []string {
	base := os.Environ()
	idx := make(map[string]int, len(base))
	for i, kv := range base {
		for j := 0; j < len(kv); j++ {
			if kv[j] == '=' {
				idx[kv[:j]] = i
				break
			}
		}
	}
	for k, v := range over {
		entry := k + "=" + v
		if i, ok := idx[k]; ok {
			base[i] = entry
		} else {
			base = append(base, entry)
		}
	}
	return base
}
