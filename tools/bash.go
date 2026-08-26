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
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/sandbox"
	"github.com/pijalu/goa/internal/secrets"
)

// Regexes backing detectShellFileEdit (the bash→edit guardrail).
var (
	// redirectRe captures a shell output redirect target: "> path", ">> path",
	// "2> path", "tee path". Excludes fd-dup forms (>&2, 2>&1) via the target
	// filter in redirectEditTarget. The target stops at whitespace, &, |, ;.
	redirectRe = regexp.MustCompile(`(?:>>?|\btee)\s*(?:\d>>?)?\s*([^\s&|;><]+)`)
	// inPlaceEditorRe matches in-place file editors: sed -i, perl -pi/-i, ed.
	inPlaceEditorRe = regexp.MustCompile(`\bsed\s+[^|;]*-i|\bperl\s+[^|;]*-[a-z]*i|\bed\s+[^|;]*\s`)
	// interpreterRe matches invoking a script interpreter that could write files.
	interpreterRe = regexp.MustCompile(`\b(node|deno|bun|python\d*|python3|ruby|perl|php|os\.system|subprocess)\b`)
	// interpreterWriteRe matches a file-write API call inside an inline script:
	// node writeFileSync, python open(...,'w'/'a')/Path(...).write_text, ruby
	// File.write, plus shell-ish fs.createWriteStream.
	interpreterWriteRe = regexp.MustCompile(`writeFileSync|createWriteStream|open\s*\([^)]*['"][wa]['"]|write_text|write_bytes|File\.(write|open\s*\([^)]*['"][wa]['"])|fs\.write|with\s+open\s*\([^)]*['"][wa]['"]`)
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
	// MaxCaptureBytes bounds combined stdout/stderr while the command runs.
	// Zero defaults to DefaultMaxCaptureBytes. Exceeding it terminates the
	// command and returns a clear output-too-large error.
	MaxCaptureBytes int

	// CompressionResolver, when non-nil, is called at execution time to
	// determine whether output compression is active. This enables
	// per-model/provider resolution instead of a static config bool.
	// When nil, CompressOutput is used as a fallback.
	CompressionResolver func() bool

	// WarnFileEdits, when true, prepends a non-blocking hint to the output of
	// shell commands that modify project files (redirects, in-place editors,
	// interpreter inline file writes), steering the model to the edit tool
	// Never blocks. Configurable via tools.bash.warn_file_edits.
	WarnFileEdits bool
	// WarnFileEditsResolver, when non-nil, is called at execution time to
	// decide whether the hint is active (live /config toggle). When nil,
	// WarnFileEdits is used as the static fallback.
	WarnFileEditsResolver func() bool

	// EscalationApprover approves or rejects a sandbox escalation before an
	// escalated (wider) command runs. It is injected by the host (App.Run)
	// and routes through the same perms-driven approval path as tool
	// confirmation. When nil, escalations are denied — fail closed.
	EscalationApprover sandbox.EscalationApprover

	// LSPSyncer, when set, reconciles language-server document overlays with
	// on-disk content after every command run. Shell commands mutate files
	// behind goa's structured file tools (sed -i, git checkout, gofmt -w);
	// LSP servers ignore disk changes under an open overlay, so without this
	// hook they keep analyzing stale buffers and report phantom errors
	// (Issue: gopls flagged declarations a bash-side file split had already
	// removed from plugin.go).
	LSPSyncer ExternalLSPSyncer
}

// Bash timeout defaults.
const (
	// DefaultBashTimeoutS is the foreground timeout when none is specified.
	DefaultBashTimeoutS = 60
	// MaxBashTimeoutS is the maximum foreground timeout allowed.
	MaxBashTimeoutS = 5 * 60
	// DefaultMaxCaptureBytes limits captured command output to 10 MiB.
	DefaultMaxCaptureBytes = 10 * 1024 * 1024
)

// LoopHints supplies tool-loop-controller metadata so the controller does not
// need to special-case the "bash" name: raw arguments heal under "command",
// and the TUI shows "Running: <command>" while a call is in flight.
func (t *BashTool) LoopHints() agentic.ToolLoopHints {
	return agentic.ToolLoopHints{HealArg: "command", Status: commandRunStatus}
}

// lspResyncTimeout bounds post-command overlay reconciliation so reporting
// command output is never delayed by more than this.
const lspResyncTimeout = 2 * time.Second

// ExternalLSPSyncer reconciles documents a language server holds open with
// their on-disk content, returning how many were refreshed. Defined here (at
// the consumer) so bash tests can inject a tiny fake instead of the manager.
type ExternalLSPSyncer interface {
	ResyncExternal(ctx context.Context) int
}

// resyncLSP pushes fresh disk content into open language-server overlays that
// the just-finished command may have modified externally. Best-effort: a nil
// syncer, a cancelled turn, or an over-deadline reconcile never fails the
// command whose output was already captured.
func (t *BashTool) resyncLSP(ctx context.Context) {
	if t.LSPSyncer == nil || ctx.Err() != nil {
		return
	}
	syncCtx, cancel := context.WithTimeout(ctx, lspResyncTimeout)
	defer cancel()
	_ = t.LSPSyncer.ResyncExternal(syncCtx)
}

// Schema returns the tool schema for bash.
func (t *BashTool) Schema() agentic.ToolSchema {
	// The working-directory statement lives in the top-level description because
	// that is the text the model actually reads; without it the model prepends a
	// redundant "cd <project root> && " to every command.
	const cwdNote = " The working directory is the project root by default — do not prepend `cd <project root>` unless a different directory is required."
	description := "Run a shell command." + cwdNote
	if t.EnableComplexity {
		description = "Run a shell command. Complex scripts may be rejected — use simple commands." + cwdNote
	}
	schema := map[string]any{
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
	}
	// The sandbox escalation surface is advertised ONLY when confinement is
	// active; non-sandboxed builds must hide these fields entirely.
	if t.confinementActive() {
		props := schema["properties"].(map[string]any)
		props["sandbox_permissions"] = map[string]any{
			"type":        "string",
			"enum":        sandbox.EscalationVocabulary,
			"description": "the wider sandbox permission a jail-denied command needs, from the closed vocabulary; required together with justification; a non-widening request fails without prompting",
		}
		props["justification"] = map[string]any{
			"type":        "string",
			"description": "one sentence for the user explaining why this exact command needs the wider sandbox permission; required together with sandbox_permissions",
		}
		// Required-with: each escalation field demands the other (JSON Schema
		// dependentRequired, 2019-09+). The tool also enforces this at execution.
		schema["dependentRequired"] = map[string]any{
			"sandbox_permissions": []string{"justification"},
			"justification":       []string{"sandbox_permissions"},
		}
	}
	return agentic.ToolSchema{
		Name:        "bash",
		Description: description,
		Schema:      schema,
	}
}

// confinementActive reports whether bash is running under project-directory
// confinement (the sandbox surface). When false the escalation fields are
// hidden from the schema and escalation requests are rejected.
func (t *BashTool) confinementActive() bool {
	return t.Jail && t.ProjectDir != ""
}

// currentSandboxMode returns the effective confinement mode of this call.
// The goa project-directory jail confines writes to the workspace root, which
// corresponds to ModeWorkspaceWrite in the mode vocabulary. The only strictly
// wider mode is danger-full-access.
func (t *BashTool) currentSandboxMode() sandbox.Mode {
	return sandbox.ModeWorkspaceWrite
}

// bashParams holds the parsed input for BashTool.
type bashParams struct {
	Command            string            `json:"command"`
	Timeout            int               `json:"timeout"`
	Workdir            string            `json:"workdir"`
	Env                map[string]string `json:"env"`
	SandboxPermissions string            `json:"sandbox_permissions"`
	Justification      string            `json:"justification"`
}

// Execute runs the shell command with security checks.
func (t *BashTool) Execute(input string) (string, error) {
	return t.ExecuteContext(context.Background(), input)
}

// ExecuteContext runs the shell command with security checks, forwarding the
// caller's context so a cancelled turn (Stop() / user cancellation) kills the
// running process tree instead of waiting for the timeout to elapse.
func (t *BashTool) ExecuteContext(ctx context.Context, input string) (string, error) {
	p, err := t.validateInput(ctx, input)
	if err != nil {
		return "", err
	}

	// Non-blocking nudge: if the command modifies a project file via
	// the shell, we still run it but prepend a hint steering the model to the
	// edit tool next time. Never block on this — bash is sometimes the only way.
	fileEditHint := t.fileEditHint(p.Command)

	output, duration, timedOut, tooLarge, runErr := t.runCommand(ctx, p)
	// The command may have mutated files outside goa's file tools; refresh
	// open LSP overlays BEFORE the model sees the result so follow-up edits
	// and diagnostics reflect on-disk reality, not stale server buffers.
	t.resyncLSP(ctx)
	return t.reportResult(ctx, p, fileEditHint, output, duration, timedOut, tooLarge, runErr)
}

// validateInput parses and gates a bash call before anything runs. Every
// guard fails closed with an early ToolError return: JSON decoding, required
// command field, escalation-field pairing, blocked/allowed command lists,
// optional AST analysis, and project-directory confinement.
func (t *BashTool) validateInput(ctx context.Context, input string) (*bashParams, error) {
	var p bashParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		return nil, toolErr("bash", "invalid_input", fmt.Sprintf("Cannot parse parameters: %v", err))
	}
	if p.Command == "" {
		return nil, toolErr("bash", "missing_command", "No command provided")
	}
	if err := t.validateEscalationInput(&p); err != nil {
		return nil, err
	}
	if err := t.checkBlocked(p.Command); err != nil {
		return nil, err
	}
	if len(t.Allowed) > 0 {
		if err := t.checkAllowed(p.Command); err != nil {
			return nil, err
		}
	}
	if err := t.checkAnalyzed(p.Command); err != nil {
		return nil, err
	}
	if err := t.enforceConfinement(ctx, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// reportResult applies the post-run precedence ladder exactly:
// cancelled beats timeout beats tooLarge. A cancelled turn wins over every
// outcome so a stopped turn stops promptly instead of emitting a timeout
// bubble; a timeout wins over an oversized capture; only otherwise is the
// captured output reported, with the non-blocking file-edit hint prepended.
func (t *BashTool) reportResult(ctx context.Context, p *bashParams, fileEditHint string, output []byte, duration time.Duration, timedOut, tooLarge bool, runErr error) (string, error) {
	// A cancelled turn takes precedence over the timeout/exit reporting so
	// the agent stops promptly instead of emitting a timeout bubble.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", toolErr("bash", "cancelled", fmt.Sprintf("Command cancelled: %v", ctxErr))
	}
	if timedOut {
		return "", timeoutErr(normalizeBashTimeout(p.Timeout))
	}

	out, fmtErr := t.formatOutput(p, output, runErr, duration)
	if tooLarge {
		return "", outputTooLargeErr(t.captureLimit())
	}
	if fileEditHint != "" {
		out = fileEditHint + out
	}
	return out, fmtErr
}

func (t *BashTool) runCommand(ctx context.Context, p *bashParams) ([]byte, time.Duration, bool, bool, error) {
	timeoutS := normalizeBashTimeout(p.Timeout)

	cmd := newBashCommand(p.Command)
	// Default workdir to ProjectDir when not explicitly set. This eliminates
	// the need for the model to prefix every command with "cd /project && ".
	if p.Workdir != "" {
		cmd.Dir = p.Workdir
	} else if t.ProjectDir != "" {
		cmd.Dir = t.ProjectDir
	}
	// Apply the caller-provided env on top of the current environment so the
	// advertised `env` parameter actually reaches the child process. Masking of
	// sensitive values happens later on the captured output (formatOutput).
	cmd.Env = buildCommandEnv(p.Env)

	var stdout, stderr bytes.Buffer
	capture := newCaptureWriter(&stdout, &stderr, t.captureLimit(), func() { killBashProcessTree(cmd) })
	cmd.Stdout = capture.stdoutWriter()
	cmd.Stderr = capture.stderrWriter()

	// If the host injected a progress emitter, stream stdout to it so the TUI
	// shows live output for long-running commands instead of a frozen spinner.
	progress := agentic.ProgressFromContext(ctx)
	var pw *progressWriter
	if progress != nil {
		pw = newProgressWriterWriter(capture.stdoutWriter(), &stdout, progress, bashProgressInterval)
		cmd.Stdout = pw
	}

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, 0, false, false, err
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var timedOut bool
	select {
	case runErr := <-done:
		pw.finalFlush()
		output := stdout.Bytes()
		output = append(output, stderr.Bytes()...)
		return output, time.Since(start), false, capture.exceeded(), runErr
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
	return output, time.Since(start), timedOut, capture.exceeded(), nil
}

func (t *BashTool) captureLimit() int {
	if t.MaxCaptureBytes > 0 {
		return t.MaxCaptureBytes
	}
	return DefaultMaxCaptureBytes
}

func outputTooLargeErr(limit int) *internal.ToolError {
	return &internal.ToolError{Tool: "bash", Type: "output_too_large", Detail: fmt.Sprintf("Command output exceeded the %d-byte limit and was aborted", limit), HintText: "Reduce the command output, filter it, or increase max_capture_bytes."}
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
	tee      io.Writer
	emit     func(string)
	interval time.Duration
	last     time.Time
}

func newProgressWriter(buf *bytes.Buffer, emit func(string), interval time.Duration) *progressWriter {
	return newProgressWriterWriter(buf, buf, emit, interval)
}

func newProgressWriterWriter(tee io.Writer, buf *bytes.Buffer, emit func(string), interval time.Duration) *progressWriter {
	return &progressWriter{buf: buf, tee: tee, emit: emit, interval: interval}
}

func (w *progressWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	n, err := w.tee.Write(p)
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
		// Sanitize last: command output is untrusted — raw ESC bytes would
		// reach both the model context and the TUI renderer, where a stray
		// "\e[2K" erases the user's screen. Keep sequences visible as `\e...`.
		masked = ansi.Sanitize(masked)
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

// timeoutErr builds the timeout error with a timeout-specific actionable hint
// (Timeout hint): raise the `timeout` parameter when there is
// headroom, otherwise split the work — never the generic docs usage line.
func timeoutErr(actualTimeout int) *internal.ToolError {
	hint := fmt.Sprintf("The command exceeded the %ds timeout. Increase the \"timeout\" parameter (default: %ds, max: %ds) or split the command into smaller/faster steps.",
		actualTimeout, DefaultBashTimeoutS, MaxBashTimeoutS)
	if actualTimeout >= MaxBashTimeoutS {
		hint = fmt.Sprintf("The command exceeded the maximum %ds timeout. Split the command into smaller/faster steps or run it in the background with bgexec.", MaxBashTimeoutS)
	}
	return &internal.ToolError{
		Tool: "bash", Type: "timeout",
		Detail:   fmt.Sprintf("Command timed out after %ds", actualTimeout),
		HintText: hint,
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

// MutatesState reports that a successful bash command may change shared state
// (files, build artifacts, processes). The loop guardrails treat it as a state
// mutation that resets the no-progress repeat horizon.
func (t *BashTool) MutatesState() bool { return true }

//go:embed bash.short.md bash.long.md
var bashDocs embed.FS

func (t *BashTool) ShortDoc() string { return readDoc(bashDocs, "bash.short.md") }
func (t *BashTool) LongDoc() string {
	doc := fmt.Sprintf(readDoc(bashDocs, "bash.long.md"), DefaultBashTimeoutS, MaxBashTimeoutS)
	if t.EnableComplexity {
		doc += "\n\n" + t.ComplexityNotice()
	}
	if t.confinementActive() {
		doc += "\n\n" + t.SandboxEscalationNotice()
	}
	return doc
}

// SandboxEscalationNotice returns the text that tells the agent about the
// sandbox escalation surface: jail-denied commands may be retried once with a
// strictly wider sandbox_permissions mode + justification, gated by user
// approval. Only included in the doc when confinement is active.
func (t *BashTool) SandboxEscalationNotice() string {
	return fmt.Sprintf("Sandbox escalation: this bash runs confined to the project directory (mode %q). A command denied by the sandbox may be retried ONCE with the narrowest strictly-wider `sandbox_permissions` (%s) plus a `justification`; the user must approve the escalation before it runs. Non-widening requests fail without prompting. Escalation is never speculative — do not attach these fields to commands that do not need wider access.", t.currentSandboxMode(), strings.Join(sandbox.EscalationVocabulary, "/"))
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

// fileEditHint returns a non-blocking hint to prepend to the command output
// when the command modifies a project file via the shell, steering the model to
// the edit tool next time. It NEVER blocks — the command always runs
// a hard block broke legitimate workflows; a visible nudge is enough). Returns
// "" when the command looks read-only or the hint is disabled. Conservative:
// on doubt it stays silent rather than nag.
// warnFileEditsEnabled resolves whether the file-edit hint is active: the live
// /config resolver wins when present, else the static WarnFileEdits field.
func (t *BashTool) warnFileEditsEnabled() bool {
	if t.WarnFileEditsResolver != nil {
		return t.WarnFileEditsResolver()
	}
	return t.WarnFileEdits
}

func (t *BashTool) fileEditHint(cmd string) string {
	if !t.warnFileEditsEnabled() {
		return ""
	}
	if reason := detectShellFileEdit(cmd); reason != "" {
		return fmt.Sprintf("Note: this command modified a file via the shell (%s). Prefer the 'edit' tool for project file changes (search/replace, replace_lines, insert_after/before, delete_lines) — it is safer and drift-aware. If an edit failed with not_found, re-read the region with 'read' and apply a smaller, tightly-anchored edit.\n", reason)
	}
	return ""
}

// detectShellFileEdit returns a short reason when the command appears to modify
// a file via the shell, or "" when it looks read-only. Conservative by design.
func detectShellFileEdit(cmd string) string {
	// Output redirects / tee into a file (allow /dev/null and /tmp scratch).
	if r := redirectEditTarget(cmd); r != "" {
		return "writes to file via redirect/tee: " + r
	}
	// In-place editors.
	if inPlaceEditorRe.MatchString(cmd) {
		return "in-place file edit (sed -i / perl -pi / ed)"
	}
	// Interpreter inline file writes.
	if interpreterRe.MatchString(cmd) {
		if m := interpreterWriteRe.FindString(cmd); m != "" {
			return "interpreter inline file write: " + m
		}
	}
	return ""
}

// redirectEditTarget returns the target path when cmd contains a shell redirect
// or tee that writes to a real file (not /dev/null, not /tmp scratch, not a
// file descriptor like >&2). Otherwise returns "".
func redirectEditTarget(cmd string) string {
	for _, m := range redirectRe.FindAllStringSubmatch(cmd, -1) {
		target := m[1]
		if target == "" || target == "&1" || target == "&2" {
			continue
		}
		if target == "/dev/null" || strings.HasPrefix(target, "/dev/") {
			continue
		}
		if strings.HasPrefix(target, "/tmp/") || strings.HasPrefix(target, "/var/tmp/") || strings.HasPrefix(target, "/var/folders/") {
			continue
		}
		return target
	}
	return ""
}

// validateEscalationInput enforces the required-with pairing of the sandbox
// escalation fields and rejects escalation when no sandbox is active.
func (t *BashTool) validateEscalationInput(p *bashParams) error {
	hasPerms := p.SandboxPermissions != ""
	hasJust := p.Justification != ""
	if hasPerms != hasJust {
		if hasPerms {
			return toolErr("bash", "invalid_escalation", "sandbox_permissions requires a justification")
		}
		return toolErr("bash", "invalid_escalation", "justification is only valid together with sandbox_permissions")
	}
	if !hasPerms {
		return nil
	}
	if !t.confinementActive() {
		return toolErr("bash", "escalation_unavailable", "sandbox_permissions is not available in this composition (no sandboxing executor to escalate)")
	}
	if strings.TrimSpace(p.Justification) == "" {
		return toolErr("bash", "invalid_escalation", "invalid justification: expected a non-empty sentence")
	}
	if !sandbox.Mode(p.SandboxPermissions).IsValid() {
		return toolErr("bash", "invalid_escalation", fmt.Sprintf("invalid escalation: unknown sandbox_permissions %q", p.SandboxPermissions))
	}
	return nil
}

// enforceConfinement applies project-directory confinement to the call. On a
// jail violation it returns the denial — or, when the call requests a strictly
// wider sandbox mode with a justification and the user approves through the
// escalation approver, allows the exact command to run unconfined for this
// call. Non-widening requests fail without prompting anyone; a nil approver
// keeps the denial final (fail closed).
func (t *BashTool) enforceConfinement(ctx context.Context, p *bashParams) error {
	if !t.confinementActive() {
		return nil
	}
	base, err := filepath.Abs(t.ProjectDir)
	if err != nil {
		return toolErr("bash", "jail_error", fmt.Sprintf("Cannot resolve project directory: %v", err))
	}
	workdirOK := p.Workdir == "" || pathUnderDir(p.Workdir, base)
	if !bashReferencesOutsidePath(p.Command, base) && workdirOK {
		// Confined and allowed. Escalation is never speculative: requesting
		// wider permissions for a command that needs none is rejected.
		if p.SandboxPermissions != "" {
			return toolErr("bash", "escalation_unneeded", "this command does not require wider sandbox permissions — escalation is never speculative; drop sandbox_permissions and justification and retry")
		}
		if p.Workdir == "" {
			p.Workdir = base
		}
		return nil
	}

	// Jail denial. Without an escalation request the denial is final, with a
	// marker steering the model to the escalation surface.
	current := t.currentSandboxMode()
	if p.SandboxPermissions == "" {
		return t.sandboxDenialErr(base, current, true)
	}

	requested := sandbox.Mode(p.SandboxPermissions)
	// Mode validity is enforced in validateEscalationInput; widening is the
	// execution-time check.
	if !requested.StrictlyWider(current) {
		// Non-widening: fail without prompting anyone.
		return toolErr("bash", "sandbox_not_widening", fmt.Sprintf("sandbox escalation to %q is not strictly wider than this call's current %q mode", requested, current))
	}

	approved, aerr := t.approveEscalation(ctx, p, current, requested)
	if aerr != nil {
		return aerr
	}
	if !approved {
		// Rejected (or no approval path wired): the denial is final.
		return t.sandboxDenialErr(base, current, false)
	}
	// Approved: the exact command runs unconfined for this call.
	return nil
}

// approveEscalation routes an escalation request through the approval path.
// A nil approver denies escalation (fail closed).
func (t *BashTool) approveEscalation(ctx context.Context, p *bashParams, current, requested sandbox.Mode) (bool, error) {
	if t.EscalationApprover == nil {
		return false, nil
	}
	return t.EscalationApprover(ctx, sandbox.EscalationRequest{
		ToolName:      "bash",
		Command:       p.Command,
		Workdir:       p.Workdir,
		CurrentMode:   current,
		RequestedMode: requested,
		Justification: p.Justification,
	})
}

// sandboxDenialErr builds the jail denial ToolError carrying the dsh-style
// sandbox markers so the model sees the denial fact and, when escalation is
// available, the exact retry guidance.
func (t *BashTool) sandboxDenialErr(base string, mode sandbox.Mode, escalationAvailable bool) error {
	detail := fmt.Sprintf("Command references a path outside the project directory %q\n[sandbox: file access denied under %s mode]", base, mode)
	hint := "This command is not allowed by the project sandbox for security reasons."
	if escalationAvailable {
		hint += " [sandbox: escalation available — retry this exact command once with sandbox_permissions (the narrowest wider mode that suffices) + justification; the approval prompt asks the user]"
	}
	return &internal.ToolError{
		Tool: "bash", Type: "jail_violation",
		Detail:   detail,
		HintText: hint,
	}
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

// truncateCommand shortens a command for display. The cut is rune-safe so a
// multi-byte character at the boundary never renders as '�'.
func truncateCommand(cmd string, maxLen int) string {
	if maxLen <= 0 {
		return "..."
	}
	if len(cmd) <= maxLen {
		return cmd
	}
	if maxLen <= 3 {
		return string([]rune(cmd)[:maxLen])
	}
	return string([]rune(cmd)[:maxLen-3]) + "..."
}
