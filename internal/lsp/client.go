// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package lsp implements a minimal Language Server Protocol client for Go
// diagnostics. It currently targets gopls but is designed so additional
// language servers can be supported by supplying a different Server process.
package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"sync"
	"sync/atomic"
)

// Client is a JSON-RPC 2.0 LSP client connected to a language server.
type Client struct {
	conn   net.Conn
	reader *bufio.Reader
	mu     sync.Mutex
	// writeMu serializes writes to the connection.
	writeMu sync.Mutex
	// nextID is the next JSON-RPC request id.
	nextID int64
	// pending maps request IDs to response channels.
	pending map[int64]chan *rpcResponse
	// notifyHandlers map method names to notification handlers.
	notifyHandlers map[string]func(params json.RawMessage)
	// closed is set to true after Close is called.
	closed atomic.Bool
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type rpcResponse struct {
	Result json.RawMessage
	Error  *rpcError
}

// NewClient creates an LSP client over the provided connection. The caller
// is responsible for starting the read loop (ReadNotifications).
func NewClient(conn net.Conn) *Client {
	return &Client{
		conn:           conn,
		reader:         bufio.NewReader(conn),
		pending:        make(map[int64]chan *rpcResponse),
		notifyHandlers: make(map[string]func(params json.RawMessage)),
	}
}

// OnNotification registers a handler for server-side notifications.
func (c *Client) OnNotification(method string, handler func(params json.RawMessage)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.notifyHandlers[method] = handler
}

// request sends a JSON-RPC request and waits for the response.
func (c *Client) request(ctx context.Context, method string, params, result any) error {
	if c.closed.Load() {
		return fmt.Errorf("lsp client is closed")
	}
	id, body, err := c.buildRequest(method, params)
	if err != nil {
		return err
	}
	respCh := c.registerPending(id)
	defer c.unregisterPending(id)
	if err := c.writeMessage(body); err != nil {
		return err
	}
	return c.waitForResponse(ctx, respCh, result)
}

func (c *Client) buildRequest(method string, params any) (int64, []byte, error) {
	id := atomic.AddInt64(&c.nextID, 1)
	req := rpcMessage{JSONRPC: "2.0", ID: id, Method: method}
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return 0, nil, fmt.Errorf("lsp: marshal params: %w", err)
		}
		req.Params = b
	}
	body, err := json.Marshal(req)
	if err != nil {
		return 0, nil, fmt.Errorf("lsp: marshal request: %w", err)
	}
	return id, body, nil
}

func (c *Client) registerPending(id int64) chan *rpcResponse {
	respCh := make(chan *rpcResponse, 1)
	c.mu.Lock()
	c.pending[id] = respCh
	c.mu.Unlock()
	return respCh
}

func (c *Client) unregisterPending(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *Client) waitForResponse(ctx context.Context, respCh chan *rpcResponse, result any) error {
	select {
	case resp := <-respCh:
		return c.handleResponse(resp, result)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) handleResponse(resp *rpcResponse, result any) error {
	if resp == nil {
		return fmt.Errorf("lsp: nil response")
	}
	if resp.Error != nil {
		return fmt.Errorf("lsp: %s (code %d)", resp.Error.Message, resp.Error.Code)
	}
	if result != nil {
		if err := json.Unmarshal(resp.Result, result); err != nil {
			return fmt.Errorf("lsp: unmarshal result: %w", err)
		}
	}
	return nil
}

// notify sends a JSON-RPC notification (no response expected).
func (c *Client) notify(method string, params any) error {
	if c.closed.Load() {
		return fmt.Errorf("lsp client is closed")
	}
	msg := rpcMessage{JSONRPC: "2.0", Method: method}
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("lsp: marshal params: %w", err)
		}
		msg.Params = b
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("lsp: marshal notification: %w", err)
	}
	return c.writeMessage(body)
}

func (c *Client) writeMessage(body []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	if _, err := c.conn.Write([]byte(header)); err != nil {
		return fmt.Errorf("lsp: write header: %w", err)
	}
	if _, err := c.conn.Write(body); err != nil {
		return fmt.Errorf("lsp: write body: %w", err)
	}
	return nil
}

// ReadNotifications blocks reading messages from the server and dispatching
// them to pending requests or notification handlers. It returns when the
// connection is closed or the context is cancelled.
func (c *Client) ReadNotifications(ctx context.Context) error {
	for {
		if c.closed.Load() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		msg, err := c.readMessage()
		if err != nil {
			if c.closed.Load() || err == io.EOF {
				return nil
			}
			return err
		}
		c.dispatch(msg)
	}
}

func (c *Client) readMessage() (*rpcMessage, error) {
	c.mu.Lock()
	reader := c.reader
	c.mu.Unlock()

	tp := textproto.NewReader(reader)
	headers, err := tp.ReadMIMEHeader()
	if err != nil {
		return nil, err
	}
	length := 0
	for _, key := range []string{"Content-Length", "Content-length"} {
		if vals := headers.Values(key); len(vals) > 0 {
			// textproto MIMEHeader returns joined values; parse the first.
			if _, err := fmt.Sscanf(vals[0], "%d", &length); err == nil {
				break
			}
		}
	}
	if length <= 0 {
		return nil, fmt.Errorf("lsp: invalid Content-Length")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, err
	}
	var msg rpcMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		return nil, fmt.Errorf("lsp: unmarshal message: %w", err)
	}
	return &msg, nil
}

func (c *Client) dispatch(msg *rpcMessage) {
	if msg.ID != 0 {
		c.mu.Lock()
		ch, ok := c.pending[msg.ID]
		c.mu.Unlock()
		if ok {
			ch <- &rpcResponse{Result: msg.Result, Error: msg.Error}
		}
		return
	}
	c.mu.Lock()
	handler, ok := c.notifyHandlers[msg.Method]
	c.mu.Unlock()
	if ok {
		handler(msg.Params)
	}
}

// Close shuts down the connection.
func (c *Client) Close() error {
	c.closed.Store(true)
	return c.conn.Close()
}

// IsClosed reports whether the client has been closed.
func (c *Client) IsClosed() bool {
	return c.closed.Load()
}

// Initialize sends the LSP initialize request.
func (c *Client) Initialize(ctx context.Context, params InitializeParams) (InitializeResult, error) {
	var result InitializeResult
	err := c.request(ctx, "initialize", params, &result)
	return result, err
}

// Initialized sends the LSP initialized notification.
func (c *Client) Initialized(params InitializedParams) error {
	return c.notify("initialized", params)
}

// DidOpen sends textDocument/didOpen.
func (c *Client) DidOpen(params DidOpenTextDocumentParams) error {
	return c.notify("textDocument/didOpen", params)
}

// DidChange sends textDocument/didChange.
func (c *Client) DidChange(params DidChangeTextDocumentParams) error {
	return c.notify("textDocument/didChange", params)
}

// Shutdown sends the shutdown request.
func (c *Client) Shutdown(ctx context.Context) error {
	return c.request(ctx, "shutdown", nil, nil)
}

// Exit sends the exit notification.
func (c *Client) Exit() error {
	return c.notify("exit", nil)
}

// InitializeParams is the request payload for initialize.
type InitializeParams struct {
	ProcessID int             `json:"processId"`
	RootURI   string          `json:"rootUri"`
	Capabilities any          `json:"capabilities"`
	Trace     string          `json:"trace,omitempty"`
}

// InitializeResult is the response payload for initialize.
type InitializeResult struct {
	Capabilities ServerCapabilities `json:"capabilities"`
	ServerInfo   *ServerInfo        `json:"serverInfo,omitempty"`
}

// ServerInfo describes the language server.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// ServerCapabilities is a subset of LSP server capabilities.
type ServerCapabilities struct {
	TextDocumentSync           any `json:"textDocumentSync,omitempty"`
	DefinitionProvider         bool `json:"definitionProvider,omitempty"`
	HoverProvider              bool `json:"hoverProvider,omitempty"`
	DocumentSymbolProvider     bool `json:"documentSymbolProvider,omitempty"`
	WorkspaceSymbolProvider    bool `json:"workspaceSymbolProvider,omitempty"`
}

// InitializedParams is the notification payload for initialized.
type InitializedParams struct{}

// DidOpenTextDocumentParams is the notification payload for didOpen.
type DidOpenTextDocumentParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

// TextDocumentItem represents a document opened on the server.
type TextDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

// DidChangeTextDocumentParams is the notification payload for didChange.
type DidChangeTextDocumentParams struct {
	TextDocument   VersionedTextDocumentIdentifier `json:"textDocument"`
	ContentChanges []TextDocumentContentChangeEvent  `json:"contentChanges"`
}

// VersionedTextDocumentIdentifier identifies a document and version.
type VersionedTextDocumentIdentifier struct {
	URI     string `json:"uri"`
	Version int    `json:"version"`
}

// TextDocumentContentChangeEvent describes a content change.
type TextDocumentContentChangeEvent struct {
	Text string `json:"text"`
}
