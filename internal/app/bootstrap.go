// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/netutil"
	"github.com/pijalu/goa/internal/sandbox"
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
	Goal             bool
	Plain            bool
	Yes              bool
	NoMemory         bool
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
}

// Headless reports whether the user requested headless execution.
func (o RuntimeOptions) Headless() bool {
	return o.PromptArg != "" || o.PromptFile != ""
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
	if o.PromptArg != "" && o.PromptFile != "" {
		return fmt.Errorf("--prompt and --prompt-file are mutually exclusive")
	}
	if o.PromptArg != "" && (o.Dream || o.DreamApply) {
		return fmt.Errorf("--prompt is incompatible with --dream/--dream-apply")
	}
	if o.PromptFile != "" && (o.Dream || o.DreamApply) {
		return fmt.Errorf("--prompt-file is incompatible with --dream/--dream-apply")
	}
	if o.Goal && !o.Headless() {
		return fmt.Errorf("--goal requires --prompt or --prompt-file")
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
func ParseCLIFlags() (map[string]string, RuntimeOptions) {
	flags := map[string]string{}
	stringPtrs := defineStringFlags()
	scalar := defineScalarFlags()
	ro := defineRuntimeFlags()

	flag.Parse()

	collectStringFlags(flags, stringPtrs)
	scalar.collectInto(flags)
	return flags, ro.collectInto()
}

type stringFlagDef struct {
	name string
	key  string
	desc string
}

func defineStringFlags() map[string]*string {
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
		{"logfile", "logfile", "Write agent/LLM debug logs to file"},
	}
	ptrs := make(map[string]*string, len(defs))
	for _, d := range defs {
		ptrs[d.key] = flag.String(d.name, "", d.desc)
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
	temperature                *float64
	maxTokens                  *int
	maxToolRepeatTotal         *int
	maxToolRepeatConsecutive   *int
	maxToolCalls               *int
	maxStreamRounds            *int
	toolCallLimitResetWindow   *int
	reasoning                  *bool
	showThinking               *bool
	compression                *bool
	debug                      *bool
	debugKeys                  *bool
}

type runtimeFlagDefs struct {
	prompt           *string
	promptFile       *string
	goal             *bool
	plain            *bool
	yes              *bool
	noMemory         *bool
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
}

func defineScalarFlags() scalarFlags {
	return scalarFlags{
		temperature:              flag.Float64("temperature", 0, "Override model temperature"),
		maxTokens:                flag.Int("max-tokens", 0, "Override model max output tokens"),
		maxToolRepeatTotal:       flag.Int("max-tool-repeat-total", 0, "Override max total identical tool calls per turn"),
		maxToolRepeatConsecutive: flag.Int("max-tool-repeat-consecutive", 0, "Override max consecutive identical tool calls"),
		maxToolCalls:             flag.Int("max-tool-calls", 0, "Override max total tool calls per turn"),
		maxStreamRounds:          flag.Int("max-stream-rounds", 0, "Override max LLM stream rounds per turn (0 = unlimited)"),
		toolCallLimitResetWindow: flag.Int("tool-call-limit-reset-window", 0, "Override tool-call budget reset window"),
		reasoning:                flag.Bool("reasoning", false, "Enable model reasoning"),
		showThinking:             flag.Bool("show-thinking", false, "Show main-agent thinking blocks"),
		compression:              flag.Bool("compression", false, "Enable context compression"),
		debug:                    flag.Bool("debug", false, "Enable debug logging"),
		debugKeys:                flag.Bool("debug-keys", false, "Trace raw TUI keystrokes to a log file"),
	}
}

func defineRuntimeFlags() runtimeFlagDefs {
	return runtimeFlagDefs{
		prompt:           flag.String("prompt", "", "User prompt to execute (implies headless mode)"),
		promptFile:       flag.String("prompt-file", "", "Read prompt from file (implies headless mode)"),
		goal:             flag.Bool("goal", false, "Treat the prompt as a goal objective (headless mode only)"),
		plain:            flag.Bool("plain", false, "Force plain, uncolored output in headless mode"),
		yes:              flag.Bool("yes", false, "Auto-approve tool confirmations in headless mode"),
		noMemory:         flag.Bool("no-memory", false, "Do not inject long-term memory into the system prompt"),
		memoryBudget:     flag.Int("memory-budget", 0, "Maximum tokens for memory injection (0=auto)"),
		maxTurns:         flag.Int("max-turns", 0, "Maximum agent turns in headless mode (0=unlimited)"),
		timeout:          flag.Duration("timeout", 0, "Overall session timeout in headless mode (0=none)"),
		color:            flag.String("color", "auto", "Color output in headless mode: auto, always, or never"),
		dream:            flag.Bool("dream", false, "Run memory consolidation (dream) and exit"),
		dreamApply:       flag.Bool("dream-apply", false, "Run dream and apply consolidated memory immediately"),
		acp:              flag.Bool("acp", false, "Run ACP server over stdin/stdout"),
		checkUpdate:      flag.Bool("check-update", false, "Check for updates and exit"),
		telemetry:        flag.Bool("telemetry", false, "Send anonymous telemetry"),
		exportOutput:     flag.String("export-output", "", "Output path for goa export"),
		exportSession:    flag.String("export-session", "", "Session ID to export"),
		includeGlobalLog: flag.Bool("include-global-log", false, "Include global log in export"),
	}
}

// collectInto returns the parsed RuntimeOptions from flag pointers.
func (r *runtimeFlagDefs) collectInto() RuntimeOptions {
	return RuntimeOptions{
		PromptArg:        *r.prompt,
		PromptFile:       *r.promptFile,
		Goal:             *r.goal,
		Plain:            *r.plain,
		Yes:              *r.yes,
		NoMemory:         *r.noMemory,
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
// Optional tools are skipped when disabled in configuration.
func registerTools(reg *tools.ToolRegistry, wm *internal.WorktreeManager, sandboxMgr *sandbox.Manager, projectDir string, cfg *config.Config) {
	gitStager := tools.NewGitStager(projectDir)

	// Shared change tracker for edit/write → smartsearch index refresh.
	changeTracker := bm25.NewChangeTracker()
	notifyChanged := func(path string) { changeTracker.MarkChanged(path) }

	reg.Register(&tools.ReadFileTool{WorktreeMgr: wm, Config: cfg.Tools.ReadFile})
	reg.Register(&tools.WriteFileTool{
		WorktreeMgr:        wm,
		ProjectDir:         projectDir,
		GitStager:          gitStager,
		Config:             cfg.Tools.Write.FileToolConfig,
		FileChangeNotifier: notifyChanged,
	})
	reg.Register(&tools.EditFileTool{
		WorktreeMgr:        wm,
		ProjectDir:         projectDir,
		GitStager:          gitStager,
		AllowFuzz:          cfg.Tools.Edit.AllowFuzzOnEdits,
		Config:             cfg.Tools.Edit.FileToolConfig,
		FileChangeNotifier: notifyChanged,
	})
	reg.Register(&tools.SearchTool{
		WorktreeMgr: wm,
		Threads:     cfg.Tools.Search.Threads,
		MaxResults:  cfg.Tools.Search.MaxResults,
		ExcludeDirs: cfg.Tools.Search.Exclude,
	})
	// resolveCompression returns the effective tool output compression setting.
	// Resolution order:
	//   1. Model-level compress_output override (if set)
	//   2. Global tools.bash.compress_output override (if set)
	//   3. Provider auto-detect — local providers default to enabled, remote off
	resolveCompression := func() bool {
		if m, err := cfg.GetActiveModelConfig(); err == nil && m.CompressOutput != nil {
			return *m.CompressOutput
		}
		if cfg.Tools.Bash.CompressOutput != nil {
			return *cfg.Tools.Bash.CompressOutput
		}
		return config.DefaultCompressForProvider(cfg.GetActiveProviderConfig())
	}

	reg.Register(&tools.BashTool{
		WorktreeMgr:     wm,
		Blocked:         cfg.Tools.Bash.BlockedCommands,
		Allowed:         cfg.Tools.Bash.AllowedCommands,
		EnvMaskPatterns: cfg.Tools.Bash.EnvMaskPatterns,
		CompressOutput:  ptrBool(cfg.Tools.Bash.CompressOutput),
		ProjectDir:      projectDir,
		Jail:            cfg.Tools.Bash.Jail || cfg.DefaultModeState().Autonomy == internal.AutonomySolo,
		MaxOutputBytes:  cfg.Tools.Bash.MaxOutputBytes,
		CompressionResolver: resolveCompression,
	})
	reg.Register(&tools.TerminalTool{
		WorktreeMgr:    wm,
		SandboxMgr:     sandboxMgr,
		Blocked:        cfg.Tools.Terminal.Sandbox.BlockedCommands,
		Allowed:        cfg.Tools.Terminal.Sandbox.AllowedCommands,
		TimeoutSeconds: cfg.Tools.Terminal.Sandbox.TimeoutSeconds,
		MaxOutputChars: cfg.Tools.Terminal.Sandbox.MaxOutputChars,
		Bypass:         !cfg.Tools.Terminal.Sandbox.Enabled,
		CompressionResolver: resolveCompression,
	})
	if cfg.Tools.Enabled.SSHBash {
		reg.Register(&tools.SSHBashTool{Hosts: sshHosts(cfg)})
	}
	if cfg.Tools.Enabled.BGExec {
		reg.Register(tools.NewBGExecTool())
	}
	if cfg.Tools.Enabled.Memento {
		reg.Register(&tools.MementoTool{ProjectDir: projectDir, GlobalDir: cfg.ConfigDir})
	}
	// ask_user_question is enabled BY DEFAULT (inverted flag). The host
	// callback (Clarify) is injected after the App is built — see
	// internal/app attachClarifyTool.
	if !cfg.Tools.Enabled.ClarifyDisabled {
		reg.Register(&ask.AskUserQuestionTool{})
	}

	// SmartSearch uses BM25 for relevance-ranked code search.
	// It receives change notifications from edit/write tools for automatic
	// index refresh.
	if cfg.Tools.SmartSearch.Enabled || !cfg.Tools.SmartSearch.Enabled {
		// SmartSearch is always registered (no opt-out toggle yet).
		ss := &tools.SmartSearchTool{
			WorktreeMgr:   wm,
			ProjectDir:    projectDir,
			MaxResults:    defaultInt(cfg.Tools.SmartSearch.MaxResults, 20),
			MinScore:      cfg.Tools.SmartSearch.MinScore,
			ExcludeDirs:   cfg.Tools.SmartSearch.Exclude,
			K1:            defaultFloat(cfg.Tools.SmartSearch.K1, 1.5),
			B:             defaultFloat(cfg.Tools.SmartSearch.B, 0.75),
			ChangeTracker: changeTracker,
		}
		reg.Register(ss)
	}
}

// defaultInt returns val if non-zero, otherwise defaultVal.
func defaultInt(val, defaultVal int) int {
	if val <= 0 {
		return defaultVal
	}
	return val
}

// ptrBool dereferences a *bool, returning false when nil.
func ptrBool(v *bool) bool {
	if v == nil {
		return false
	}
	return *v
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
