// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/pijalu/goa/config"
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
		{"home", "home", "Override the goa home directory (config, cache, logs, usage; env: GOA_HOME)"},
		{"logfile", "logfile", "Write agent/LLM debug logs to file"},
		{"terminal-log", "terminal_log", "Write raw TUI terminal output to file"},
		{"render-log", "render_trace", "Write per-frame compositor render trace (JSONL) to file"},
		{"capture-stream", "capture_stream", "Capture the exact agent stream flow as JSONL to file (replay/diagnosis)"},
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

func defineScalarFlags() scalarFlags {
	return scalarFlags{
		temperature:              flag.Float64("temperature", 0, "Override model temperature"),
		maxTokens:                flag.Int("max-tokens", 0, "Override model max output tokens"),
		maxToolRepeatTotal:       flag.Int("max-tool-repeat-total", 0, "Override max total identical tool calls per turn"),
		maxToolRepeatConsecutive: flag.Int("max-tool-repeat-consecutive", 0, "Override max consecutive identical tool calls"),
		maxToolCalls:             flag.Int("max-tool-calls", 0, "Override max duplicate tool calls within the rolling window"),
		maxStreamRounds:          flag.Int("max-stream-rounds", 0, "Override max LLM stream rounds per turn (0 = unlimited)"),
		maxConsecutiveToolRounds: flag.Int("max-consecutive-tool-rounds", 0, "Override max consecutive tool-only rounds before forced-answer nudge (0 = disabled, default 15)"),
		toolCallLimitResetWindow: flag.Int("tool-call-limit-reset-window", 0, "Override tool-call duplicate rolling-window size"),
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
		orchestrate:      flag.String("orchestrate", "", "Resume orchestrator run <run-id> headless"),
		plain:            flag.Bool("plain", false, "Force plain, uncolored output in headless mode"),
		yes:              flag.Bool("yes", false, "Auto-approve tool confirmations in headless mode"),
		noMemory:         flag.Bool("no-memory", false, "Do not inject long-term memory into the system prompt"),
		noPlugins:        flag.Bool("no-plugins", false, "Start without loading any plugins (bundled and installed)"),
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
		cpuProfile:       flag.String("cpuprofile", "", "Write CPU profile to `file`"),
		memProfile:       flag.String("memprofile", "", "Write memory profile to `file`"),
		traceFile:        flag.String("trace", "", "Write execution trace to `file`"),
		perfLoad:         flag.Bool("perf-load", false, "Run a synthetic TUI performance load instead of an agent turn"),
		perfLoadDuration: flag.Duration("perf-load-duration", 30*time.Second, "Duration of the synthetic performance load"),
		withProfiling:    flag.Bool("with-profiling", false, "Capture CPU, memory, and trace profiles after exit (default names unless overridden)"),
	}
}

// collectInto returns the parsed RuntimeOptions from flag pointers.
func (r *runtimeFlagDefs) collectInto() RuntimeOptions {
	// Detect if --prompt was explicitly set (even to empty string).
	// flag.Visit only iterates over flags that were explicitly changed by the user.
	promptSet := false
	flag.Visit(func(f *flag.Flag) {
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
