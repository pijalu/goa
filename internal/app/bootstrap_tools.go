// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/background"
	"github.com/pijalu/goa/internal/lsp"
	"github.com/pijalu/goa/internal/mcp"
	"github.com/pijalu/goa/internal/netutil"
	"github.com/pijalu/goa/internal/sandbox"
	"github.com/pijalu/goa/internal/secrets"
	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/tools"
	"github.com/pijalu/goa/tools/ask"
	"github.com/pijalu/goa/tools/search/bm25"
)

// registerTools registers the built-in filesystem and execution tools.
// Optional tools are skipped when disabled in configuration. It also connects
// configured MCP servers and returns their manager (nil when none configured).
// headless suppresses interactive-only tools (Bug C).
func registerTools(reg *tools.ToolRegistry, wm *internal.WorktreeManager, sandboxMgr *sandbox.Manager, projectDir string, cfg *config.Config, bgMgr *background.Manager, headless bool) (*lsp.Manager, *mcp.Manager) {
	backupStager := tools.NewBackupStager(projectDir)

	// Shared change tracker for edit/write → smartsearch index refresh.
	changeTracker := bm25.NewChangeTracker()
	notifyChanged := func(path string) { changeTracker.MarkChanged(path) }

	lspMgr := newLSPManager(projectDir, cfg)

	reg.Register(&tools.ReadFileTool{WorktreeMgr: wm, Config: cfg.Tools.ReadFile, LSPManager: lspMgr})

	reg.Register(&tools.WriteFileTool{
		WorktreeMgr:        wm,
		ProjectDir:         projectDir,
		BackupStager:       backupStager,
		FileChangeNotifier: notifyChanged,
		LSPManager:         lspMgr,
	})
	reg.Register(&tools.EditFileTool{
		WorktreeMgr:        wm,
		ProjectDir:         projectDir,
		BackupStager:       backupStager,
		AllowFuzz:          cfg.Tools.Edit.AllowFuzzOnEdits,
		Config:             cfg.Tools.Edit.FileToolConfig,
		FileChangeNotifier: notifyChanged,
		LSPManager:         lspMgr,
	})
	reg.Register(&tools.SearchTool{
		WorktreeMgr: wm,
		Threads:     cfg.Tools.Search.Threads,
		MaxResults:  cfg.Tools.Search.MaxResults,
		ExcludeDirs: cfg.Tools.Search.Exclude,
	})

	// lsp code navigation (definition/references/hover/symbols). Registered when
	// the tool is enabled and an LSP manager exists (any language). The manager
	// spawns servers lazily per file, so the tool degrades gracefully on files
	// with no available server.
	if cfg.Tools.Enabled.LSP && lspMgr != nil {
		reg.Register(&tools.LSPTool{
			WorktreeMgr: wm,
			ProjectDir:  projectDir,
			Manager:     lspMgr,
		})
	}

	compression := resolveCompression(cfg)
	reg.Register(&tools.BashTool{
		WorktreeMgr:      wm,
		Blocked:          cfg.Tools.Bash.BlockedCommands,
		Allowed:          cfg.Tools.Bash.AllowedCommands,
		EnvMaskPatterns:  cfg.Tools.Bash.EnvMaskPatterns,
		CompressOutput:   ptrBool(cfg.Tools.Bash.CompressOutput),
		ProjectDir:       projectDir,
		Jail:             cfg.Tools.Bash.Jail || cfg.DefaultModeState().Autonomy == internal.AutonomySolo,
		MaxOutputBytes:   cfg.Tools.Bash.MaxOutputBytes,
		MaxCaptureBytes:  cfg.Tools.Bash.MaxCaptureBytes,
		EnableComplexity: cfg.Tools.Bash.EnableComplexityAnalysis,
		WarnFileEdits:    cfg.Tools.Bash.WarnFileEdits == nil || *cfg.Tools.Bash.WarnFileEdits,
		// Resolve live so /config → tools.bash.warn_file_edits takes effect immediately.
		WarnFileEditsResolver: func() bool {
			return cfg.Tools.Bash.WarnFileEdits == nil || *cfg.Tools.Bash.WarnFileEdits
		},
		CompressionResolver: func() bool { return compression },
		Analyzer:            analyzerForBash(cfg.Tools.Bash),
		Redactor:            secrets.DefaultRedactor(),
	})
	registerOptionalTools(reg, wm, projectDir, cfg, bgMgr, changeTracker, headless)
	// P1 deferred-tool loader: a tiny schema whose description embeds the
	// compact catalog of deferred tools. Registered last so its catalog sees
	// the full deferred set; the model pulls deferred schemas on demand
	// instead of shipping every schema with every request.
	reg.Register(tools.NewToolSearchTool(reg))
	mcpMgr := registerMCPServers(reg, projectDir, cfg)
	return lspMgr, mcpMgr
}

// registerMCPServers connects every enabled MCP server from config and
// registers its tools under the "mcp__<server>__*" namespace. It never fails
// startup: per-server connect errors are logged and recorded in the manager's
// status. Connections are established concurrently so a slow/hung server does
// not block the others. The manager is always returned (never nil) so /mcp
// commands can add and connect servers at runtime.
func registerMCPServers(reg *tools.ToolRegistry, projectDir string, cfg *config.Config) *mcp.Manager {
	mgr := mcp.NewManager(reg)
	mgr.SetProjectDir(projectDir)
	if len(cfg.MCP) == 0 {
		return mgr
	}
	var wg sync.WaitGroup
	for name, srv := range cfg.MCP {
		if !srv.IsEnabled() {
			continue
		}
		sc := mcp.FromConfig(name, projectDir, srv)
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), sc.EffectiveTimeout())
			defer cancel()
			if err := mgr.Connect(ctx, sc); err != nil {
				log.Printf("mcp: %v\n", err)
			}
		}()
	}
	wg.Wait()
	return mgr
}

// registerOptionalTools registers tools that are gated by configuration flags.
// headless suppresses interactive-only tools: ask_user_question requires a
// human at the input line, which headless mode has none of (Bug C).
func registerOptionalTools(reg *tools.ToolRegistry, wm *internal.WorktreeManager, projectDir string, cfg *config.Config, bgMgr *background.Manager, changeTracker *bm25.ChangeTracker, headless bool) {
	registerVerifyTool(reg, projectDir, cfg)
	registerPythonTool(reg, projectDir, cfg)
	registerRunCodeTool(reg, projectDir, cfg)
	if cfg.Tools.Enabled.SSHBash {
		reg.Register(&tools.SSHBashTool{Hosts: sshHosts(cfg)})
	}
	if cfg.Tools.Enabled.BGExec {
		reg.Register(tools.NewBGExecToolWithManager(bgMgr))
	}
	if cfg.Tools.Enabled.Memento {
		reg.Register(&tools.MementoTool{ProjectDir: projectDir, GlobalDir: cfg.ConfigDir})
	}
	// ask_user_question is enabled BY DEFAULT (inverted flag) — except in
	// headless mode, where there is no user at the input line to answer
	// (Bug C). The host callback (Clarify) is injected after the App
	// is built — see internal/app attachClarifyTool.
	if !cfg.Tools.Enabled.ClarifyDisabled && !headless {
		reg.Register(&ask.AskUserQuestionTool{})
	}
	registerSmartSearchTool(reg, wm, projectDir, cfg, changeTracker)
}

func registerVerifyTool(reg *tools.ToolRegistry, projectDir string, cfg *config.Config) {
	if cfg.Tools.Enabled.Verify {
		reg.Register(&tools.VerifyTool{ProjectDir: projectDir})
	}
}

func registerPythonTool(reg *tools.ToolRegistry, projectDir string, cfg *config.Config) {
	if !cfg.Tools.Enabled.PythonEnabled {
		return
	}
	reg.Register(&tools.PythonTool{
		TimeoutSeconds: cfg.Tools.Python.TimeoutSeconds,
		ProjectDir:     projectDir,
		Jail:           cfg.Tools.Python.Jail || cfg.DefaultModeState().Autonomy == internal.AutonomySolo,
	})
}

// registerRunCodeTool registers run_code code-mode dispatch (gap TL7): a
// Python program that performs multiple tool sub-calls through the same
// guarded pipeline as direct calls, with a durable per-sub-call dispatch log
// under .goa/dispatch/<run>/ (spill-bounded artifacts).
func registerRunCodeTool(reg *tools.ToolRegistry, projectDir string, cfg *config.Config) {
	if !cfg.Tools.Enabled.RunCode {
		return
	}
	var dispatchDir string
	if projectDir != "" {
		dispatchDir = filepath.Join(projectDir, ".goa", "dispatch")
	}
	reg.Register(&tools.RunCodeTool{
		TimeoutSeconds:    cfg.Tools.RunCode.TimeoutSeconds,
		MaxProgramBytes:   cfg.Tools.RunCode.MaxProgramBytes,
		MaxLogResultBytes: cfg.Tools.RunCode.MaxLogResultBytes,
		ProjectDir:        projectDir,
		// The run_code worker is a "jailed worker" (gap TL7): its own os
		// file API is confined to the project unless the user explicitly
		// opts out. Sub-calls still respect their own tools' jails.
		Jail:        cfg.Tools.RunCode.Jail == nil || *cfg.Tools.RunCode.Jail,
		Registry:    reg,
		DispatchDir: dispatchDir,
	})
}

// registerSmartSearchTool registers BM25 relevance-ranked code search. It
// receives change notifications from edit/write tools for automatic index
// refresh. Only registered when enabled in configuration.
func registerSmartSearchTool(reg *tools.ToolRegistry, wm *internal.WorktreeManager, projectDir string, cfg *config.Config, changeTracker *bm25.ChangeTracker) {
	if !cfg.Tools.SmartSearch.Enabled {
		return
	}
	reg.Register(&tools.SmartSearchTool{
		WorktreeMgr:   wm,
		ProjectDir:    projectDir,
		MaxResults:    defaultInt(cfg.Tools.SmartSearch.MaxResults, 20),
		MinScore:      cfg.Tools.SmartSearch.MinScore,
		ExcludeDirs:   cfg.Tools.SmartSearch.ExcludeDirs,
		K1:            defaultFloat(cfg.Tools.SmartSearch.K1, 1.5),
		B:             defaultFloat(cfg.Tools.SmartSearch.B, 0.75),
		ChangeTracker: changeTracker,
	})
}

// resolveCompression returns the effective tool output compression setting.
// Resolution order:
//  1. Model-level compress_output override (if set)
//  2. Global tools.bash.compress_output override (if set)
//  3. Provider auto-detect — local providers default to enabled, remote off
func resolveCompression(cfg *config.Config) bool {
	if m, err := cfg.GetActiveModelConfig(); err == nil && m.CompressOutput != nil {
		return *m.CompressOutput
	}
	if cfg.Tools.Bash.CompressOutput != nil {
		return *cfg.Tools.Bash.CompressOutput
	}
	return config.DefaultCompressForProvider(cfg.GetActiveProviderConfig())
}

// defaultInt returns val if non-zero, otherwise defaultVal.
func defaultInt(val, defaultVal int) int {
	if val <= 0 {
		return defaultVal
	}
	return val
}

// newLSPManager builds a multi-language LSP manager from config. It returns
// nil when LSP is disabled EITHER globally (`lsp: false`) OR via the
// user-facing tool switch (`tools.enabled.lsp: false`) — off means off: no
// manager, no file touches, no background server spawns (Issue LSP:
// the tool flag used to gate only the model-facing tool while the manager
// kept spawning servers, wedging reads for ~55s on cold npx downloads).
// Servers spawn lazily per file (async, never blocking file tools), so Start
// never fails startup; per-server failures are recorded and surfaced via
// diagnostics absence. Config overrides (disable/override/custom servers) and
// disable_download are honored, matching OpenCode's lsp config model.
func newLSPManager(projectDir string, cfg *config.Config) *lsp.Manager {
	if cfg != nil && (!cfg.LSP.IsEnabled() || !cfg.Tools.Enabled.LSP) {
		return nil
	}
	installAllowed := cfg == nil || !cfg.LSP.DisableDownload
	specs := lsp.Registry()
	if cfg != nil && len(cfg.LSP.Servers) > 0 {
		specs = lsp.MergeRegistry(lspOverrides(cfg.LSP.Servers))
	}
	mgr := lsp.NewManager(projectDir,
		lsp.WithInstall(installAllowed),
		lsp.WithServers(specs),
	)
	// Start only flips the gate (servers spawn lazily); it cannot fail.
	_ = mgr.Start(context.Background())
	return mgr
}

// lspOverrides converts config server entries into the lsp package's override
// form (avoids a config→lsp import cycle).
func lspOverrides(servers map[string]config.LSPServerConfig) map[string]lsp.ServerOverride {
	out := make(map[string]lsp.ServerOverride, len(servers))
	for id, s := range servers {
		out[id] = lsp.ServerOverride{
			Command:        s.Command,
			Extensions:     s.Extensions,
			Disabled:       s.Disabled,
			Env:            s.Env,
			Initialization: s.Initialization,
			Markers:        s.Markers,
			LanguageID:     s.LanguageID,
		}
	}
	return out
}

// ptrBool dereferences a *bool, returning false when nil.
func ptrBool(v *bool) bool {
	if v == nil {
		return false
	}
	return *v
}

// analyzerForBash creates an AST-based analyzer when blocked or allowed
// command lists are configured, or when complexity analysis is explicitly
// enabled. This catches obfuscated or dynamic commands that simple
// first-token matching misses. Complexity analysis is enabled only
// when the user explicitly opts in via tools.bash.enable_complexity_analysis.
func analyzerForBash(cfg config.BashConfig) *sandbox.Analyzer {
	if len(cfg.BlockedCommands) == 0 && len(cfg.AllowedCommands) == 0 && !cfg.EnableComplexityAnalysis {
		return nil
	}
	return &sandbox.Analyzer{
		Blocked:            cfg.BlockedCommands,
		Allowed:            cfg.AllowedCommands,
		MaxComplexityScore: cfg.MaxComplexityScore,
		EnableComplexity:   &cfg.EnableComplexityAnalysis,
	}
}

// defaultFloat returns val if non-zero, otherwise defaultVal.
func defaultFloat(val, defaultVal float64) float64 {
	if val <= 0 {
		return defaultVal
	}
	return val
}

func registerWebFetchTool(reg *tools.ToolRegistry, sessionStore *core.SessionStore, cfg *config.Config, projectDir string) {
	if !cfg.Tools.Enabled.WebFetch {
		return
	}
	wc := cfg.Tools.WebFetch
	if !wc.Enabled {
		return
	}

	cacheDir := wc.Cache.Dir
	if cacheDir == "" {
		cacheDir = filepath.Join(projectDir, ".goa", "cache", "webfetch")
	}

	timeout := time.Duration(wc.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	fetcher := &netutil.Fetcher{
		Timeout:      timeout,
		UserAgent:    wc.UserAgent,
		MaxRedirects: wc.MaxRedirects,
		MaxBodyBytes: int64(wc.MaxTotalBytes),
	}

	cache := tools.NewWebFetchCache(
		cacheDir,
		time.Duration(wc.Cache.TTLHours)*time.Hour,
		wc.Cache.MaxEntries,
		wc.Cache.MaxBytes,
		time.Duration(wc.Cache.CleanupIntervalHours)*time.Hour,
		sessionStore,
	)

	tool := &tools.WebFetchTool{
		Fetcher:  fetcher,
		Cache:    cache,
		Config:   wc,
		HasModel: hasConfiguredModel(cfg),
		Summarizer: &tools.WebSummarizer{
			Role:          wc.Summary.SubAgentRole,
			DefaultPrompt: wc.Summary.DefaultPrompt,
			MaxInputLines: wc.Summary.MaxInputLines,
		},
	}
	reg.Register(tool)
}

// registerSessionQueryTools registers the read-only session query tools
// (session_search, session_event_read) over the shared session store.
func registerSessionQueryTools(reg *tools.ToolRegistry, sessionStore *core.SessionStore) {
	// Session tools are always registered: they are read-only and give the
	// model the ability to recall decisions from prior sessions.
	reg.Register(&tools.SessionSearchTool{Store: sessionStore})
	reg.Register(&tools.SessionEventReadTool{Store: sessionStore})
}

func hasConfiguredModel(cfg *config.Config) bool {
	return cfg.ActiveProvider != "" && cfg.ActiveModel != ""
}

func attachWebFetchSummarizer(reg *tools.ToolRegistry, pool tools.AgentPool) {
	if t, ok := reg.Get("webfetch"); ok {
		if wt, ok := t.(*tools.WebFetchTool); ok && wt.Summarizer != nil {
			wt.Summarizer.Pool = pool
		}
	}
}

// attachClarifyTool injects the interactive host callback (the Clarify
// function that renders a ClarifyCard and captures the answer on the main
// input line) into the registered ask_user_question tool. Called from App.Run
// once both the App and the tool registry exist.
func attachClarifyTool(reg *tools.ToolRegistry, clarify ask.ClarifyFunc) {
	if clarify == nil {
		return
	}
	if t, ok := reg.Get("ask_user_question"); ok {
		if at, ok := t.(*ask.AskUserQuestionTool); ok {
			at.SetClarify(clarify)
		}
	}
}

// attachEscalationApprover injects the sandbox escalation approval callback
// into the registered bash tool. Called from App.Run once both the App and the
// tool registry exist. When no approver is wired (e.g. headless mode), the
// bash tool keeps its fail-closed default: escalations are denied.
func attachEscalationApprover(reg *tools.ToolRegistry, approver func(ctx context.Context, req sandbox.EscalationRequest) (bool, error)) {
	if approver == nil {
		return
	}
	if t, ok := reg.Get("bash"); ok {
		if bt, ok := t.(*tools.BashTool); ok {
			bt.EscalationApprover = sandbox.EscalationApprover(approver)
		}
	}
}

type webFetchAgentPool struct {
	pool *multiagent.AgentPool
}

func (p *webFetchAgentPool) GetOrCreate(role string) (tools.AgentRunner, error) {
	return p.pool.GetOrCreate(role)
}

func sshHosts(cfg *config.Config) []tools.SSHHostConfig {
	var hosts []tools.SSHHostConfig
	for _, h := range cfg.Tools.SSH.Hosts {
		hosts = append(hosts, tools.SSHHostConfig{
			ID: h.ID, Host: h.Host, Port: h.Port,
			User: h.User, KeyFile: h.KeyFile,
		})
	}
	return hosts
}

// buildAgentLogger creates a file logger for the agentic SDK when config
// requests a log file. The file is opened for append and the logger is set to
// Debug so all agent events can be traced. Returns nil when no log file is
// configured.
func buildAgentLogger(cfg *config.Config, projectDir string) *agentic.Logger {
	logFile := cfg.Logging.File
	if logFile == "" {
		return nil
	}
	if !filepath.IsAbs(logFile) {
		logFile = filepath.Join(projectDir, logFile)
	}
	if err := os.MkdirAll(filepath.Dir(logFile), 0o755); err != nil {
		log.Printf("Warning: cannot create log directory for %q: %v", logFile, err)
		return nil
	}
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Printf("Warning: cannot open log file %q: %v", logFile, err)
		return nil
	}
	// Log files are used for diagnosis, so always capture the most verbose
	// event stream regardless of the console logging level.
	stdLogger := log.New(f, "", log.LstdFlags)
	logger := agentic.NewLoggerWithStdLogger(stdLogger, agentic.Debug)
	logger.Log(agentic.Info, " Goa agent log started (level=debug)")
	return logger
}
