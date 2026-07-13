// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/sandbox"
	"github.com/pijalu/goa/internal/secrets"
)

// BashTool executes shell commands locally with security controls:
// blocked/allowed command filtering, env variable masking, optional
// built-in output compression for token-heavy commands, and an optional
// project-directory jail that prevents commands from escaping the codebase.
type BashTool struct {
	WorktreeMgr     *internal.WorktreeManager
	Blocked         []string
	Allowed         []string
	EnvMaskPatterns []string
	CompressOutput  bool
	ProjectDir      string
	Jail            bool
	// EnableComplexity, when true, enables the AST complexity analyzer and
	// advertises the restriction in the tool description. When false, only
	// blocked/allowed lists and project jail are enforced.
	EnableComplexity bool
	// Analyzer performs AST-based static analysis of shell commands when
	// non-nil. Complexity checks run only when EnableComplexity is true.
	Analyzer *sandbox.Analyzer
	// Redactor removes secrets from command output before it is returned to
	// the model. When nil, no secret scanning is performed.
	Redactor *secrets.Redactor
	// MaxOutputLines caps the number of lines returned to the agent.
	// Zero defaults to DefaultMaxLines.
	MaxOutputLines int
	// MaxOutputBytes caps the byte size of returned output.
	// Zero defaults to DefaultMaxBytes.
	MaxOutputBytes int

	// CompressionResolver, when non-nil, is called at execution time to
	// determine whether output compression is active. This enables
	// per-model/provider resolution instead of a static config bool.
	// When nil, CompressOutput is used as a fallback.
	CompressionResolver func() bool
}

// Bash timeout defaults.
const (
	// DefaultBashTimeoutS is the foreground timeout when none is specified.
	DefaultBashTimeoutS = 60
	// MaxBashTimeoutS is the maximum foreground timeout allowed.
	MaxBashTimeoutS = 5 * 60
)

// LoopHints supplies tool-loop-controller metadata so the controller does not
// need to special-case the "bash" name: raw arguments heal under "command",
// and the TUI shows "Running: <command>" while a call is in flight.
func (t *BashTool) LoopHints() agentic.ToolLoopHints {
	return agentic.ToolLoopHints{HealArg: "command", Status: commandRunStatus}
}

// Schema returns the tool schema for bash.
func (t *BashTool) Schema() agentic.ToolSchema {
	description := "Run a shell command."
	if t.EnableComplexity {
		description = "Run a shell command. Complex scripts may be rejected — use simple commands."
	}
	return agentic.ToolSchema{
		Name:        "bash",
		Description: description,
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "command to execute",
				},
				"timeout": map[string]any{
					"type":        "integer",
					"description": fmt.Sprintf("timeout (default: %ds, max: %ds)", DefaultBashTimeoutS, MaxBashTimeoutS),
				},
				"workdir": map[string]any{
					"type":        "string",
					"description": "working directory (default: project root)",
				},
				"env": map[string]any{
					"type":                 "object",
					"additionalProperties": map[string]any{"type": "string"},
					"description":          "env vars (values matching *KEY*, *TOKEN*, *SECRET*, *PASSWORD* masked)",
				},
			},
			"required": []string{"command"},
		},
	}
}

// bashParams holds the parsed input for BashTool.
type bashParams struct {
	Command string            `json:"command"`
	Timeout int               `json:"timeout"`
	Workdir string            `json:"workdir"`
	Env     map[string]string `json:"env"`
}

// Execute runs the shell command with security checks.
func (t *BashTool) Execute(input string) (string, error) {
	return t.ExecuteContext(context.Background(), input)
}

// ExecuteContext runs the shell command with security checks, forwarding the
// caller's context so a cancelled turn (Stop() / user cancellation) kills the
// running process tree instead of waiting for the timeout to elapse.
func (t *BashTool) ExecuteContext(ctx context.Context, input string) (string, error) {
	var p bashParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		return "", toolErr("bash", "invalid_input", fmt.Sprintf("Cannot parse parameters: %v", err))
	}
	if p.Command == "" {
		return "", toolErr("bash", "missing_command", "No command provided")
	}

	if err := t.checkBlocked(p.Command); err != nil {
		return "", err
	}
	if len(t.Allowed) > 0 {
		if err := t.checkAllowed(p.Command); err != nil {
			return "", err
		}
	}
	if err := t.checkAnalyzed(p.Command); err != nil {
		return "", err
	}
	if err := t.checkJail(&p); err != nil {
		return "", err
	}

	output, duration, timedOut, err := t.runCommand(ctx, &p)

	// A cancelled turn takes precedence over the timeout/exit reporting so
	// the agent stops promptly instead of emitting a timeout bubble.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", toolErr("bash", "cancelled", fmt.Sprintf("Command cancelled: %v", ctxErr))
	}
	if timedOut {
		actualTimeout := normalizeBashTimeout(p.Timeout)
		return "", toolErr("bash", "timeout", fmt.Sprintf("Command timed out after %ds", actualTimeout))
	}

	return t.formatOutput(&p, output, err, duration)
}

func (t *BashTool) runCommand(ctx context.Context, p *bashParams) ([]byte, time.Duration, bool, error) {
	timeoutS := normalizeBashTimeout(p.Timeout)

	cmd := newBashCommand(p.Command)
	if p.Workdir != "" {
		cmd.Dir = p.Workdir
	}
	// Apply the caller-provided env on top of the current environment so the
	// advertised `env` parameter actually reaches the child process. Masking of
	// sensitive values happens later on the captured output (formatOutput).
	cmd.Env = buildCommandEnv(p.Env)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// If the host injected a progress emitter, stream stdout to it so the TUI
	// shows live output for long-running commands instead of a frozen spinner.
	progress := agentic.ProgressFromContext(ctx)
	var pw *progressWriter
	if progress != nil {
		pw = newProgressWriter(&stdout, progress, bashProgressInterval)
		cmd.Stdout = pw
	}

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, 0, false, err
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var timedOut bool
	select {
	case runErr := <-done:
		pw.finalFlush()
		output := stdout.Bytes()
		output = append(output, stderr.Bytes()...)
		return output, time.Since(start), false, runErr
	case <-ctx.Done():
		// Turn cancellation: kill the whole process tree promptly so a stopped
		// turn does not keep child processes alive until the bash timeout.
		killBashProcessTree(cmd)
		<-done
	case <-time.After(time.Duration(timeoutS) * time.Second):
		timedOut = true
		killBashProcessTree(cmd)
		<-done
	}

	pw.finalFlush()
	output := stdout.Bytes()
	output = append(output, stderr.Bytes()...)
	return output, time.Since(start), timedOut, nil
}

// normalizeBashTimeout applies defaults and caps.
// A zero or negative timeout falls back to DefaultBashTimeoutS.
// Values above MaxBashTimeoutS are clamped.
func normalizeBashTimeout(timeout int) int {
	if timeout <= 0 {
		return DefaultBashTimeoutS
	}
	if timeout > MaxBashTimeoutS {
		return MaxBashTimeoutS
	}
	return timeout
}

// bashProgressInterval is the minimum spacing between two streamed progress
// snapshots for a running bash command. It keeps a fast-producing command from
// flooding the TUI with redraws while still updating well within a human
// perceptible window.
const bashProgressInterval = 120 * time.Millisecond

// progressWriter copies every write to buf (so the final output is preserved
// unchanged) and, no more than once per interval, reports the output-so-far to
// the host via emit. The snapshot is taken under the lock and the emit happens
// outside it so a slow observer never blocks the child's pipe drain.
type progressWriter struct {
	mu       sync.Mutex
	buf      *bytes.Buffer
	emit     func(string)
	interval time.Duration
	last     time.Time
}

func newProgressWriter(buf *bytes.Buffer, emit func(string), interval time.Duration) *progressWriter {
	return &progressWriter{buf: buf, emit: emit, interval: interval}
}

func (w *progressWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	n, err := w.buf.Write(p)
	due := w.emit != nil && time.Since(w.last) >= w.interval
	var snap string
	if due {
		w.last = time.Now()
		snap = w.buf.String()
	}
	w.mu.Unlock()
	if due {
		w.emit(snap)
	}
	return n, err
}

// finalFlush reports the final output-so-far so the last chunk produced just
// before exit is not held back by the debounce interval.
func (w *progressWriter) finalFlush() {
	if w == nil || w.emit == nil {
		return
	}
	w.mu.Lock()
	snap := w.buf.String()
	w.mu.Unlock()
	if snap != "" {
		w.emit(snap)
	}
}

func (t *BashTool) formatOutput(p *bashParams, output []byte, cmdErr error, duration time.Duration) (string, error) {
	// Return raw command output without [bash:] / Exit: metadata.
	// Duration is appended as a footer for the renderer to parse.
	// Non-zero exit codes are returned as errors with output included.
	var buf bytes.Buffer

	if len(output) > 0 {
		masked := maskOutput(string(output), t.buildMasks(p.Env))
		masked = t.redactOutput(masked)
		masked = t.applyCompression(p.Command, masked)
		maxLines := t.MaxOutputLines
		if maxLines <= 0 {
			maxLines = DefaultMaxLines
		}
		maxBytes := t.MaxOutputBytes
		if maxBytes <= 0 {
			maxBytes = DefaultMaxBytes
		}
		truncRes := TruncateTail(masked, maxLines, maxBytes)
		truncOutput := truncRes.Content
		if truncRes.Truncated {
			if path, err := SaveTruncatedOutput(masked); err == nil {
				fmt.Fprintf(&buf, "Output truncated: %s\nFull output saved to: %s\n", TruncResString(truncRes), path)
			} else {
				fmt.Fprintf(&buf, "Output truncated: %s\n", TruncResString(truncRes))
			}
		}
		fmt.Fprintf(&buf, "%s\n", truncOutput)
	}

	// Append duration footer for renderer
	if duration > 0 {
		fmt.Fprintf(&buf, "Duration: %.2fs\n", duration.Seconds())
	}

	if cmdErr != nil {
		// Non-zero exit: return output as error. Do not attach a
		// generic hint; non-zero exits are normal for many commands (e.g. grep
		// with no matches) and the exit code is sufficient information.
		return strings.TrimSpace(buf.String()), &internal.ToolError{
			Tool: "bash", Type: "non_zero_exit",
			Detail: fmt.Sprintf("Command exited with code %d\n%s", exitCode(cmdErr), strings.TrimSpace(buf.String())),
		}
	}

	return strings.TrimSpace(buf.String()), nil
}

// Helper functions for Execute.

func toolErr(tool, typ, detail string) *internal.ToolError {
	return &internal.ToolError{
		Tool: tool, Type: typ, Detail: detail,
		HintText: "See /docs TOOLS or /tools " + tool + " for usage.",
	}
}

func (t *BashTool) applyCompression(command, output string) string {
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

func (t *BashTool) redactOutput(output string) string {
	if t.Redactor == nil {
		return output
	}
	redacted, _ := t.Redactor.Redact(output)
	return redacted
}

func (t *BashTool) IsRetryable(err error) bool {
	return false
}

// Access returns shell category for all bash executions.
func (t *BashTool) Access(input string) ToolAccess {
	return ToolAccess{Category: "shell"}
}

//go:embed bash.short.md bash.long.md
var bashDocs embed.FS

func (t *BashTool) ShortDoc() string { return readDoc(bashDocs, "bash.short.md") }
func (t *BashTool) LongDoc() string {
	doc := fmt.Sprintf(readDoc(bashDocs, "bash.long.md"), DefaultBashTimeoutS, MaxBashTimeoutS)
	if t.EnableComplexity {
		doc += "\n\n" + t.ComplexityNotice()
	}
	return doc
}

// ComplexityNotice returns the text that tells the agent that bash complexity
// analysis is enabled and that scripts must be simple enough to be analyzed.
func (t *BashTool) ComplexityNotice() string {
	return "Complexity analysis is enabled for this bash tool. Scripts must be statically analyzable: avoid command substitution, variable expansion in command position, loops, conditionals, and other complex constructs. The analyzer will reject commands that exceed the configured complexity threshold or that cannot be evaluated safely."
}

func (t *BashTool) Examples() []string {
	return []string{
		`{"command": "ls -la"}`,
		`{"command": "go build ./...", "timeout": 120}`,
		`{"command": "npm test", "workdir": "/projects/myapp", "timeout": 60}`,
	}
}

// firstCommandToken extracts the first command name from a shell command.
// Handles simple quoting but does not fully lex — enough for security matching.
func firstCommandToken(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}
	// Skip leading variable assignments (e.g. "FOO=bar make install" → "make")
	// Skip leading redirects (e.g. ">/dev/null ls" → "ls")
	for i := 0; i < len(cmd); {
		start := i
		i = advanceShellWord(cmd, i)
		word := cmd[start:i]
		i = skipShellSpace(cmd, i)
		if isCommandWord(word) {
			return word
		}
	}
	return ""
}

// advanceShellWord advances i past one shell word, respecting simple quotes
// and escapes. It returns the index after the word.
func advanceShellWord(cmd string, i int) int {
	for i < len(cmd) && cmd[i] != ' ' && cmd[i] != '\t' {
		if q := cmd[i]; q == '\\' || q == '\'' || q == '"' {
			i = skipQuoted(cmd, i+1, q)
			continue
		}
		i++
	}
	return i
}

func skipQuoted(cmd string, i int, quote byte) int {
	for i < len(cmd) && cmd[i] != quote {
		if cmd[i] == '\\' && quote == '\'' {
			break
		}
		i++
	}
	if i < len(cmd) {
		i++ // consume closing quote
	}
	return i
}

// skipShellSpace advances i past spaces and tabs.
func skipShellSpace(cmd string, i int) int {
	for i < len(cmd) && (cmd[i] == ' ' || cmd[i] == '\t') {
		i++
	}
	return i
}

// isCommandWord reports whether word is a command token rather than an
// assignment, redirect, or empty string.
func isCommandWord(word string) bool {
	return word != "" && !strings.Contains(word, "=") && !strings.HasPrefix(word, ">")
}

// checkBlocked checks if the command's first token matches any blocked pattern.
func (t *BashTool) checkBlocked(cmd string) error {
	program := firstCommandToken(cmd)
	for _, blocked := range t.Blocked {
		if program == blocked {
			return &internal.ToolError{
				Tool: "bash", Type: "blocked_command",
				Detail:   fmt.Sprintf("Command %q is blocked: %q", blocked, cmd),
				HintText: "This command is not allowed for security reasons.",
			}
		}
	}
	return nil
}

// checkAllowed checks if the command's first token is in the allowed list.
func (t *BashTool) checkAllowed(cmd string) error {
	program := firstCommandToken(cmd)
	for _, allowed := range t.Allowed {
		if program == allowed {
			return nil
		}
	}
	return &internal.ToolError{
		Tool: "bash", Type: "command_not_allowed",
		Detail:   fmt.Sprintf("Command %q is not in the allowed list", program),
		HintText: "Use one of the allowed commands or update tools.bash.allowed_commands in config.",
	}
}

// checkAnalyzed runs the AST-based analyzer when configured and rejects
// blocked commands, commands outside the allowed list, and commands that
// cannot be analyzed statically (e.g. dynamic command construction).
func (t *BashTool) checkAnalyzed(cmd string) error {
	if t.Analyzer == nil {
		return nil
	}
	res, err := t.Analyzer.Analyze(cmd)
	if err != nil {
		return toolErr("bash", "analysis_error", fmt.Sprintf("Failed to analyze command: %v", err))
	}
	if res.TooComplex {
		return &internal.ToolError{
			Tool: "bash", Type: "command_too_complex",
			Detail:   fmt.Sprintf("Command cannot be analyzed statically: %s", res.Reason),
			HintText: "Simplify the command or avoid dynamic command construction (command substitution, variables in command position).",
		}
	}
	if res.Blocked {
		return &internal.ToolError{
			Tool: "bash", Type: "blocked_command",
			Detail:   fmt.Sprintf("Blocked command detected: %s", res.Reason),
			HintText: "This command is not allowed for security reasons.",
		}
	}
	if !res.Allowed {
		return &internal.ToolError{
			Tool: "bash", Type: "command_not_allowed",
			Detail:   fmt.Sprintf("Command not in allowed list: %s", res.Reason),
			HintText: "Use one of the allowed commands or update tools.bash.allowed_commands in config.",
		}
	}
	return nil
}

// checkJail enforces project-directory containment when Jail is enabled.
// It rejects commands that reference paths outside ProjectDir and ensures
// the working directory stays inside ProjectDir.
func (t *BashTool) checkJail(p *bashParams) error {
	if !t.Jail || t.ProjectDir == "" {
		return nil
	}
	base, err := filepath.Abs(t.ProjectDir)
	if err != nil {
		return toolErr("bash", "jail_error", fmt.Sprintf("Cannot resolve project directory: %v", err))
	}
	if bashReferencesOutsidePath(p.Command, base) {
		return &internal.ToolError{
			Tool: "bash", Type: "jail_violation",
			Detail:   fmt.Sprintf("Command references a path outside the project directory %q", base),
			HintText: "Avoid using .., absolute paths outside the project, or cd commands that leave the codebase.",
		}
	}
	if p.Workdir != "" && !pathUnderDir(p.Workdir, base) {
		return &internal.ToolError{
			Tool: "bash", Type: "jail_violation",
			Detail:   fmt.Sprintf("Working directory %q is outside the project directory %q", p.Workdir, base),
			HintText: "Use a workdir inside the project directory.",
		}
	}
	if p.Workdir == "" {
		p.Workdir = base
	}
	return nil
}

// buildMasks creates a list of secret values to mask in output.
func (t *BashTool) buildMasks(env map[string]string) []string {
	var masks []string
	for k, v := range env {
		for _, pattern := range t.EnvMaskPatterns {
			if matchEnvKey(k, pattern) {
				masks = append(masks, v)
				break
			}
		}
	}
	return masks
}

// matchEnvKey checks if a key matches a glob-like pattern (*KEY*).
func matchEnvKey(key, pattern string) bool {
	// Simple glob matching: * matches any sequence
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return key == pattern
	}
	idx := 0
	for _, part := range parts {
		if part == "" {
			continue
		}
		pos := strings.Index(key[idx:], part)
		if pos < 0 {
			return false
		}
		idx += pos + len(part)
	}
	return true
}

// maskOutput replaces sensitive values in command output.
func maskOutput(output string, masks []string) string {
	for _, mask := range masks {
		if mask != "" {
			output = strings.ReplaceAll(output, mask, "***")
		}
	}
	return output
}

// buildCommandEnv returns an env slice for exec.Cmd: the current process
// environment overlaid with the caller-provided variables.
func buildCommandEnv(extra map[string]string) []string {
	env := os.Environ()
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}

func newBashCommand(command string) *exec.Cmd {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	cmd := exec.Command(shell, "-c", command)
	return configureBashCommand(cmd)
}

// exitCode extracts the exit code from an exec error.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return -1
}

// truncateCommand shortens a command for display.
func truncateCommand(cmd string, maxLen int) string {
	if maxLen <= 0 {
		return "..."
	}
	if len(cmd) <= maxLen {
		return cmd
	}
	if maxLen <= 3 {
		return cmd[:maxLen]
	}
	return cmd[:maxLen-3] + "..."
}
