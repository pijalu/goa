// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
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
	ask "github.com/pijalu/goa/tools/ask"
	"github.com/pijalu/goa/tools/search/bm25"
)

// RuntimeOptions holds CLI flags that control runtime behavior, including
// headless execution mode and shared options like memory injection.
type RuntimeOptions struct {
	PromptArg        string
	PromptFile       string
	PromptGiven      bool // true when --prompt was explicitly set (even if empty)
	Goal             bool
	Orchestrate      string // run-id to resume headless via the orchestrator runtime
	Plain            bool
	Yes              bool
	NoMemory         bool
	NoPlugins        bool
	MemoryBudget     int
	MaxTurns         int
	Timeout          time.Duration
	Color            string
	Dream            bool
	DreamApply       bool
	ACP              bool
	CheckUpdate      bool
	Telemetry        bool
	ExportOutput     string
	ExportSession    string
	IncludeGlobalLog bool
	CPUProfile       string
	MemProfile       string
	TraceFile        string
	PerfLoad         bool
	PerfLoadDuration time.Duration
	WithProfiling    bool
}

// Headless reports whether the user requested headless execution.
func (o RuntimeOptions) Headless() bool {
	return o.Goal || o.Orchestrate != "" || o.promptImpliesHeadless()
}

func (o RuntimeOptions) promptImpliesHeadless() bool {
	return o.PromptGiven || o.PromptArg != "" || o.PromptFile != ""
}

// Dream reports whether the user requested dream mode.
func (o RuntimeOptions) DreamMode() bool {
	return o.Dream || o.DreamApply
}

// UserPrompt returns the user prompt, either directly from --prompt or read
// from --prompt-file. It returns an empty string when running in TUI mode.
func (o RuntimeOptions) UserPrompt() (string, error) {
	if o.PromptArg != "" {
		return o.PromptArg, nil
	}
	if o.PromptFile == "" {
		return "", nil
	}
	const maxSize = 1 << 20 // 1 MB
	info, err := os.Stat(o.PromptFile)
	if err != nil {
		return "", fmt.Errorf("--prompt-file: %w", err)
	}
	if info.Size() > maxSize {
		return "", fmt.Errorf("--prompt-file exceeds 1MB limit")
	}
	data, err := os.ReadFile(o.PromptFile)
	if err != nil {
		return "", fmt.Errorf("--prompt-file: %w", err)
	}
	return string(data), nil
}

// Validate returns an error if runtime options are inconsistent.
func (o RuntimeOptions) Validate() error {
	if err := o.validateModes(); err != nil {
		return err
	}
	if o.MemoryBudget < 0 {
		return fmt.Errorf("--memory-budget must be >= 0")
	}
	if err := validateColor(o.Color); err != nil {
		return err
	}
	if o.MaxTurns < 0 {
		return fmt.Errorf("--max-turns must be >= 0")
	}
	if o.Timeout < 0 {
		return fmt.Errorf("--timeout must be >= 0")
	}
	return nil
}

func (o RuntimeOptions) validateModes() error {
	if err := o.checkHeadlessIncompatible(); err != nil {
		return err
	}
	if err := o.validatePromptFlags(); err != nil {
		return err
	}
	if err := o.validateDreamCompatibility(); err != nil {
		return err
	}
	if o.Goal && !o.promptImpliesHeadless() {
		return fmt.Errorf("--goal requires --prompt or --prompt-file")
	}
	return nil
}

func (o RuntimeOptions) validatePromptFlags() error {
	if o.PromptArg != "" && o.PromptFile != "" {
		return fmt.Errorf("--prompt and --prompt-file are mutually exclusive")
	}
	if o.PromptGiven && o.PromptArg == "" && o.PromptFile == "" {
		return fmt.Errorf("--prompt requires a non-empty value")
	}
	return nil
}

func (o RuntimeOptions) validateDreamCompatibility() error {
	if o.PromptArg != "" && (o.Dream || o.DreamApply) {
		return fmt.Errorf("--prompt is incompatible with --dream/--dream-apply")
	}
	if o.PromptFile != "" && (o.Dream || o.DreamApply) {
		return fmt.Errorf("--prompt-file is incompatible with --dream/--dream-apply")
	}
	return nil
}

func (o RuntimeOptions) checkHeadlessIncompatible() error {
	if !o.Headless() {
		return nil
	}
	if o.ACP {
		return fmt.Errorf("--acp is incompatible with --prompt/--prompt-file")
	}
	if o.CheckUpdate {
		return fmt.Errorf("--check-update is incompatible with --prompt/--prompt-file")
	}
	if o.ExportOutput != "" || o.ExportSession != "" || o.IncludeGlobalLog {
		return fmt.Errorf("--export-* flags are incompatible with --prompt/--prompt-file")
	}
	return nil
}

func validateColor(color string) error {
	switch color {
	case "", "auto", "always", "never":
		return nil
	default:
		return fmt.Errorf("--color must be auto, always, or never")
	}
}

// ParseCLIFlags parses command-line flags into a map of config overrides and
// runtime options.
//
// Flags are registered on a FRESH FlagSet per call. goa relaunches itself in
// the same process after the setup wizard (Main's runApp loop); re-registering
// on the process-global flag.CommandLine panics with "flag redefined: ..." on
// the second launch, right after the wizard finishes.
func ParseCLIFlags() (map[string]string, RuntimeOptions) {
	fs := flag.NewFlagSet("goa", flag.ExitOnError)
	flags := map[string]string{}
	stringPtrs := defineStringFlags(fs)
	scalar := defineScalarFlags(fs)
	ro := defineRuntimeFlags(fs)

	fs.Parse(os.Args[1:])

	collectStringFlags(flags, stringPtrs)
	scalar.collectInto(flags)
	return flags, ro.collectInto(fs)
}

type stringFlagDef struct {
	name string
	key  string
	desc string
}

func defineStringFlags(fs *flag.FlagSet) map[string]*string {
	defs := []stringFlagDef{
		{"model", "model", "Override active model"},
		{"profile", "profile", "Override active mode"},
		{"provider", "provider", "Override active provider"},
		{"endpoint", "endpoint", "Override provider endpoint"},
		{"api-key", "api_key", "Override provider API key"},
		{"skill-mode", "skill_mode", "Override skill execution mode (inline or subagent)"},
		{"execution-mode", "execution_mode", "Override execution mode (yolo, solo, confirm, review)"},
		{"thinking-level", "thinking_level", "Set thinking level (off, minimal, low, medium, high, xhigh)"},
		{"thinking-blocks", "thinking_blocks", "Set thinking blocks visibility (on or off)"},
		{"theme", "theme", "Override TUI theme (dark or light)"},
		{"config", "config", "Explicit config path"},
		{"home", "home", "Override the goa home directory (config, cache, logs, usage; env: GOA_HOME)"},
		{"logfile", "logfile", "Write agent/LLM debug logs to file"},
		{"terminal-log", "terminal_log", "Write raw TUI terminal output to file"},
		{"render-log", "render_trace", "Write per-frame compositor render trace (JSONL) to file"},
		{"capture-stream", "capture_stream", "Capture the exact agent stream flow as JSONL to file (replay/diagnosis)"},
	}
	ptrs := make(map[string]*string, len(defs))
	for _, d := range defs {
		ptrs[d.key] = fs.String(d.name, "", d.desc)
	}
	return ptrs
}

func collectStringFlags(flags map[string]string, ptrs map[string]*string) {
	for key, ptr := range ptrs {
		if *ptr != "" {
			flags[key] = *ptr
		}
	}
}

type scalarFlags struct {
	temperature              *float64
	maxTokens                *int
	maxToolRepeatTotal       *int
	maxToolRepeatConsecutive *int
	maxToolCalls             *int
	maxStreamRounds          *int
	maxConsecutiveToolRounds *int
	toolCallLimitResetWindow *int
	reasoning                *bool
	showThinking             *bool
	compression              *bool
	debug                    *bool
	debugKeys                *bool
}

type runtimeFlagDefs struct {
	prompt           *string
	promptFile       *string
	goal             *bool
	orchestrate      *string
	plain            *bool
	yes              *bool
	noMemory         *bool
	noPlugins        *bool
	memoryBudget     *int
	maxTurns         *int
	timeout          *time.Duration
	color            *string
	dream            *bool
	dreamApply       *bool
	acp              *bool
	checkUpdate      *bool
	telemetry        *bool
	exportOutput     *string
	exportSession    *string
	includeGlobalLog *bool
	cpuProfile       *string
	memProfile       *string
	traceFile        *string
	perfLoad         *bool
	perfLoadDuration *time.Duration
	withProfiling    *bool
}

func defineScalarFlags(fs *flag.FlagSet) scalarFlags {
	return scalarFlags{
		temperature:              fs.Float64("temperature", 0, "Override model temperature"),
		maxTokens:                fs.Int("max-tokens", 0, "Override model max output tokens"),
		maxToolRepeatTotal:       fs.Int("max-tool-repeat-total", 0, "Override max total identical tool calls per turn"),
		maxToolRepeatConsecutive: fs.Int("max-tool-repeat-consecutive", 0, "Override max consecutive identical tool calls"),
		maxToolCalls:             fs.Int("max-tool-calls", 0, "Override max duplicate tool calls within the rolling window"),
		maxStreamRounds:          fs.Int("max-stream-rounds", 0, "Override max LLM stream rounds per turn (0 = unlimited)"),
		maxConsecutiveToolRounds: fs.Int("max-consecutive-tool-rounds", 0, "Override max consecutive tool-only rounds before forced-answer nudge (0 = disabled, default 15)"),
		toolCallLimitResetWindow: fs.Int("tool-call-limit-reset-window", 0, "Override tool-call duplicate rolling-window size"),
		reasoning:                fs.Bool("reasoning", false, "Enable model reasoning"),
		showThinking:             fs.Bool("show-thinking", false, "Show main-agent thinking blocks"),
		compression:              fs.Bool("compression", false, "Enable context compression"),
		debug:                    fs.Bool("debug", false, "Enable debug logging"),
		debugKeys:                fs.Bool("debug-keys", false, "Trace raw TUI keystrokes to a log file"),
	}
}

func defineRuntimeFlags(fs *flag.FlagSet) runtimeFlagDefs {
	return runtimeFlagDefs{
		prompt:           fs.String("prompt", "", "User prompt to execute (implies headless mode)"),
		promptFile:       fs.String("prompt-file", "", "Read prompt from file (implies headless mode)"),
		goal:             fs.Bool("goal", false, "Treat the prompt as a goal objective (headless mode only)"),
		orchestrate:      fs.String("orchestrate", "", "Resume orchestrator run <run-id> headless"),
		plain:            fs.Bool("plain", false, "Force plain, uncolored output in headless mode"),
		yes:              fs.Bool("yes", false, "Auto-approve tool confirmations in headless mode"),
		noMemory:         fs.Bool("no-memory", false, "Do not inject long-term memory into the system prompt"),
		noPlugins:        fs.Bool("no-plugins", false, "Start without loading any plugins (bundled and installed)"),
		memoryBudget:     fs.Int("memory-budget", 0, "Maximum tokens for memory injection (0=auto)"),
		maxTurns:         fs.Int("max-turns", 0, "Maximum agent turns in headless mode (0=unlimited)"),
		timeout:          fs.Duration("timeout", 0, "Overall session timeout in headless mode (0=none)"),
		color:            fs.String("color", "auto", "Color output in headless mode: auto, always, or never"),
		dream:            fs.Bool("dream", false, "Run memory consolidation (dream) and exit"),
		dreamApply:       fs.Bool("dream-apply", false, "Run dream and apply consolidated memory immediately"),
		acp:              fs.Bool("acp", false, "Run ACP server over stdin/stdout"),
		checkUpdate:      fs.Bool("check-update", false, "Check for updates and exit"),
		telemetry:        fs.Bool("telemetry", false, "Send anonymous telemetry"),
		exportOutput:     fs.String("export-output", "", "Output path for goa export"),
		exportSession:    fs.String("export-session", "", "Session ID to export"),
		includeGlobalLog: fs.Bool("include-global-log", false, "Include global log in export"),
		cpuProfile:       fs.String("cpuprofile", "", "Write CPU profile to `file`"),
		memProfile:       fs.String("memprofile", "", "Write memory profile to `file`"),
		traceFile:        fs.String("trace", "", "Write execution trace to `file`"),
		perfLoad:         fs.Bool("perf-load", false, "Run a synthetic TUI performance load instead of an agent turn"),
		perfLoadDuration: fs.Duration("perf-load-duration", 30*time.Second, "Duration of the synthetic performance load"),
		withProfiling:    fs.Bool("with-profiling", false, "Capture CPU, memory, and trace profiles after exit (default names unless overridden)"),
	}
}

// collectInto returns the parsed RuntimeOptions from flag pointers.
// fs is the FlagSet the flags were registered on; it is used to detect
// explicitly-set flags (flag.Visit only visits flags changed by the user).
func (r *runtimeFlagDefs) collectInto(fs *flag.FlagSet) RuntimeOptions {
	// Detect if --prompt was explicitly set (even to empty string).
	promptSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "prompt" {
			promptSet = true
		}
	})

	return RuntimeOptions{
		PromptArg:        *r.prompt,
		PromptGiven:      promptSet,
		PromptFile:       *r.promptFile,
		Goal:             *r.goal,
		Orchestrate:      *r.orchestrate,
		Plain:            *r.plain,
		Yes:              *r.yes,
		NoMemory:         *r.noMemory,
		NoPlugins:        *r.noPlugins,
		MemoryBudget:     *r.memoryBudget,
		MaxTurns:         *r.maxTurns,
		Timeout:          *r.timeout,
		Color:            *r.color,
		Dream:            *r.dream,
		DreamApply:       *r.dreamApply,
		ACP:              *r.acp,
		CheckUpdate:      *r.checkUpdate,
		Telemetry:        *r.telemetry,
		ExportOutput:     *r.exportOutput,
		ExportSession:    *r.exportSession,
		IncludeGlobalLog: *r.includeGlobalLog,
		CPUProfile:       *r.cpuProfile,
		MemProfile:       *r.memProfile,
		TraceFile:        *r.traceFile,
		PerfLoad:         *r.perfLoad,
		PerfLoadDuration: *r.perfLoadDuration,
		WithProfiling:    *r.withProfiling,
	}
}

func (s scalarFlags) collectInto(flags map[string]string) {
	if *s.temperature != 0 {
		flags["temperature"] = strconv.FormatFloat(*s.temperature, 'f', -1, 64)
	}
	if *s.maxTokens != 0 {
		flags["max_tokens"] = strconv.Itoa(*s.maxTokens)
	}
	if *s.maxToolRepeatTotal != 0 {
		flags["max_tool_repeat_total"] = strconv.Itoa(*s.maxToolRepeatTotal)
	}
	if *s.maxToolRepeatConsecutive != 0 {
		flags["max_tool_repeat_consecutive"] = strconv.Itoa(*s.maxToolRepeatConsecutive)
	}
	if *s.maxToolCalls != 0 {
		flags["max_tool_calls"] = strconv.Itoa(*s.maxToolCalls)
	}
	if *s.maxStreamRounds != 0 {
		flags["max_stream_rounds"] = strconv.Itoa(*s.maxStreamRounds)
	}
	if *s.maxConsecutiveToolRounds != 0 {
		flags["max_consecutive_tool_rounds"] = strconv.Itoa(*s.maxConsecutiveToolRounds)
	}
	if *s.toolCallLimitResetWindow != 0 {
		flags["tool_call_limit_reset_window"] = strconv.Itoa(*s.toolCallLimitResetWindow)
	}
	collectBoolFlag(flags, "reasoning", *s.reasoning)
	collectBoolFlag(flags, "show_thinking", *s.showThinking)
	collectBoolFlag(flags, "compression", *s.compression)
	collectBoolFlag(flags, "debug", *s.debug)
	collectBoolFlag(flags, "debug_keys", *s.debugKeys)
}

func collectBoolFlag(flags map[string]string, key string, value bool) {
	if value {
		flags[key] = "true"
	}
}

// MustGetwd returns the current working directory or exits on error.
func MustGetwd() string {
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	return dir
}

// LoadConfig loads configuration from the cascade loader, running the first-run
// wizard when necessary.
func LoadConfig(loader *config.CascadeLoader, projectDir string) *config.Config {
	cfg, err := loader.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Config error: %v\n", err)
		os.Exit(1)
	}

	if !cfg.FirstRun {
		return cfg
	}

	return handleFirstRun(loader, cfg, projectDir)
}

func handleFirstRun(loader *config.CascadeLoader, cfg *config.Config, projectDir string) *config.Config {
	fmt.Println("⟡  First run detected — launching setup wizard")
	result, err := config.RunSetupWizard(projectDir, loader)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Setup wizard error: %v\n", err)
		os.Exit(1)
	}
	if result.Cancelled {
		fmt.Println("Setup skipped. Edit ~/.goa/config.yaml manually, then restart.")
		os.Exit(0)
	}
	if !result.ConfigWritten {
		return cfg
	}

	fmt.Println("Configuration saved to ~/.goa/config.yaml")
	cfg, err = loader.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Reload config error: %v\n", err)
		os.Exit(1)
	}
	return cfg
}

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
