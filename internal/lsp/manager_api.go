// SPDX-License-Identifier: GPL-3.0-or-later

package lsp

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// notifyOpen marks uri open at version 1 and sends didOpen. When a concurrent
// opener already opened uri, it degrades to a full-content didChange instead
// of a protocol-violating duplicate didOpen. The client mutex is held across
// the send so wire order matches version order (the JSON-RPC client already
// serializes writes via its writeMu, so this adds no real contention).
func (c *serverClient) notifyOpen(uri, languageID, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, opened := c.versions[uri]; opened {
		return c.didChangeLocked(uri, v, text)
	}
	c.versions[uri] = 1
	return c.server.Client().DidOpen(DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{
			URI:        uri,
			LanguageID: languageID,
			Version:    1,
			Text:       text,
		},
	})
}

// notifyChange bumps uri's version and sends didChange, opening the document
// first when it is not open yet (so a bare DidChange still works).
func (c *serverClient) notifyChange(uri, languageID, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, opened := c.versions[uri]
	if !opened {
		c.versions[uri] = 1
		return c.server.Client().DidOpen(DidOpenTextDocumentParams{
			TextDocument: TextDocumentItem{
				URI:        uri,
				LanguageID: languageID,
				Version:    1,
				Text:       text,
			},
		})
	}
	return c.didChangeLocked(uri, v, text)
}

// didChangeLocked sends a full-content didChange with the next version.
// Callers must hold c.mu.
func (c *serverClient) didChangeLocked(uri string, version int, text string) error {
	version++
	c.versions[uri] = version
	return c.server.Client().DidChange(DidChangeTextDocumentParams{
		TextDocument:   VersionedTextDocumentIdentifier{URI: uri, Version: version},
		ContentChanges: []TextDocumentContentChangeEvent{{Text: text}},
	})
}

// isOpen reports whether uri has been opened on this client's server.
func (c *serverClient) isOpen(uri string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, opened := c.versions[uri]
	return opened
}

// OpenDocument notifies the appropriate server that a document was opened.
// It never blocks: when the server is still starting (or unavailable) the
// notification is dropped and a descriptive error returned — file tools
// ignore it and the next touch re-opens the document once the server is up
// (notifyChange self-heals by opening unopened documents).
func (m *Manager) OpenDocument(ctx context.Context, path, text string) error {
	c := m.clientFor(path)
	if c == nil {
		return m.noClientError(path)
	}
	uri := uriFor(path)
	c.mu.Lock()
	version := c.versions[uri] + 1
	c.mu.Unlock()
	m.diags.MarkPending(uri, version)
	return c.notifyOpen(uri, c.spec.languageID(path), text)
}

// DidChange notifies the appropriate server of a content change, opening the
// document first if it was not open (so a bare DidChange still works). Like
// OpenDocument it never blocks on a starting server (Read stuck).
func (m *Manager) DidChange(ctx context.Context, path, text string) error {
	c := m.clientFor(path)
	if c == nil {
		return m.noClientError(path)
	}
	uri := uriFor(path)
	c.mu.Lock()
	version := c.versions[uri] + 1
	c.mu.Unlock()
	m.diags.MarkPending(uri, version)
	return c.notifyChange(uri, c.spec.languageID(path), text)
}

// noClientError describes why no client is available for path: unsupported
// file type, server still starting, or server failed to start.
func (m *Manager) noClientError(path string) error {
	_, key, _, ok := m.lookup(path)
	if !ok {
		return fmt.Errorf("lsp manager: no server for %s", path)
	}
	m.mu.Lock()
	_, starting := m.spawning[key]
	_, broken := m.broken[key]
	m.mu.Unlock()
	switch {
	case starting:
		return fmt.Errorf("lsp manager: server for %s is still starting", path)
	case broken:
		m.mu.Lock()
		err := m.broken[key]
		m.mu.Unlock()
		return fmt.Errorf("lsp manager: server for %s failed to start: %v", path, err)
	default:
		return fmt.Errorf("lsp manager: no server for %s", path)
	}
}

// DiagnosticsFor returns the latest diagnostics published for a file path.
func (m *Manager) DiagnosticsFor(ctx context.Context, path string) []Diagnostic {
	if m == nil {
		return nil
	}
	return m.diags.Get(uriFor(path))
}

// WaitDiagnostics waits for an explicit publication at or after the document
// version requested by the most recent open/change.
func (m *Manager) WaitDiagnostics(ctx context.Context, path string) DiagnosticSnapshot {
	if m == nil {
		return DiagnosticSnapshot{}
	}
	uri := uriFor(path)
	snap := m.diags.Snapshot(uri)
	return m.diags.Wait(ctx, uri, snap.Pending)
}

// PullDiagnostics requests a fresh report from servers implementing the pull
// diagnostic extension. Push-only servers simply return an unavailable error.
func (m *Manager) PullDiagnostics(ctx context.Context, path string) (DiagnosticSnapshot, error) {
	c := m.waitClientFor(ctx, path)
	if c == nil {
		return DiagnosticSnapshot{}, m.noClientError(path)
	}
	uri := uriFor(path)
	c.mu.Lock()
	version := c.versions[uri]
	previousResultID := c.resultIDs[uri]
	c.mu.Unlock()
	report, err := c.server.Client().Diagnostic(ctx, DocumentDiagnosticParams{
		TextDocument:     TextDocumentIdentifier{URI: uri},
		PreviousResultID: previousResultID,
	})
	if err != nil {
		return DiagnosticSnapshot{}, err
	}
	// An unchanged pull report refers to the previous result ID and must not
	// erase the last known diagnostics. A full report, including an empty items
	// list, is an explicit clean publication.
	if report.Kind != "unchanged" {
		m.diags.SetVersion(uri, version, report.Items)
	}
	c.mu.Lock()
	if report.ResultID != "" {
		c.resultIDs[uri] = report.ResultID
	}
	c.mu.Unlock()
	return m.diags.Snapshot(uri), nil
}

// ServerIDFor returns the id of the language server that handles path
// (e.g. "gopls", "pyright"), or "" when no server supports the file type.
// Used to label diagnostics with the actual source server instead of a
// hardcoded "gopls" (Issue LSP — py/js files reported as gopls).
func (m *Manager) ServerIDFor(path string) string {
	spec, _, _, ok := m.lookup(path)
	if !ok {
		return ""
	}
	return spec.ID
}

// HasErrors reports whether any tracked file has an error-level diagnostic.
func (m *Manager) HasErrors() bool {
	if m == nil {
		return false
	}
	return m.diags.HasErrors()
}

// ensureOpen makes sure the document is open on its server before a position
// request, opening it (with on-disk contents) if needed. Unlike the fire-and-
// forget touch path it WAITS for a starting server (bounded by ctx) because
// the model explicitly asked for a navigation answer.
func (m *Manager) ensureOpen(ctx context.Context, path string) (*serverClient, error) {
	c := m.waitClientFor(ctx, path)
	if c == nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("lsp manager: waiting for server for %s: %w", path, ctx.Err())
		}
		return nil, m.noClientError(path)
	}
	uri := uriFor(path)
	if c.isOpen(uri) {
		return c, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("lsp manager: read %s: %w", path, err)
	}
	if err := m.OpenDocument(ctx, path, string(data)); err != nil {
		return nil, err
	}
	return c, nil
}

// Definition returns the definition locations of the symbol at line/character
// (zero-indexed) in the given file.
func (m *Manager) Definition(ctx context.Context, path string, line, character int) ([]Location, error) {
	c, err := m.ensureOpen(ctx, path)
	if err != nil {
		return nil, err
	}
	return c.server.Client().Definition(ctx, TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: uriFor(path)},
		Position:     Position{Line: line, Character: character},
	})
}

// References returns the reference locations of the symbol at line/character
// (zero-indexed) in the given file, including its declaration.
func (m *Manager) References(ctx context.Context, path string, line, character int) ([]Location, error) {
	c, err := m.ensureOpen(ctx, path)
	if err != nil {
		return nil, err
	}
	return c.server.Client().References(ctx, ReferenceParams{
		TextDocumentPositionParams: TextDocumentPositionParams{
			TextDocument: TextDocumentIdentifier{URI: uriFor(path)},
			Position:     Position{Line: line, Character: character},
		},
		Context: ReferenceContext{IncludeDeclaration: true},
	})
}

// Hover returns the hover information for the symbol at line/character
// (zero-indexed) in the given file. Nil (with nil error) means no info.
func (m *Manager) Hover(ctx context.Context, path string, line, character int) (*Hover, error) {
	c, err := m.ensureOpen(ctx, path)
	if err != nil {
		return nil, err
	}
	return c.server.Client().Hover(ctx, TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: uriFor(path)},
		Position:     Position{Line: line, Character: character},
	})
}

// DocumentSymbols returns the symbols defined in the given file.
func (m *Manager) DocumentSymbols(ctx context.Context, path string) ([]DocumentSymbol, error) {
	c, err := m.ensureOpen(ctx, path)
	if err != nil {
		return nil, err
	}
	return c.server.Client().DocumentSymbol(ctx, DocumentSymbolParams{TextDocument: TextDocumentIdentifier{URI: uriFor(path)}})
}

// PrepareRename validates that a symbol can be renamed and returns its range.
func (m *Manager) PrepareRename(ctx context.Context, path string, line, character int) (*PrepareRenameResult, error) {
	c, err := m.ensureOpen(ctx, path)
	if err != nil {
		return nil, err
	}
	return c.server.Client().PrepareRename(ctx, TextDocumentPositionParams{TextDocument: TextDocumentIdentifier{URI: uriFor(path)}, Position: Position{Line: line, Character: character}})
}

// Rename requests a multi-file workspace edit from the language server.
func (m *Manager) Rename(ctx context.Context, path string, line, character int, newName string) (*WorkspaceEdit, error) {
	c, err := m.ensureOpen(ctx, path)
	if err != nil {
		return nil, err
	}
	return c.server.Client().Rename(ctx, RenameParams{TextDocumentPositionParams: TextDocumentPositionParams{TextDocument: TextDocumentIdentifier{URI: uriFor(path)}, Position: Position{Line: line, Character: character}}, NewName: newName})
}

// Completion returns completion items at a position.
func (m *Manager) Completion(ctx context.Context, path string, line, character int) ([]CompletionItem, error) {
	c, err := m.ensureOpen(ctx, path)
	if err != nil {
		return nil, err
	}
	return c.server.Client().Completion(ctx, CompletionParams{TextDocumentPositionParams: TextDocumentPositionParams{TextDocument: TextDocumentIdentifier{URI: uriFor(path)}, Position: Position{Line: line, Character: character}}})
}

// CodeAction requests actions for a range.
func (m *Manager) CodeAction(ctx context.Context, path string, r Range) ([]CodeAction, error) {
	c, err := m.ensureOpen(ctx, path)
	if err != nil {
		return nil, err
	}
	return c.server.Client().CodeAction(ctx, CodeActionParams{TextDocument: TextDocumentIdentifier{URI: uriFor(path)}, Range: r})
}

// Formatting requests whole-document formatting edits.
func (m *Manager) Formatting(ctx context.Context, path string, options FormattingOptions) ([]TextEdit, error) {
	c, err := m.ensureOpen(ctx, path)
	if err != nil {
		return nil, err
	}
	return c.server.Client().Formatting(ctx, TextDocumentIdentifier{URI: uriFor(path)}, options)
}

// SupportsPath reports whether an enabled registry server handles path.
func (m *Manager) SupportsPath(path string) bool { _, _, _, ok := m.lookup(path); return ok }

func (m *Manager) Implementation(ctx context.Context, path string, line, character int) ([]Location, error) {
	c, err := m.ensureOpen(ctx, path)
	if err != nil {
		return nil, err
	}
	return c.server.Client().Implementation(ctx, TextDocumentPositionParams{TextDocument: TextDocumentIdentifier{URI: uriFor(path)}, Position: Position{Line: line, Character: character}})
}

func (m *Manager) WorkspaceSymbols(ctx context.Context, path, query string) ([]WorkspaceSymbol, error) {
	if _, err := m.ensureOpen(ctx, path); err != nil {
		return nil, err
	}
	m.mu.Lock()
	clients := make([]*serverClient, 0, len(m.clients))
	for _, c := range m.clients {
		clients = append(clients, c)
	}
	m.mu.Unlock()
	var out []WorkspaceSymbol
	seen := make(map[string]struct{})
	for _, c := range clients {
		items, err := c.server.Client().WorkspaceSymbol(ctx, WorkspaceSymbolParams{Query: query})
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			key := item.Name + "|" + item.Location.URI + "|" + fmt.Sprint(item.Location.Range.Start.Line, ":", item.Location.Range.Start.Character)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, item)
		}
	}
	return out, nil
}

func (m *Manager) PrepareCallHierarchy(ctx context.Context, path string, line, character int) ([]CallHierarchyItem, error) {
	c, err := m.ensureOpen(ctx, path)
	if err != nil {
		return nil, err
	}
	return c.server.Client().PrepareCallHierarchy(ctx, CallHierarchyPrepareParams{TextDocumentPositionParams: TextDocumentPositionParams{TextDocument: TextDocumentIdentifier{URI: uriFor(path)}, Position: Position{Line: line, Character: character}}})
}

func (m *Manager) IncomingCalls(ctx context.Context, path string, line, character int) ([]CallHierarchyIncomingCall, error) {
	items, err := m.PrepareCallHierarchy(ctx, path, line, character)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return []CallHierarchyIncomingCall{}, nil
	}
	c, err := m.ensureOpen(ctx, path)
	if err != nil {
		return nil, err
	}
	return c.server.Client().IncomingCalls(ctx, items[0])
}

func (m *Manager) OutgoingCalls(ctx context.Context, path string, line, character int) ([]CallHierarchyOutgoingCall, error) {
	items, err := m.PrepareCallHierarchy(ctx, path, line, character)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return []CallHierarchyOutgoingCall{}, nil
	}
	c, err := m.ensureOpen(ctx, path)
	if err != nil {
		return nil, err
	}
	return c.server.Client().OutgoingCalls(ctx, items[0])
}

// Close shuts down every running language server and cancels the lifecycle
// context so no new server spawns after close.
func (m *Manager) Close(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	if m.lifecycleCancel != nil {
		m.lifecycleCancel()
	}
	clients := make([]*serverClient, 0, len(m.clients))
	for _, c := range m.clients {
		clients = append(clients, c)
	}
	m.clients = make(map[string]*serverClient)
	m.mu.Unlock()
	var firstErr error
	for _, c := range clients {
		if err := c.server.Close(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// ClientState describes the lifecycle of one server workspace client.
type ClientState string

const (
	ClientStarting  ClientState = "starting"
	ClientConnected ClientState = "connected"
	ClientError     ClientState = "error"
	ClientBroken    ClientState = "broken"
)

// ClientStatus is a structured, machine-readable server lifecycle snapshot.
type ClientStatus struct {
	ServerID string      `json:"serverId"`
	Name     string      `json:"name"`
	Root     string      `json:"root"`
	State    ClientState `json:"state"`
	Error    string      `json:"error,omitempty"`
}

// Statuses reports all configured client lifecycle entries.
func (m *Manager) Statuses() []ClientStatus {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ClientStatus, 0, len(m.clients)+len(m.spawning)+len(m.broken))
	for _, c := range m.clients {
		out = append(out, ClientStatus{ServerID: c.spec.ID, Name: c.spec.ID, Root: c.root, State: ClientConnected})
	}
	for key := range m.spawning {
		id, root := splitClientKey(key)
		out = append(out, ClientStatus{ServerID: id, Name: id, Root: root, State: ClientStarting})
	}
	for key, err := range m.broken {
		id, root := splitClientKey(key)
		out = append(out, ClientStatus{ServerID: id, Name: id, Root: root, State: ClientBroken, Error: err.Error()})
	}
	return out
}

func splitClientKey(key string) (string, string) {
	idx := strings.IndexByte(key, '|')
	if idx < 0 {
		return key, ""
	}
	return key[:idx], key[idx+1:]
}

// HasClients reports whether a running client is available for path.
func (m *Manager) HasClients(path string) bool {
	if m == nil {
		return false
	}
	_, key, _, ok := m.lookup(path)
	if !ok {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, exists := m.clients[key]
	return exists
}

// Status reports which servers are running (and starting), for diagnostics/UI.
func (m *Manager) Status() string {
	if m == nil {
		return "lsp: none"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var ids []string
	for _, c := range m.clients {
		ids = append(ids, c.spec.ID)
	}
	for key := range m.spawning {
		if idx := strings.IndexByte(key, '|'); idx > 0 {
			ids = append(ids, key[:idx]+"(starting)")
		}
	}
	if len(ids) == 0 {
		return "lsp: none"
	}
	return "lsp: " + strings.Join(ids, ",")
}
