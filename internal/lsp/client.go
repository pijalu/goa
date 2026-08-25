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
	notifyHandlers  map[string]func(params json.RawMessage)
	requestHandlers map[string]func(params json.RawMessage) any
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
		conn:            conn,
		reader:          bufio.NewReader(conn),
		pending:         make(map[int64]chan *rpcResponse),
		notifyHandlers:  make(map[string]func(params json.RawMessage)),
		requestHandlers: make(map[string]func(params json.RawMessage) any),
	}
}

// OnNotification registers a handler for server-side notifications.
func (c *Client) OnNotification(method string, handler func(params json.RawMessage)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.notifyHandlers[method] = handler
}

// OnRequest registers a handler for requests initiated by the language server.
// Returning nil produces a JSON null result, suitable for optional client
// features such as workspace/configuration when no settings are available.
func (c *Client) OnRequest(method string, handler func(params json.RawMessage) any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requestHandlers[method] = handler
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

func (c *Client) writeResponse(id int64, result []byte) error {
	body, err := json.Marshal(rpcMessage{JSONRPC: "2.0", ID: id, Result: result})
	if err != nil {
		return err
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
	// Requests have a method; responses do not. Server request IDs may collide
	// with client request IDs, so classify by method before consulting pending.
	if msg.ID != 0 && msg.Method != "" {
		c.mu.Lock()
		handler := c.requestHandlers[msg.Method]
		c.mu.Unlock()
		// Optional server requests must always receive a response. Returning
		// JSON null for an unsupported hook prevents servers (notably gopls,
		// which may dynamically register watched files) from blocking all later
		// requests while waiting for a client acknowledgement.
		var value any
		if handler != nil {
			value = handler(msg.Params)
		}
		result, err := json.Marshal(value)
		if err == nil {
			_ = c.writeResponse(msg.ID, result)
		}
		return
	}
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

// Diagnostic requests pull diagnostics when a server advertises the pull
// protocol. A nil result is treated as an empty report by callers.
func (c *Client) Diagnostic(ctx context.Context, params DocumentDiagnosticParams) (DocumentDiagnosticReport, error) {
	var result DocumentDiagnosticReport
	err := c.request(ctx, "textDocument/diagnostic", params, &result)
	return result, err
}

func (c *Client) DiagnosticRefresh(ctx context.Context) error {
	return c.request(ctx, "workspace/diagnostic/refresh", nil, nil)
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
	ProcessID        int               `json:"processId"`
	RootURI          string            `json:"rootUri"`
	WorkspaceFolders []WorkspaceFolder `json:"workspaceFolders,omitempty"`
	Capabilities     any               `json:"capabilities"`
	// InitializationOptions are server-specific settings sent at initialize
	// (e.g. pythonPath for pyright). Nil/empty is omitted.
	InitializationOptions map[string]any `json:"initializationOptions,omitempty"`
	Trace                 string         `json:"trace,omitempty"`
}

// WorkspaceFolder identifies a workspace root.
type WorkspaceFolder struct {
	URI  string `json:"uri"`
	Name string `json:"name"`
}

// RegistrationParams and UnregistrationParams model dynamic client capability
// negotiation. Registrations are retained by the manager for later feature use.
type RegistrationParams struct {
	Registrations []Registration `json:"registrations"`
}

type Registration struct {
	ID              string `json:"id"`
	Method          string `json:"method"`
	RegisterOptions any    `json:"registerOptions,omitempty"`
}

type UnregistrationParams struct {
	Unregisterations []Unregistration `json:"unregisterations"`
}

type Unregistration struct {
	ID     string `json:"id"`
	Method string `json:"method"`
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

// ServerCapabilities is a subset of LSP server capabilities. Provider flags
// are `any` because the spec allows `boolean | object` (e.g. pyright sends
// {"workDoneProgress":true}); a strict bool broke every pyright initialize
// handshake (Issue LSP). The values are informational only.
type ServerCapabilities struct {
	TextDocumentSync           any `json:"textDocumentSync,omitempty"`
	DefinitionProvider         any `json:"definitionProvider,omitempty"`
	HoverProvider              any `json:"hoverProvider,omitempty"`
	DocumentSymbolProvider     any `json:"documentSymbolProvider,omitempty"`
	WorkspaceSymbolProvider    any `json:"workspaceSymbolProvider,omitempty"`
	DiagnosticProvider         any `json:"diagnosticProvider,omitempty"`
	ImplementationProvider     any `json:"implementationProvider,omitempty"`
	ReferencesProvider         any `json:"referencesProvider,omitempty"`
	CallHierarchyProvider      any `json:"callHierarchyProvider,omitempty"`
	CompletionProvider         any `json:"completionProvider,omitempty"`
	CodeActionProvider         any `json:"codeActionProvider,omitempty"`
	RenameProvider             any `json:"renameProvider,omitempty"`
	DocumentFormattingProvider any `json:"documentFormattingProvider,omitempty"`
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
	TextDocument   VersionedTextDocumentIdentifier  `json:"textDocument"`
	ContentChanges []TextDocumentContentChangeEvent `json:"contentChanges"`
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

// TextDocumentPositionParams identifies a position within a document, the
// common payload for definition/hover/references-style requests.
type TextDocumentPositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// WorkspaceSymbolParams queries symbols across the workspace.
type WorkspaceSymbolParams struct {
	Query string `json:"query"`
}

// CallHierarchyPrepareParams identifies the symbol whose call hierarchy is requested.
type CallHierarchyPrepareParams struct{ TextDocumentPositionParams }

// CallHierarchyItem is a symbol participating in a call hierarchy.
type CallHierarchyItem struct {
	Name           string `json:"name"`
	Kind           int    `json:"kind"`
	URI            string `json:"uri"`
	Range          Range  `json:"range"`
	SelectionRange Range  `json:"selectionRange"`
	Detail         string `json:"detail,omitempty"`
}

type CallHierarchyIncomingCall struct {
	From       CallHierarchyItem `json:"from"`
	FromRanges []Range           `json:"fromRanges"`
}

type CallHierarchyOutgoingCall struct {
	To         CallHierarchyItem `json:"to"`
	FromRanges []Range           `json:"fromRanges"`
}

// WorkspaceSymbol is the flat symbol representation returned by workspaceSymbol.
type WorkspaceSymbol struct {
	Name          string   `json:"name"`
	Kind          int      `json:"kind"`
	Location      Location `json:"location"`
	ContainerName string   `json:"containerName,omitempty"`
}

// TextDocumentIdentifier identifies a document by URI.
type TextDocumentIdentifier struct {
	URI string `json:"uri"`
}

// TextEdit replaces a range with new text.
type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}
type RenameParams struct {
	TextDocumentPositionParams
	NewName string `json:"newName"`
}
type PrepareRenameResult struct {
	Range       Range  `json:"range"`
	Placeholder string `json:"placeholder,omitempty"`
}
type FormattingOptions struct {
	TabSize      int  `json:"tabSize"`
	InsertSpaces bool `json:"insertSpaces"`
}
type CompletionParams struct{ TextDocumentPositionParams }
type CodeActionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Range        Range                  `json:"range"`
	Context      CodeActionContext      `json:"context"`
}
type CodeActionContext struct {
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
}
type CodeAction struct {
	Title string         `json:"title"`
	Kind  string         `json:"kind,omitempty"`
	Edit  *WorkspaceEdit `json:"edit,omitempty"`
}

// WorkspaceEdit describes edits grouped by URI or by versioned documents.
// LSP permits either form; servers commonly use documentChanges for rename.
type WorkspaceEdit struct {
	Changes         map[string][]TextEdit `json:"changes,omitempty"`
	DocumentChanges []TextDocumentEdit    `json:"documentChanges,omitempty"`
}

// TextDocumentEdit contains edits for one document in documentChanges.
type TextDocumentEdit struct {
	TextDocument VersionedTextDocumentIdentifier `json:"textDocument"`
	Edits        []TextEdit                      `json:"edits"`
}

type CompletionItem struct {
	Label      string `json:"label"`
	Detail     string `json:"detail,omitempty"`
	InsertText string `json:"insertText,omitempty"`
}

// Location is an LSP location: a URI plus a range.
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// ReferenceParams is the payload for textDocument/references.
type ReferenceParams struct {
	TextDocumentPositionParams
	Context ReferenceContext `json:"context"`
}

// ReferenceContext controls reference lookup (include the declaration?).
type ReferenceContext struct {
	IncludeDeclaration bool `json:"includeDeclaration"`
}

// Hover is the textDocument/hover result. Contents may be a string, a marked
// string, or markdown markup content; we keep it permissive.
type Hover struct {
	Contents any    `json:"contents"`
	Range    *Range `json:"range,omitempty"`
}

// DocumentSymbolParams is the payload for textDocument/documentSymbol.
type DocumentSymbolParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// DocumentSymbol is a symbol in a document (hierarchical; children may nest).
type DocumentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail,omitempty"`
	Kind           int              `json:"kind"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children,omitempty"`
}

// Definition sends textDocument/definition and returns the definition
// locations of the symbol at the given position.
func (c *Client) PrepareRename(ctx context.Context, params TextDocumentPositionParams) (*PrepareRenameResult, error) {
	var result *PrepareRenameResult
	err := c.request(ctx, "textDocument/prepareRename", params, &result)
	return result, err
}
func (c *Client) Rename(ctx context.Context, params RenameParams) (*WorkspaceEdit, error) {
	var result *WorkspaceEdit
	err := c.request(ctx, "textDocument/rename", params, &result)
	return result, err
}
func (c *Client) Completion(ctx context.Context, params CompletionParams) ([]CompletionItem, error) {
	var result []CompletionItem
	err := c.request(ctx, "textDocument/completion", params, &result)
	return result, err
}
func (c *Client) CodeAction(ctx context.Context, params CodeActionParams) ([]CodeAction, error) {
	var result []CodeAction
	err := c.request(ctx, "textDocument/codeAction", params, &result)
	return result, err
}
func (c *Client) Formatting(ctx context.Context, id TextDocumentIdentifier, options FormattingOptions) ([]TextEdit, error) {
	var result []TextEdit
	err := c.request(ctx, "textDocument/formatting", map[string]any{"textDocument": id, "options": options}, &result)
	return result, err
}

func (c *Client) Definition(ctx context.Context, params TextDocumentPositionParams) ([]Location, error) {
	var result []Location
	err := c.request(ctx, "textDocument/definition", params, &result)
	return result, err
}

// References sends textDocument/references and returns all reference locations
// of the symbol at the given position (declaration included).
func (c *Client) References(ctx context.Context, params ReferenceParams) ([]Location, error) {
	var result []Location
	err := c.request(ctx, "textDocument/references", params, &result)
	return result, err
}

// Hover sends textDocument/hover and returns the hover information for the
// symbol at the given position. A nil *Hover (with nil error) means no info.
func (c *Client) Hover(ctx context.Context, params TextDocumentPositionParams) (*Hover, error) {
	var result *Hover
	err := c.request(ctx, "textDocument/hover", params, &result)
	return result, err
}

// DocumentSymbol sends textDocument/documentSymbol and returns the symbols
// defined in the document.
func (c *Client) DocumentSymbol(ctx context.Context, params DocumentSymbolParams) ([]DocumentSymbol, error) {
	var result []DocumentSymbol
	err := c.request(ctx, "textDocument/documentSymbol", params, &result)
	return result, err
}

func (c *Client) Implementation(ctx context.Context, params TextDocumentPositionParams) ([]Location, error) {
	var result []Location
	err := c.request(ctx, "textDocument/implementation", params, &result)
	return result, err
}

func (c *Client) WorkspaceSymbol(ctx context.Context, params WorkspaceSymbolParams) ([]WorkspaceSymbol, error) {
	var result []WorkspaceSymbol
	err := c.request(ctx, "workspace/symbol", params, &result)
	return result, err
}

func (c *Client) PrepareCallHierarchy(ctx context.Context, params CallHierarchyPrepareParams) ([]CallHierarchyItem, error) {
	var result []CallHierarchyItem
	err := c.request(ctx, "textDocument/prepareCallHierarchy", params, &result)
	return result, err
}

func (c *Client) IncomingCalls(ctx context.Context, item CallHierarchyItem) ([]CallHierarchyIncomingCall, error) {
	var result []CallHierarchyIncomingCall
	err := c.request(ctx, "callHierarchy/incomingCalls", map[string]any{"item": item}, &result)
	return result, err
}

func (c *Client) OutgoingCalls(ctx context.Context, item CallHierarchyItem) ([]CallHierarchyOutgoingCall, error) {
	var result []CallHierarchyOutgoingCall
	err := c.request(ctx, "callHierarchy/outgoingCalls", map[string]any{"item": item}, &result)
	return result, err
}
