// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"
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
	// Server is set by the `goa server` subcommand: run the interactive
	// session against a virtual terminal and serve it over HTTP instead of a
	// TTY. ServerAddr is the listen address (--server-addr),
	// ServerReadOnly makes every browser a viewer (--server-read-only) and
	// ServerMaxClients caps attachments (--server-max-clients, 0 = default).
	Server           bool
	ServerAddr       string
	ServerReadOnly   bool
	ServerMaxClients int
	// ServerCells serves the v1 cell-terminal plane (--server-cells). The
	// default is the blocks plane (specs/webui.md §22): semantic conversation
	// blocks rendered as HTML by the browser, with the editor band as cells.
	ServerCells bool
	// ServerAuth selects the web UI's auth scheme ("none", "basic", "token").
	// Without it the server only binds loopback; see webui.CheckExposure.
	ServerAuth      string
	ServerAuthUser  string
	ServerAuthPass  string
	ServerAuthToken string
	// InsecureNoAuth waives the loopback requirement (--insecure-no-auth).
	InsecureNoAuth bool
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

// cliFlagDefs is this process's command-line surface: the flag set plus the
// typed pointers every registered flag wrote into.
type cliFlagDefs struct {
	fs      *flag.FlagSet
	strPtrs map[string]*string
	scalar  scalarFlags
	runtime runtimeFlagDefs
}

var (
	cliFlagDefsOnce sync.Once
	cliFlagDefsInst *cliFlagDefs
)

// cliFlags returns the flag set with every goa flag registered, defining it on
// first use.
//
// Registration is a single lazy step because two very different callers need the
// same set: the parser, and the help subsystem — which renders the option
// reference from the live flags *before* any parsing happens. It also makes the
// definition idempotent, which matters because the flag package panics on a
// duplicate definition and runApp can be re-entered by the relaunch loop.
func cliFlags() *cliFlagDefs {
	cliFlagDefsOnce.Do(func() {
		fs := flag.NewFlagSet("goa", flag.ContinueOnError)
		// The flag package's own output is never used: errors are reported by
		// the caller in the manual's voice, and -h/--help is answered with the
		// manual instead of a bare flag dump.
		fs.SetOutput(io.Discard)
		fs.Usage = func() {}
		cliFlagDefsInst = &cliFlagDefs{
			fs:      fs,
			strPtrs: defineStringFlags(fs),
			scalar:  defineScalarFlags(fs),
			runtime: defineRuntimeFlags(fs),
		}
	})
	return cliFlagDefsInst
}

// ParseCLIFlags parses command-line flags into a map of config overrides and
// runtime options.
//
// Parsing is done in ContinueOnError mode with the flag package's output
// silenced, so this function — not flag.Parse — owns every exit path. That
// matters twice over: the flag package answers `-h`/`--help` by printing its own
// bare flag dump and calling os.Exit(0) directly, which both truncated the output
// (the stderr tee drains asynchronously) and documented nothing but flag names;
// and a usage error must print the synopsis instead of that dump.
func ParseCLIFlags() (map[string]string, RuntimeOptions) {
	defs := cliFlags()

	if err := parseArgv(defs.fs); err != nil {
		// Not a help request: the error text carries the parser's message.
		fmt.Fprintf(os.Stderr, "goa: %v\n\n%s", err, cliShortUsage)
		exitAfterFlush(2)
	}
	if err := unknownCommandError(defs.fs.Args()); err != nil {
		fmt.Fprintf(os.Stderr, "goa: %v\n", err)
		exitAfterFlush(2)
	}

	flags := map[string]string{}
	collectStringFlags(flags, defs.strPtrs)
	defs.scalar.collectInto(flags)
	return flags, defs.runtime.collectInto(defs.fs)
}

// parseArgv parses argv against the registered flag set. It returns an error for
// a plain parse failure; a help request is answered and exits the process.
// (runHelpCLI answers the help forms that must be recognized before parsing —
// this one covers `goa --model x --help`.)
func parseArgv(fs *flag.FlagSet) error {
	err := fs.Parse(os.Args[1:])
	if errors.Is(err, flag.ErrHelp) {
		exitWithHelp("")
	}
	return err
}

type stringFlagDef struct {
	name string
	key  string
	desc string
}

// defineStringFlags registers every free-form string flag and returns the
// pointers keyed by their config-override key.
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
	serverAddr       *string
	serverReadOnly   *bool
	serverMaxClients *int
	serverCells      *bool
	serverAuth       *string
	serverAuthUser   *string
	serverAuthPass   *string
	serverAuthToken  *string
	insecureNoAuth   *bool
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
		serverAddr:       fs.String("server-addr", "", "Listen address for 'goa server' (default 127.0.0.1:8080)"),
		serverReadOnly:   fs.Bool("server-read-only", false, "Serve 'goa server' as a viewer: browsers see the session but cannot drive it"),
		serverMaxClients: fs.Int("server-max-clients", 0, "Maximum browsers attached to 'goa server' (0 = built-in default)"),
		serverCells:      fs.Bool("server-cells", false, "Serve 'goa server' in the legacy cell-terminal plane (default: blocks plane — HTML blocks + cell band)"),
		serverAuth:       fs.String("server-auth", "none", "Authentication for 'goa server': none, basic or token"),
		serverAuthUser:   fs.String("server-auth-user", "", "Username for --server-auth=basic"),
		serverAuthPass:   fs.String("server-auth-password", "", "Password for --server-auth=basic (prefer the env var GOA_SERVER_AUTH_PASSWORD)"),
		serverAuthToken:  fs.String("server-auth-token", "", "Bearer token for --server-auth=token (prefer the env var GOA_SERVER_AUTH_TOKEN)"),
		insecureNoAuth:   fs.Bool("insecure-no-auth", false, "Serve 'goa server' on a non-loopback address with no authentication (unsafe: anyone who can reach it drives the agent)"),
	}
}

// collectInto returns the parsed RuntimeOptions from flag pointers.
func (r *runtimeFlagDefs) collectInto(fs *flag.FlagSet) RuntimeOptions {
	// Detect if --prompt was explicitly set (even to empty string).
	// fs.Visit only iterates over flags that were explicitly changed by the user.
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
		ServerAddr:       *r.serverAddr,
		ServerReadOnly:   *r.serverReadOnly,
		ServerMaxClients: *r.serverMaxClients,
		ServerCells:      *r.serverCells,
		ServerAuth:       *r.serverAuth,
		ServerAuthUser:   *r.serverAuthUser,
		ServerAuthPass:   serverAuthSecret(*r.serverAuthPass, "GOA_SERVER_AUTH_PASSWORD"),
		ServerAuthToken:  serverAuthSecret(*r.serverAuthToken, "GOA_SERVER_AUTH_TOKEN"),
		InsecureNoAuth:   *r.insecureNoAuth,
	}
}

// serverAuthSecret prefers the environment variable over the flag. A secret on
// a command line is readable by every other process on the machine and lands in
// the shell history; the environment does not, so the flag stays as a fallback
// for the odd case where an exported variable cannot be set.
func serverAuthSecret(flagVal, envKey string) string {
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	return flagVal
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
		fatalExitf("Error: %v\n", err)
	}
	return dir
}

// LoadConfig loads configuration from the cascade loader, running the first-run
// wizard when necessary. Invalid layers no longer abort startup: they are
// dropped or answered by the defaults fallback and reported back so the TUI
// can announce them and offer a confirmed repair (bugs.md 2026-09-26: goa
// always aims to start, self-healing with user guidance).
func LoadConfig(loader *config.CascadeLoader, projectDir string) (*config.Config, *config.LoadReport) {
	cfg, rep, err := loader.LoadWithReport()
	if err != nil || cfg == nil {
		fatalExitf("Config error: %v\n", err)
	}
	if rep.UsedDefaults {
		// Non-TUI modes only see stderr; the TUI additionally surfaces this
		// through announceConfigIssues in the chat.
		fmt.Fprintf(os.Stderr, "Warning: %s — the default configuration is in effect\n",
			config.FallbackProblemSummary(rep))
	}

	if !cfg.FirstRun {
		return cfg, rep
	}

	return handleFirstRun(loader, cfg, projectDir, rep)
}

func handleFirstRun(loader *config.CascadeLoader, cfg *config.Config, projectDir string, rep *config.LoadReport) (*config.Config, *config.LoadReport) {
	fmt.Println("⟡  First run detected — launching setup wizard")
	result, err := config.RunSetupWizard(projectDir, loader)
	if err != nil {
		fatalExitf("Setup wizard error: %v\n", err)
	}
	if result.Cancelled {
		fmt.Println("Setup skipped. Edit ~/.goa/config.yaml manually, then restart.")
		exitAfterFlush(0)
	}
	if !result.ConfigWritten {
		return cfg, rep
	}

	fmt.Println("Configuration saved to ~/.goa/config.yaml")
	cfg, err = loader.Load()
	if err != nil {
		fatalExitf("Reload config error: %v\n", err)
	}
	// The wizard just wrote a valid configuration — nothing left to report.
	return cfg, &config.LoadReport{}
}
