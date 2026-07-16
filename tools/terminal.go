// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/sandbox"
)

// TerminalConfig controls terminal tool behavior.
type TerminalConfig struct {
	Sandbox TerminalSandboxConfig `yaml:"sandbox"`
}

// TerminalSandboxConfig controls sandboxing for the terminal tool.
type TerminalSandboxConfig struct {
	Enabled         bool     `yaml:"enabled"`
	BlockedCommands []string `yaml:"blocked_commands"`
	AllowedCommands []string `yaml:"allowed_commands"`
	TimeoutSeconds  int      `yaml:"timeout_seconds"`
	MaxOutputChars  int      `yaml:"max_output_chars"`
	BypassAllowed   bool     `yaml:"bypass_allowed"`
}

// TerminalTool executes shell commands locally inside a sandbox.
// It is a hardened replacement for BashTool.
type TerminalTool struct {
	agentic.BaseTool
	WorktreeMgr     WorktreeResolver
	SandboxMgr      *sandbox.Manager
	Blocked         []string
	Allowed         []string
	TimeoutSeconds  int
	MaxOutputChars  int
	CompressOutput  bool
	EnvMaskPatterns []string
	Bypass          bool

	// CompressionResolver, when non-nil, is called at execution time to
	// determine whether output compression is active. When nil, the static
	// CompressOutput field is used.
	CompressionResolver func() bool
}

// WorktreeResolver resolves the project directory for sandbox binding.
type WorktreeResolver interface {
	ProjectDir() string
}

// LoopHints supplies tool-loop-controller metadata so the controller does not
// need to special-case the "terminal" name: raw arguments heal under
// "command", and the TUI shows "Running: <command>" while a call is in flight.
func (t *TerminalTool) LoopHints() agentic.ToolLoopHints {
	return agentic.ToolLoopHints{HealArg: "command", Status: commandRunStatus}
}

// Schema returns the tool schema for terminal.
func (t *TerminalTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "terminal",
		Description: "Run a command in a terminal.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "command to execute",
				},
				"timeout": map[string]any{
					"type":        "integer",
					"description": "timeout in seconds (default: from config)",
				},
			},
			"required": []string{"command"},
		},
	}
}

// terminalParams holds parsed tool input.
type terminalParams struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

// Execute runs the shell command with security checks.
func (t *TerminalTool) Execute(input string) (string, error) {
	return t.ExecuteContext(context.Background(), input)
}

// ExecuteContext runs the shell command with security checks, forwarding the
// caller's context so a cancelled turn (Stop() / user cancellation) interrupts
// the running subprocess instead of waiting for the timeout to elapse.
func (t *TerminalTool) ExecuteContext(ctx context.Context, input string) (string, error) {
	p, err := t.parseParams(input)
	if err != nil {
		return "", err
	}
	if err := t.validate(p); err != nil {
		return "", err
	}

	workdir, err := t.workdir()
	if err != nil {
		return "", toolErr("terminal", "workdir_error", fmt.Sprintf("Failed to create sandbox workdir: %v", err))
	}

	env := t.buildEnv(workdir)
	timeout := t.timeout(p.Timeout)
	maxOut := t.maxOutput()

	res, duration, runErr := t.run(ctx, p.Command, workdir, env, timeout, maxOut)
	return t.formatResult(p.Command, res, duration, runErr, ctx)
}

func (t *TerminalTool) parseParams(input string) (terminalParams, error) {
	var p terminalParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		return p, toolErr("terminal", "invalid_input", fmt.Sprintf("Cannot parse parameters: %v", err))
	}
	if strings.TrimSpace(p.Command) == "" {
		return p, toolErr("terminal", "missing_command", "No command provided")
	}
	return p, nil
}

func (t *TerminalTool) validate(p terminalParams) error {
	if !t.Bypass {
		blocked := findBlockedCommands(p.Command, t.Blocked)
		if len(blocked) > 0 {
			return toolErr("terminal", "blocked_command",
				fmt.Sprintf("Blocked command(s) for safety: %s", strings.Join(blocked, ", ")))
		}
		return t.checkAllowed(p.Command)
	}
	if !sandbox.Harden() {
		return toolErr("terminal", "harden_failed",
			"Could not harden parent process against /proc environment reads; refusing bypass execution.")
	}
	return nil
}

func (t *TerminalTool) buildEnv(workdir string) map[string]string {
	b := &sandbox.EnvBuilder{}
	if t.Bypass {
		return b.BuildBypassEnv(workdir)
	}
	return b.BuildSafeEnv(workdir)
}

func (t *TerminalTool) run(ctx context.Context, command, workdir string, env map[string]string, timeout time.Duration, maxOut int) (sandbox.RunResult, time.Duration, error) {
	start := time.Now()
	res, runErr := sandbox.Run(sandbox.RunOpts{
		Cmd:       getShellCmd(command),
		Workdir:   workdir,
		Env:       env,
		Timeout:   timeout,
		MaxOutput: maxOut,
		Cancel:    ctx,
	})
	return res, time.Since(start), runErr
}

func (t *TerminalTool) formatResult(command string, res sandbox.RunResult, duration time.Duration, runErr error, ctx context.Context) (string, error) {
	output := t.maskOutput(res.Output)
	output = t.applyCompression(command, output)
	output = t.truncate(output)

	// A cancelled turn takes precedence over timeout/exit-code reporting:
	// surface the ctx error so the agent stops promptly.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", toolErr("terminal", "cancelled", fmt.Sprintf("Command cancelled: %v", ctxErr))
	}
	if runErr != nil && !res.TimedOut {
		return "", toolErr("terminal", "exec_error", fmt.Sprintf("Execution error: %v", runErr))
	}
	if res.TimedOut {
		return "", toolErr("terminal", "timeout", fmt.Sprintf("Command timed out after %v\n%s", t.timeout(0), output))
	}

	if res.ExitCode != 0 {
		return output, &internal.ToolError{
			Tool:   "terminal",
			Type:   "non_zero_exit",
			Detail: fmt.Sprintf("Exit code %d\n%s", res.ExitCode, output),
		}
	}

	if duration > 0 {
		output = fmt.Sprintf("%s\nDuration: %.2fs", strings.TrimSpace(output), duration.Seconds())
	}
	return strings.TrimSpace(output), nil
}

func (t *TerminalTool) workdir() (string, error) {
	if t.SandboxMgr != nil {
		return t.SandboxMgr.Workdir("")
	}
	if t.WorktreeMgr != nil {
		return t.WorktreeMgr.ProjectDir(), nil
	}
	return ".", nil
}

func (t *TerminalTool) timeout(override int) time.Duration {
	if override > 0 {
		return time.Duration(override) * time.Second
	}
	if t.TimeoutSeconds > 0 {
		return time.Duration(t.TimeoutSeconds) * time.Second
	}
	return 5 * time.Minute
}

func (t *TerminalTool) maxOutput() int {
	if t.MaxOutputChars > 0 {
		return t.MaxOutputChars
	}
	return 8000
}

func (t *TerminalTool) checkAllowed(command string) error {
	if len(t.Allowed) == 0 {
		return nil
	}
	program := firstCommandToken(command)
	for _, allowed := range t.Allowed {
		if strings.EqualFold(program, allowed) {
			return nil
		}
	}
	return &internal.ToolError{
		Tool:     "terminal",
		Type:     "command_not_allowed",
		Detail:   fmt.Sprintf("Command %q is not in the allowed list", program),
		HintText: "Use one of the allowed commands or update tools.terminal.sandbox.allowed_commands in config.",
	}
}

func (t *TerminalTool) maskOutput(output string) string {
	masks := t.buildMasks()
	for _, m := range masks {
		if m != "" {
			output = strings.ReplaceAll(output, m, "***")
		}
	}
	return output
}

func (t *TerminalTool) buildMasks() []string {
	var masks []string
	for _, pattern := range t.EnvMaskPatterns {
		for k, v := range getEnvForMasking() {
			if matchEnvKey(k, pattern) {
				masks = append(masks, v)
			}
		}
	}
	return masks
}

var getEnvForMasking = func() map[string]string {
	m := make(map[string]string)
	for _, e := range osEnviron() {
		i := strings.IndexByte(e, '=')
		if i < 0 {
			continue
		}
		m[e[:i]] = e[i+1:]
	}
	return m
}

func (t *TerminalTool) applyCompression(command, output string) string {
	if !OutputCompressors.Enabled {
		return output
	}
	compress := t.CompressOutput
	if t.CompressionResolver != nil {
		compress = t.CompressionResolver()
	}
	if !compress {
		return output
	}
	if compressed, ok := CompressOutput(command, output); ok {
		return compressed
	}
	return output
}

func (t *TerminalTool) truncate(output string) string {
	res := TruncateTail(output, DefaultMaxLines, DefaultMaxBytes)
	// Sanitize: command output is untrusted — raw ESC bytes reaching the TUI
	// renderer would be executed by the terminal (erase line, colors).
	return ansi.Sanitize(res.Content)
}

func (t *TerminalTool) IsRetryable(err error) bool { return false }

// Access returns shell category for all terminal executions.
func (t *TerminalTool) Access(input string) ToolAccess {
	return ToolAccess{Category: "shell"}
}

//go:embed terminal.short.md terminal.long.md
var terminalDocs embed.FS

func (t *TerminalTool) ShortDoc() string { return readDoc(terminalDocs, "terminal.short.md") }
func (t *TerminalTool) LongDoc() string  { return readDoc(terminalDocs, "terminal.long.md") }

func (t *TerminalTool) Examples() []string {
	return []string{
		`{"command": "ls -la"}`,
		`{"command": "go build ./...", "timeout": 120}`,
	}
}

// osEnviron is overridden in tests.
var osEnviron = func() []string { return os.Environ() }
