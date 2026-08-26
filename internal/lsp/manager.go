// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package lsp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Manager coordinates one language-server client per (server, project-root)
// pair, selecting the right server for each file by extension and spawning it
// lazily on first use (OpenCode's getClients model). It is safe for concurrent
// use.
type Manager struct {
	mu      sync.Mutex
	rootDir string
	binDir  string
	install bool
	started bool
	specs   []ServerSpec
	clients map[string]*serverClient // key: serverID + "|" + root
	broken  map[string]error         // key: serverID + "|" + root → start error
	diags   *Diagnostics
	// spawning tracks in-flight async spawns: key → flight whose done channel
	// closes when the spawn resolves (client registered or key marked broken).
	// A channel (not sync.WaitGroup) so waiters can bound the wait with ctx.
	spawning map[string]*spawnFlight
	// serverFactory starts a server process; defaults to Start (spawns the real
	// binary). Swappable in tests.
	serverFactory func(ctx context.Context, cfg ServerConfig) (*Server, error)
	// resolve maps a spec to its launch argv; defaults to spec.resolveCommand.
	// Swappable in tests to bypass binary lookup/install.
	resolve func(spec *ServerSpec, workspace, binDir string, installAllowed bool) ([]string, bool)
	// lifecycleCtx governs spawned server processes, decoupled from any single
	// query's context: a query ctx cancellation must not kill a long-lived
	// server. Cancelled by Close.
	lifecycleCtx    context.Context
	lifecycleCancel context.CancelFunc
}

// spawnFlight tracks one in-flight server spawn; done is closed when the
// spawn resolves (success → client registered, failure → key marked broken).
type spawnFlight struct{ done chan struct{} }

// spawnHandshakeTimeout bounds server startup + the initialize handshake.
// Cold npx downloads (pyright, typescript-language-server) take ~1 minute on
// first run; 3 minutes is generous. The timeout governs ONLY the resolve +
// initialize handshake — the server PROCESS stays on lifecycleCtx, so a
// healthy server is never killed by this deadline. Before this bound, a
// server that accepted its pipe but never answered Initialize wedged the
// calling tool FOREVER (Read stuck/ Issue 21-style hang).
// A var (not const) so tests can shrink the bound.
var spawnHandshakeTimeout = 3 * time.Minute

// serverClient is one running language server and the files it has open.
type serverClient struct {
	server *Server
	spec   *ServerSpec
	root   string
	// mu guards versions AND orders open/change notifications so the version
	// number sent on the wire is monotonic per uri. Tool executions
	// (read/edit/write touchLSP) run in parallel scheduler goroutines, so
	// several goroutines can notify the same client at once — the unguarded
	// map caused "fatal error: concurrent map writes" (Issue 19).
	mu           sync.Mutex
	versions     map[string]int    // uri → latest didOpen/didChange version
	sent         map[string]string // uri → hash of the content last pushed to the server (ResyncExternal skip marker)
	capabilities ServerCapabilities
	// registrations records dynamic server capabilities (for example pull
	// diagnostics) acknowledged through client/registerCapability.
	registrations map[string]string
	resultIDs     map[string]string // URI → pull-diagnostic result id
}

// Option configures
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
		spawning:      make(map[string]*spawnFlight),
		serverFactory: Start,
		resolve: func(spec *ServerSpec, workspace, binDir string, installAllowed bool) ([]string, bool) {
			return spec.resolveCommandWithWorkspace(workspace, binDir, installAllowed)
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

// lookup resolves path to its server spec, (server, root) key, and root.
// ok is false when the manager is down or no server handles the file type.
// Nil-receiver safe: bootstrap may hold a typed-nil manager when LSP is off.
func (m *Manager) lookup(path string) (spec *ServerSpec, key, root string, ok bool) {
	if m == nil {
		return nil, "", "", false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.started {
		return nil, "", "", false
	}
	spec = specForFile(m.specs, path)
	if spec == nil {
		return nil, "", "", false
	}
	root = spec.FindRoot(path, m.rootDir)
	return spec, clientKey(spec.ID, root), root, true
}

// clientFor returns the RUNNING client for the given file — it never blocks.
// When the server is not up yet it kicks an asynchronous spawn (single-flight)
// and returns nil so the caller degrades gracefully instead of parking on a
// server start. Server starts can take ~1 minute (cold npx download) and used
// to run synchronously here, which wedged file tools for the full duration —
// two parallel reads of Python files both reported "Took 55.1s"
// (Read stuck). Use waitClientFor where blocking is acceptable
// (model-initiated navigation queries, which carry a ctx).
func (m *Manager) clientFor(path string) *serverClient {
	spec, key, root, ok := m.lookup(path)
	if !ok {
		return nil
	}
	m.mu.Lock()
	if c, ok := m.clients[key]; ok {
		m.mu.Unlock()
		return c
	}
	if _, broken := m.broken[key]; broken {
		m.mu.Unlock()
		return nil
	}
	if _, spawning := m.spawning[key]; spawning {
		m.mu.Unlock()
		return nil // server is starting: degrade, do not block
	}
	m.startSpawnLocked(spec, key, root)
	m.mu.Unlock()
	return nil
}

// waitClientFor is clientFor but waits (bounded by ctx) for an in-flight or
// freshly kicked spawn to resolve. Used by model-initiated query ops where
// blocking is expected and the caller's ctx bounds the wait (turn cancel,
// tool deadline).
func (m *Manager) waitClientFor(ctx context.Context, path string) *serverClient {
	spec, key, root, ok := m.lookup(path)
	if !ok {
		return nil
	}
	m.mu.Lock()
	if c, ok := m.clients[key]; ok {
		m.mu.Unlock()
		return c
	}
	if _, broken := m.broken[key]; broken {
		m.mu.Unlock()
		return nil
	}
	flight, spawning := m.spawning[key]
	if !spawning {
		flight = m.startSpawnLocked(spec, key, root)
	}
	m.mu.Unlock()

	select {
	case <-flight.done:
		m.mu.Lock()
		c := m.clients[key]
		m.mu.Unlock()
		return c
	case <-ctx.Done():
		return nil
	}
}

// startSpawnLocked records a new spawn flight and launches the async spawn.
// Caller must hold m.mu.
func (m *Manager) startSpawnLocked(spec *ServerSpec, key, root string) *spawnFlight {
	flight := &spawnFlight{done: make(chan struct{})}
	m.spawning[key] = flight
	go m.spawnAsync(spec, root, key, flight)
	return flight
}

// spawnAsync runs the slow spawn off the caller's path, then publishes the
// outcome (client registered, or key marked broken) and releases waiters.
func (m *Manager) spawnAsync(spec *ServerSpec, root, key string, flight *spawnFlight) {
	c, spawnErr := m.spawn(spec, root)
	m.mu.Lock()
	if c != nil {
		m.clients[key] = c
	} else {
		if spawnErr == nil {
			spawnErr = fmt.Errorf("lsp: server %s unavailable", spec.ID)
		}
		m.broken[key] = spawnErr
	}
	delete(m.spawning, key)
	m.mu.Unlock()
	close(flight.done)
}

// spawn resolves the server's command and starts it, returning nil when the
// server cannot be launched. The process is tied to lifecycleCtx (long-lived);
// only the resolve + initialize handshake is bounded by spawnHandshakeTimeout.
func (m *Manager) spawn(spec *ServerSpec, root string) (*serverClient, error) {
	hsCtx, cancel := context.WithTimeout(m.lifecycleCtx, spawnHandshakeTimeout)
	defer cancel()

	argv, ok := m.resolve(spec, root, m.binDir, m.install)
	if !ok || len(argv) == 0 {
		return nil, fmt.Errorf("lsp: %s unavailable; tried %s. Install the declared toolchain or enable automatic installation", spec.ID, spec.resolutionHint(m.install))
	}
	server, err := m.serverFactory(m.lifecycleCtx, ServerConfig{
		Command:        argv[0],
		Args:           argv[1:],
		RootDir:        root,
		Env:            mergeEnv(spec.Env),
		Initialization: initOptions(spec, root),
	})
	if err != nil {
		return nil, fmt.Errorf("lsp: start %s: %w", spec.ID, err)
	}
	client := server.Client()
	running := &serverClient{server: server, spec: spec, root: root, versions: make(map[string]int), sent: make(map[string]string), registrations: make(map[string]string), resultIDs: make(map[string]string)}
	m.registerClientHandlers(client, running)
	capabilities, err := m.handshake(hsCtx, client, spec, root)
	if err != nil {
		// Cleanup-on-error: one teardown site covers both handshake phases;
		// handshake itself carries no server reference.
		_ = server.Close(m.lifecycleCtx)
		return nil, err
	}
	running.capabilities = capabilities
	return running, nil
}

// registerClientHandlers wires the server→client traffic every spawned server
// needs before the handshake: pushed diagnostics, the diagnostic-refresh
// acknowledgement, dynamic capability (un)registration bookkeeping, and the
// standard workspace/window requests servers issue right after startup.
func (m *Manager) registerClientHandlers(client *Client, running *serverClient) {
	client.OnNotification("textDocument/publishDiagnostics", m.diags.Handler())
	client.OnNotification("workspace/diagnostic/refresh", func(_ json.RawMessage) {
		// A refresh is intentionally acknowledged as a notification. The next
		// bounded PullDiagnostics call obtains the fresh report; no request is
		// made from this callback, avoiding re-entrant protocol deadlocks.
	})

	client.OnRequest("client/registerCapability", func(params json.RawMessage) any {
		var p RegistrationParams
		if json.Unmarshal(params, &p) == nil && running != nil {
			running.mu.Lock()
			for _, reg := range p.Registrations {
				running.registrations[reg.ID] = reg.Method
			}
			running.mu.Unlock()
		}
		return nil
	})
	client.OnRequest("client/unregisterCapability", func(params json.RawMessage) any {
		var p UnregistrationParams
		if json.Unmarshal(params, &p) == nil && running != nil {
			running.mu.Lock()
			for _, reg := range p.Unregisterations {
				delete(running.registrations, reg.ID)
			}
			running.mu.Unlock()
		}
		return nil
	})
	client.OnRequest("workspace/configuration", func(params json.RawMessage) any { return []any{} })
	client.OnRequest("workspace/workspaceFolders", func(_ json.RawMessage) any {
		return []WorkspaceFolder{{URI: uriFor(running.root), Name: filepath.Base(running.root)}}
	})
	client.OnRequest("window/workDoneProgress/create", func(_ json.RawMessage) any { return nil })
}

// handshake performs the Initialize → Initialized exchange that completes a
// server's startup and returns the negotiated server capabilities. Errors are
// wrapped with the failing phase; teardown of the half-started server remains
// with spawn (see cleanup-on-error there).
func (m *Manager) handshake(ctx context.Context, client *Client, spec *ServerSpec, root string) (ServerCapabilities, error) {
	rootURI := uriFor(root)
	initResult, err := client.Initialize(ctx, InitializeParams{
		ProcessID:             0,
		RootURI:               rootURI,
		WorkspaceFolders:      []WorkspaceFolder{{URI: rootURI, Name: filepath.Base(root)}},
		Capabilities:          clientCapabilities(),
		InitializationOptions: initOptions(spec, root),
		Trace:                 "off",
	})
	if err != nil {
		return ServerCapabilities{}, fmt.Errorf("lsp: initialize %s: %w", spec.ID, err)
	}
	if err := client.Initialized(InitializedParams{}); err != nil {
		return ServerCapabilities{}, fmt.Errorf("lsp: initialized %s: %w", spec.ID, err)
	}
	return initResult.Capabilities, nil
}

// uriFor converts a path to a file:// URI. Symlinks are resolved (macOS
// /tmp→/private/tmp, /var→/private/var): some servers (typescript-language-
// server) canonicalize URIs to the real path in publishDiagnostics, while
// others (gopls, pyright) echo the didOpen URI back — resolving up front
// keeps didOpen and diagnostics keyed identically for BOTH kinds
// (Issue LSP: tsserver diagnostics silently dropped).
func uriFor(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return "file://" + filepath.ToSlash(path)
}

// pathFromURI is the inverse of uriFor: it converts a file:// URI produced by
// this client back to a filesystem path. ok is false for non-file schemes
// (untitled:, https:…) which have no on-disk counterpart to reconcile.
func pathFromURI(uri string) (path string, ok bool) {
	const scheme = "file://"
	if !strings.HasPrefix(uri, scheme) {
		return "", false
	}
	return filepath.FromSlash(strings.TrimPrefix(uri, scheme)), true
}

// contentHash fingerprints pushed document content so ResyncExternal can skip
// documents whose overlay already matches disk. SHA-256 truncation-free hex;
// document pushes are rare and files are small, so the cost is negligible.
func contentHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// clientCapabilities declares what the goa LSP client supports. Crucially it
// includes textDocument.publishDiagnostics: servers such as
// typescript-language-server only PUSH diagnostics when the client declares
// the capability — with an empty capabilities object they stay silent forever
// (Issue LSP: tsserver produced zero diagnostics).
func clientCapabilities() map[string]any {
	return map[string]any{
		"workspace": map[string]any{
			"workspaceFolders":       true,
			"configuration":          true,
			"didChangeConfiguration": map[string]any{"dynamicRegistration": true},
			"diagnostics":            map[string]any{"refreshSupport": true},
		},
		"window": map[string]any{"workDoneProgress": true},
		"textDocument": map[string]any{
			"synchronization":    map[string]any{"dynamicRegistration": true, "willSave": false, "didSave": true},
			"publishDiagnostics": map[string]any{"relatedInformation": true},
			"diagnostic":         map[string]any{"dynamicRegistration": true, "relatedDocumentSupport": true},
		},
	}
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
	// Workspace-derived values are declared by each registry entry. This keeps
	// server behavior data-driven and makes new providers extensible.
	if spec.Dynamic != nil && spec.Dynamic.PythonVenv != "" {
		if py := pythonVenv(root); py != "" {
			out = map[string]any{"python": map[string]any{"pythonPath": py}}
		}
	}
	if spec.Dynamic != nil && spec.Dynamic.TypeScriptServer {
		if ts := typeScriptServer(root); ts != "" {
			if out == nil {
				out = map[string]any{}
			}
			out["tsserver"] = map[string]any{"path": ts}
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

// typeScriptServer resolves the nearest workspace TypeScript installation.
func typeScriptServer(root string) string {
	for _, base := range []string{root, filepath.Dir(root)} {
		candidate := filepath.Join(base, "node_modules", "typescript", "lib", "tsserver.js")
		if fileExists(candidate) {
			return candidate
		}
	}
	return ""
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
