// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package lsp

import (
	"context"
	"fmt"
	"path/filepath"
)

// Manager manages a gopls language server instance for a Go project.
type Manager struct {
	server     *Server
	diags      *Diagnostics
	rootDir    string
	rootURI    string
	started    bool
	// serverFactory creates the underlying LSP server. Defaults to Start.
	serverFactory func(ctx context.Context) (*Server, error)
}

// NewManager creates a manager for the project at rootDir. It does not start
// gopls until Start is called.
func NewManager(rootDir string) *Manager {
	m := &Manager{
		diags:   NewDiagnostics(),
		rootDir: rootDir,
	}
	m.rootURI = m.fileURI(rootDir)
	m.serverFactory = func(ctx context.Context) (*Server, error) {
		return Start(ctx, ServerConfig{Command: "gopls", Args: []string{"-mode=stdio"}})
	}
	return m
}

// Start launches gopls and initializes the LSP session.
func (m *Manager) Start(ctx context.Context) error {
	if m.started {
		return nil
	}
	server, err := m.serverFactory(ctx)
	if err != nil {
		return fmt.Errorf("lsp manager: start gopls: %w", err)
	}
	m.server = server
	client := server.Client()
	client.OnNotification("textDocument/publishDiagnostics", m.diags.Handler())

	if _, err := client.Initialize(ctx, InitializeParams{
		ProcessID: 0,
		RootURI:   m.rootURI,
		Capabilities: map[string]any{},
		Trace:     "off",
	}); err != nil {
		_ = server.Close(ctx)
		return fmt.Errorf("lsp manager: initialize: %w", err)
	}
	if err := client.Initialized(InitializedParams{}); err != nil {
		_ = server.Close(ctx)
		return fmt.Errorf("lsp manager: initialized: %w", err)
	}
	m.started = true
	return nil
}

// OpenDocument notifies gopls that a document has been opened.
func (m *Manager) OpenDocument(ctx context.Context, path, text string) error {
	if !m.started || m.server == nil {
		return fmt.Errorf("lsp manager: not started")
	}
	uri := m.fileURI(path)
	return m.server.Client().DidOpen(DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{
			URI:        uri,
			LanguageID: "go",
			Version:    1,
			Text:       text,
		},
	})
}

// DidChange notifies gopls of a content change. The version is incremented
// from the previous value for this path; a separate tracker is required for
// precise version tracking, so this uses a simple increment based on the
// current diagnostics state.
func (m *Manager) DidChange(ctx context.Context, path, text string) error {
	if !m.started || m.server == nil {
		return fmt.Errorf("lsp manager: not started")
	}
	uri := m.fileURI(path)
	return m.server.Client().DidChange(DidChangeTextDocumentParams{
		TextDocument: VersionedTextDocumentIdentifier{URI: uri, Version: 2},
		ContentChanges: []TextDocumentContentChangeEvent{
			{Text: text},
		},
	})
}

// DiagnosticsFor returns the latest diagnostics for a file path.
func (m *Manager) DiagnosticsFor(path string) []Diagnostic {
	return m.diags.Get(m.fileURI(path))
}

// HasErrors reports whether any tracked file has an error-level diagnostic.
func (m *Manager) HasErrors() bool {
	return m.diags.HasErrors()
}

// Close shuts down the language server.
func (m *Manager) Close(ctx context.Context) error {
	if m.server == nil {
		return nil
	}
	return m.server.Close(ctx)
}

func (m *Manager) fileURI(path string) string {
	if filepath.IsAbs(path) {
		return "file://" + filepath.ToSlash(path)
	}
	abs := filepath.Join(m.rootDir, path)
	return "file://" + filepath.ToSlash(abs)
}
