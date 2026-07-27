// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package lsp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Manager coordinates one language-server client per (server, project-root)
// pair, selecting the right server for each file by extension and spawning it
// lazily on first use (OpenCode's getClients model). It is safe for concurrent
// use.
type Manager struct {
	mu       sync.Mutex
	rootDir  string
	binDir   string
	install  bool
	started  bool
	specs    []ServerSpec
	clients  map[string]*serverClient // key: serverID + "|" + root
	broken   map[string]error         // key: serverID + "|" + root → start error
	diags    *Diagnostics
	spawning map[string]*sync.WaitGroup
	// serverFactory starts a server process; defaults to Start (spawns the real
	// binary). Swappable in tests.
	serverFactory func(ctx context.Context, cfg ServerConfig) (*Server, error)
	// resolve maps a spec to its launch argv; defaults to spec.resolveCommand.
	// Swappable in tests to bypass binary lookup/install.
	resolve func(spec *ServerSpec, binDir string, installAllowed bool) ([]string, bool)
	// lifecycleCtx governs spawned server processes, decoupled from any single
	// query's context: a query ctx cancellation must not kill a long-lived
	// server. Cancelled by Close.
	lifecycleCtx    context.Context
	lifecycleCancel context.CancelFunc
}

// serverClient is one running language server and the files it has open.
type serverClient struct {
	server   *Server
	spec     *ServerSpec
	root     string
	versions map[string]int // uri → latest didOpen/didChange version
}

// Option configures a Manager.
type Option func(*Manager)

// WithBinDir sets the directory installers place binaries into (default
// <rootDir>/.goa/bin).
func WithBinDir(dir string) Option { return func(m *Manager) { m.binDir = dir } }

// WithInstall enables/disables on-demand server installation (default on).
func WithInstall(allowed bool) Option { return func(m *Manager) { m.install = allowed } }

// WithServers overrides the active server specs (default: the embedded builtin
// registry). Pass MergeRegistry(overrides) to apply user config.
func WithServers(specs []ServerSpec) Option { return func(m *Manager) { m.specs = specs } }

// NewManager creates a multi-server LSP manager rooted at rootDir. It does not
// start any language server until a file requiring one is touched.
func NewManager(rootDir string, opts ...Option) *Manager {
	m := &Manager{
		rootDir:       rootDir,
		binDir:        filepath.Join(rootDir, ".goa", "bin"),
		install:       true,
		specs:         Registry(),
		clients:       make(map[string]*serverClient),
		broken:        make(map[string]error),
		diags:         NewDiagnostics(),
		spawning:      make(map[string]*sync.WaitGroup),
		serverFactory: Start,
		resolve: func(spec *ServerSpec, binDir string, installAllowed bool) ([]string, bool) {
			return spec.resolveCommand(binDir, installAllowed)
		},
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

// Start marks the manager started and establishes the lifecycle context that
// governs spawned server processes. Language servers are spawned lazily per
// file, so Start only flips the gate — it does not launch any process.
// Kept for API compatibility with the previous gopls-only manager.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	m.started = true
	m.lifecycleCtx, m.lifecycleCancel = context.WithCancel(context.Background())
	m.mu.Unlock()
	return nil
}

// Started reports whether the manager is enabled. It is nil-safe (bootstrap
// may hold a typed-nil when no project root exists).
func (m *Manager) Started() bool { return m != nil && m.started }

// StartError returns nil in the multi-server model: per-server start failures
// are recorded per root (broken) and surfaced on demand rather than failing
// the whole manager. Kept for the startup banner's API compatibility.
func (m *Manager) StartError() error { return nil }

// clientKey identifies a (server, root) pair.
func clientKey(serverID, root string) string { return serverID + "|" + root }

// clientFor returns the running client for the given file, spawning the server
// on first use. It returns nil (and no error) when no server handles the file
// or the server is unavailable; diagnostics/navigation degrade gracefully.
func (m *Manager) clientFor(ctx context.Context, path string) *serverClient {
	if m == nil || !m.started {
		return nil
	}
	spec := specForFile(m.specs, path)
	if spec == nil {
		return nil
	}
	root := spec.FindRoot(path, m.rootDir)
	key := clientKey(spec.ID, root)

	m.mu.Lock()
	if c, ok := m.clients[key]; ok {
		m.mu.Unlock()
		return c
	}
	if _, broken := m.broken[key]; broken {
		m.mu.Unlock()
		return nil
	}
	// Single-flight: one goroutine spawns, others wait.
	if wg, spawning := m.spawning[key]; spawning {
		m.mu.Unlock()
		wg.Wait()
		m.mu.Lock()
		c := m.clients[key]
		m.mu.Unlock()
		return c
	}
	wg := &sync.WaitGroup{}
	wg.Add(1)
	m.spawning[key] = wg
	m.mu.Unlock()

	c := m.spawn(m.lifecycleCtx, spec, root)

	m.mu.Lock()
	if c != nil {
		m.clients[key] = c
	} else {
		m.broken[key] = fmt.Errorf("lsp: server %s unavailable", spec.ID)
	}
	delete(m.spawning, key)
	m.mu.Unlock()
	wg.Done()
	return c
}

// spawn resolves the server's command and starts it, returning nil when the
// server cannot be launched.
func (m *Manager) spawn(ctx context.Context, spec *ServerSpec, root string) *serverClient {
	argv, ok := m.resolve(spec, m.binDir, m.install)
	if !ok || len(argv) == 0 {
		return nil
	}
	server, err := m.serverFactory(ctx, ServerConfig{
		Command: argv[0],
		Args:    argv[1:],
		RootDir: root,
		Env:     mergeEnv(spec.Env),
	})
	if err != nil {
		return nil
	}
	client := server.Client()
	client.OnNotification("textDocument/publishDiagnostics", m.diags.Handler())
	rootURI := "file://" + filepath.ToSlash(root)
	if _, err := client.Initialize(ctx, InitializeParams{
		ProcessID:             0,
		RootURI:               rootURI,
		Capabilities:          map[string]any{},
		InitializationOptions: initOptions(spec, root),
		Trace:                 "off",
	}); err != nil {
		_ = server.Close(ctx)
		return nil
	}
	if err := client.Initialized(InitializedParams{}); err != nil {
		_ = server.Close(ctx)
		return nil
	}
	return &serverClient{
		server:   server,
		spec:     spec,
		root:     root,
		versions: make(map[string]int),
	}
}

// uriFor converts a path to a file:// URI.
func uriFor(path string) string {
	if filepath.IsAbs(path) {
		return "file://" + filepath.ToSlash(path)
	}
	return "file://" + filepath.ToSlash(path)
}

// mergeEnv returns os.Environ() with the spec's Env overrides applied, or nil
// when there are no overrides (so the child inherits the default environment).
func mergeEnv(overrides map[string]string) []string {
	if len(overrides) == 0 {
		return nil
	}
	env := os.Environ()
	for k, v := range overrides {
		env = append(env, k+"="+v)
	}
	return env
}

// initOptions computes initializationOptions for a server: builtin dynamic
// options (e.g. pyright's virtualenv pythonPath) merged under any user-config
// overrides (user wins).
func initOptions(spec *ServerSpec, root string) map[string]any {
	var out map[string]any
	// Builtin dynamic: point pyright at the project virtualenv interpreter so
	// imports resolve (OpenCode does the same).
	if spec.ID == "pyright" {
		if py := pythonVenv(root); py != "" {
			out = map[string]any{"python": map[string]any{"pythonPath": py}}
		}
	}
	for k, v := range spec.Initialization {
		if out == nil {
			out = map[string]any{}
		}
		out[k] = v
	}
	return out
}

// pythonVenv returns the interpreter path of the project virtualenv, checking
// VIRTUAL_ENV then .venv/venv under root. Returns "" when none exists.
func pythonVenv(root string) string {
	for _, base := range []string{os.Getenv("VIRTUAL_ENV"), filepath.Join(root, ".venv"), filepath.Join(root, "venv")} {
		if base == "" {
			continue
		}
		py := filepath.Join(base, "bin", "python")
		if fileExists(py) {
			return py
		}
	}
	return ""
}

// OpenDocument notifies the appropriate server that a document was opened.
// It is a no-op (nil) when no server handles the file.
func (m *Manager) OpenDocument(ctx context.Context, path, text string) error {
	c := m.clientFor(ctx, path)
	if c == nil {
		return fmt.Errorf("lsp manager: no server for %s", path)
	}
	uri := uriFor(path)
	c.versions[uri] = 1
	return c.server.Client().DidOpen(DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{
			URI:        uri,
			LanguageID: c.spec.languageID(path),
			Version:    1,
			Text:       text,
		},
	})
}

// DidChange notifies the appropriate server of a content change, opening the
// document first if it was not open (so a bare DidChange still works).
func (m *Manager) DidChange(ctx context.Context, path, text string) error {
	c := m.clientFor(ctx, path)
	if c == nil {
		return fmt.Errorf("lsp manager: no server for %s", path)
	}
	uri := uriFor(path)
	if _, opened := c.versions[uri]; !opened {
		return m.OpenDocument(ctx, path, text)
	}
	c.versions[uri]++
	return c.server.Client().DidChange(DidChangeTextDocumentParams{
		TextDocument: VersionedTextDocumentIdentifier{URI: uri, Version: c.versions[uri]},
		ContentChanges: []TextDocumentContentChangeEvent{
			{Text: text},
		},
	})
}

// DiagnosticsFor returns the latest diagnostics published for a file path.
func (m *Manager) DiagnosticsFor(ctx context.Context, path string) []Diagnostic {
	if m == nil {
		return nil
	}
	return m.diags.Get(uriFor(path))
}

// HasErrors reports whether any tracked file has an error-level diagnostic.
func (m *Manager) HasErrors() bool {
	if m == nil {
		return false
	}
	return m.diags.HasErrors()
}

// ensureOpen makes sure the document is open on its server before a position
// request, opening it (with on-disk contents) if needed.
func (m *Manager) ensureOpen(ctx context.Context, path string) (*serverClient, error) {
	c := m.clientFor(ctx, path)
	if c == nil {
		return nil, fmt.Errorf("lsp manager: no server for %s", path)
	}
	uri := uriFor(path)
	if _, opened := c.versions[uri]; opened {
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
	return c.server.Client().DocumentSymbol(ctx, DocumentSymbolParams{
		TextDocument: TextDocumentIdentifier{URI: uriFor(path)},
	})
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

// Status reports which servers are running, for diagnostics/UI.
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
	if len(ids) == 0 {
		return "lsp: none"
	}
	return "lsp: " + strings.Join(ids, ",")
}
