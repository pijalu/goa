// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package hooks implements lifecycle event hooks for the agent.
//
// Hook commands are executed with a JSON payload on stdin. beforeTool hooks
// can veto a tool execution by exiting with a non-zero status. afterTool
// hooks receive the tool result. sessionStart and sessionEnd hooks fire at
// the boundaries of an agent session. All hook executions are recorded in an
// audit log for replay and debugging.
//
// Two dialects are supported (gap TL4). Goa-native hooks (from .goa/hooks.yaml)
// receive goa's own payload shapes and veto a beforeTool by exiting non-zero.
// Claude Code hooks (from .claude/hooks.json / .claude/settings.json) receive
// Claude Code's wire payloads, run as shell (sh -c) or exec-form commands, and
// follow the CC exit-code/JSON output codec: stdout JSON is read on every exit
// code, exit 2 blocks (JSON cannot override it), and otherwise a valid JSON
// object alone decides (permissionDecision allow/deny/ask/defer for PreToolUse,
// top-level decision block/approve elsewhere). Matching hooks run serially in
// config order and fold most-restrictively (deny/block > ask/defer > allow);
// a beforeTool veto error carries every vetoing hook's reason plus all
// additionalContext so the model sees it in the tool-result error.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Event identifies a lifecycle hook event.
type Event string

const (
	EventBeforeTool   Event = "beforeTool"
	EventAfterTool    Event = "afterTool"
	EventSessionStart Event = "sessionStart"
	EventSessionEnd   Event = "sessionEnd"
)

// Dialect identifies the configuration source (and wire format) of a hook.
type Dialect string

const (
	// DialectGoa is goa's native hooks.yaml format. Empty values default to
	// this dialect.
	DialectGoa Dialect = "goa"
	// DialectClaudeCode is a Claude Code hooks.json command hook: CC-shaped
	// stdin payloads, CC exit-code/JSON output codec, shell/exec execution.
	DialectClaudeCode Dialect = "claude-code"
)

// Config is the user-facing hook configuration.
type Config struct {
	// Hooks is the list of lifecycle hooks to execute.
	Hooks []Hook `yaml:"hooks"`
	// Warnings collects non-fatal parse notices (for example skipped
	// non-command Claude Code hooks). Populated by LoadConfig.
	Warnings []string `yaml:"-"`
}

// Hook describes a single command to execute for a lifecycle event.
type Hook struct {
	// Event is the lifecycle event that triggers this hook.
	Event Event `yaml:"event"`
	// Command is the executable to run (goa-native) or the CC command hook's
	// command string (claude-code dialect).
	Command string `yaml:"command"`
	// Args are optional fixed arguments prepended to the payload. For the
	// claude-code dialect a non-empty Args switches to CC exec form: Command
	// is resolved as an executable and spawned directly with Args, no shell.
	Args []string `yaml:"args,omitempty"`
	// Dialect selects the wire format. Empty means DialectGoa. Set by config
	// loading; never present in the YAML file.
	Dialect Dialect `yaml:"-"`
	// Matcher is the CC matcher-group pattern for PreToolUse/PostToolUse
	// (tool-name) hooks. Empty/absent/"*" match all tools; a letters/digits/
	// underscore/hyphen/space/comma/pipe pattern is an exact (case-insensitive)
	// alternative list, anything else is an unanchored Go regexp. Ignored for
	// goa-native hooks and for session events (CC session matchers are
	// discarded at parse time because goa's session payloads carry no source
	// or reason).
	Matcher string `yaml:"-"`
	// TimeoutSeconds bounds a hook run. Zero means no explicit timeout for
	// goa-native hooks and the CC default of 600s for claude-code hooks.
	TimeoutSeconds int `yaml:"-"`
	// WorkDir is the working directory for the hook process. Claude Code
	// hooks run in the project directory (and receive CLAUDE_PROJECT_DIR as
	// an environment variable); goa-native hooks run in the caller's cwd when
	// empty.
	WorkDir string `yaml:"-"`
}

// Validate returns an error if the hook configuration is invalid.
func (c Config) Validate() error {
	for i, h := range c.Hooks {
		if h.Event == "" {
			return fmt.Errorf("hook %d: event is required", i)
		}
		if h.Command == "" {
			return fmt.Errorf("hook %d: command is required", i)
		}
		switch h.Event {
		case EventBeforeTool, EventAfterTool, EventSessionStart, EventSessionEnd:
		default:
			return fmt.Errorf("hook %d: unknown event %q", i, h.Event)
		}
		switch h.Dialect {
		case "", DialectGoa, DialectClaudeCode:
		default:
			return fmt.Errorf("hook %d: unknown dialect %q", i, h.Dialect)
		}
	}
	return nil
}

// ToolPayload is the JSON payload sent to beforeTool and afterTool hooks.
type ToolPayload struct {
	Event     string `json:"event"`
	ToolName  string `json:"tool_name"`
	ToolInput string `json:"tool_input"`
	CallID    string `json:"call_id"`
	Output    string `json:"output,omitempty"`
	Error     string `json:"error,omitempty"`
	// SessionID and CWD are forwarded into Claude Code dialect payloads
	// (session_id / cwd). They are empty when the caller does not know them.
	SessionID string `json:"session_id,omitempty"`
	CWD       string `json:"cwd,omitempty"`
}

// SessionPayload is the JSON payload sent to sessionStart and sessionEnd hooks.
type SessionPayload struct {
	Event      string `json:"event"`
	SessionID  string `json:"session_id"`
	ProjectDir string `json:"project_dir"`
}

// Engine dispatches lifecycle hook commands and records audit entries.
type Engine struct {
	cfg   Config
	store *Store
}

// NewEngine creates an Engine from the provided configuration. If cfg is nil or
// has no hooks, the engine is a no-op.
func NewEngine(cfg *Config, store *Store) *Engine {
	if cfg == nil {
		cfg = &Config{}
	}
	if store == nil {
		store = &Store{}
	}
	return &Engine{cfg: *cfg, store: store}
}

// Store returns the underlying audit store for testing and inspection.
func (e *Engine) Store() *Store { return e.store }

// FireBeforeTool executes all matching beforeTool hooks and returns an error if
// any hook vetoes the tool execution. Matching hooks run serially in config
// order and fold most-restrictively: a goa-native hook vetoes on any non-zero
// exit; a Claude Code hook vetoes on exit 2, on a permissionDecision of deny /
// ask / defer, or on a top-level decision of block. The returned error carries
// every vetoing hook's reason plus the accumulated additionalContext so the
// caller can surface it to the model.
func (e *Engine) FireBeforeTool(ctx context.Context, payload ToolPayload) error {
	return e.fireAll(ctx, EventBeforeTool, payload)
}

// FireAfterTool executes all matching afterTool hooks. afterTool hooks never
// veto (the tool has already run); outcomes are recorded in the audit store.
func (e *Engine) FireAfterTool(ctx context.Context, payload ToolPayload) error {
	return e.fireAll(ctx, EventAfterTool, payload)
}

// FireSessionStart executes all matching sessionStart hooks.
func (e *Engine) FireSessionStart(ctx context.Context, payload SessionPayload) error {
	return e.fireAll(ctx, EventSessionStart, payload)
}

// FireSessionEnd executes all matching sessionEnd hooks.
func (e *Engine) FireSessionEnd(ctx context.Context, payload SessionPayload) error {
	return e.fireAll(ctx, EventSessionEnd, payload)
}

// hookOutcome is one hook execution's decoded result, folded into the merged
// outcome of a point.
type hookOutcome struct {
	command   string
	vetoed    bool
	reason    string
	context   string
	systemMsg string
}

// mergedOutcome is the most-restrictive fold of every matching hook (gap TL4:
// deny/block > ask/defer > allow; additionalContext and systemMessage
// accumulate in hook order, mirroring dsh-hook-protocol's mergeHookOutputs).
type mergedOutcome struct {
	vetoed  bool
	reasons []string
	context []string
	system  []string
}

func (m *mergedOutcome) fold(h Hook, o hookOutcome) {
	if o.context != "" {
		m.context = append(m.context, o.context)
	}
	if o.systemMsg != "" {
		m.system = append(m.system, o.systemMsg)
	}
	if !o.vetoed {
		return
	}
	m.vetoed = true
	reason := o.reason
	if reason == "" {
		reason = "hook denied the action"
	}
	m.reasons = append(m.reasons, fmt.Sprintf("beforeTool hook %q vetoed: %s", o.command, reason))
}

// vetoError renders the veto so the model sees the reasons and any
// additionalContext (acceptance: "its additionalContext reaches the model").
func (m mergedOutcome) vetoError() error {
	var b strings.Builder
	for i, r := range m.reasons {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(r)
	}
	if len(m.context) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("Hook additional context:\n")
		b.WriteString(strings.Join(m.context, "\n"))
	}
	if b.Len() == 0 {
		b.WriteString("beforeTool hook vetoed")
	}
	return fmt.Errorf("%s", b.String())
}

func (e *Engine) fireAll(ctx context.Context, event Event, payload any) error {
	subject := matchSubject(event, payload)
	var merged mergedOutcome
	for _, h := range e.cfg.Hooks {
		if h.Event != event {
			continue
		}
		if h.Dialect == DialectClaudeCode && h.Matcher != "" && !matchesClaudeMatcher(h.Matcher, subject) {
			continue
		}
		o := e.fire(ctx, h, payload)
		merged.fold(h, o)
	}
	if event == EventBeforeTool && merged.vetoed {
		return merged.vetoError()
	}
	return nil
}

// matchSubject returns the matcher subject for a payload: the tool name for
// tool events, empty otherwise (CC session matchers are discarded at parse).
func matchSubject(event Event, payload any) string {
	if event != EventBeforeTool && event != EventAfterTool {
		return ""
	}
	tp, ok := payload.(ToolPayload)
	if !ok {
		return ""
	}
	return tp.ToolName
}

func (e *Engine) fire(ctx context.Context, h Hook, payload any) hookOutcome {
	payloadJSON, err := json.Marshal(dialectPayload(h, payload))
	if err != nil {
		// Cannot marshal our own structs in practice; fail closed for
		// beforeTool regardless of dialect.
		return hookOutcome{command: h.Command, vetoed: h.Event == EventBeforeTool, reason: fmt.Sprintf("marshal payload: %v", err)}
	}

	entry := Entry{
		Event:     h.Event,
		Command:   h.Command,
		Args:      h.Args,
		Payload:   string(payloadJSON),
		Dialect:   h.Dialect,
		StartedAt: time.Now(),
	}
	defer func() {
		entry.FinishedAt = time.Now()
		_ = e.store.Record(entry)
	}()

	runCtx, cancel := hookTimeout(ctx, h)
	if cancel != nil {
		defer cancel()
	}

	var stdout, stderr bytes.Buffer
	cmd := buildCmd(runCtx, h)
	cmd.Stdin = bytes.NewReader(payloadJSON)
	if h.Dialect == DialectClaudeCode {
		// CC sends the payload as a single line with a trailing newline.
		cmd.Stdin = bytes.NewReader(append(append([]byte{}, payloadJSON...), '\n'))
	}
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	entry.ExitCode = exitCode(cmd, runErr)
	outText := stdout.String()
	errText := stderr.String()
	entry.Output = outText + errText
	if runErr != nil && entry.Output == "" {
		// Record the launch failure reason (e.g. binary not found) so the audit
		// entry is not silently empty with a success-looking exit code.
		entry.Output = runErr.Error()
	}

	if h.Dialect == DialectClaudeCode {
		dec := decodeClaudeOutcome(entry.ExitCode, outText, errText)
		return hookOutcome{
			command:   h.Command,
			vetoed:    dec.veto,
			reason:    dec.reason,
			context:   dec.context,
			systemMsg: dec.system,
		}
	}
	return hookOutcome{
		command: h.Command,
		vetoed:  runErr != nil && h.Event == EventBeforeTool,
		reason:  entry.Output,
	}
}

// hookTimeout applies a per-hook timeout. Claude Code hooks default to the CC
// reference 600s when they set none; goa-native hooks run without a timeout
// unless one is configured.
func hookTimeout(ctx context.Context, h Hook) (context.Context, context.CancelFunc) {
	timeout := time.Duration(h.TimeoutSeconds) * time.Second
	if h.Dialect == DialectClaudeCode && timeout <= 0 {
		timeout = 600 * time.Second
	}
	if timeout <= 0 {
		return ctx, nil
	}
	return context.WithTimeout(ctx, timeout)
}

// buildCmd constructs the hook process: goa-native hooks exec Command+Args
// directly; CC hooks use exec form when Args is present and shell form
// (sh -c) otherwise, run in WorkDir with CLAUDE_PROJECT_DIR exported.
func buildCmd(ctx context.Context, h Hook) *exec.Cmd {
	var cmd *exec.Cmd
	switch {
	case h.Dialect == DialectClaudeCode && len(h.Args) > 0:
		cmd = exec.CommandContext(ctx, h.Command, h.Args...)
	case h.Dialect == DialectClaudeCode:
		cmd = exec.CommandContext(ctx, "sh", "-c", h.Command)
	default:
		cmd = exec.CommandContext(ctx, h.Command, append([]string{}, h.Args...)...)
	}
	if h.WorkDir != "" {
		cmd.Dir = h.WorkDir
		if h.Dialect == DialectClaudeCode {
			cmd.Env = append(os.Environ(), "CLAUDE_PROJECT_DIR="+h.WorkDir)
		}
	}
	return cmd
}

// dialectPayload renders a payload in the hook's wire dialect.
func dialectPayload(h Hook, payload any) any {
	if h.Dialect != DialectClaudeCode {
		return payload
	}
	switch h.Event {
	case EventBeforeTool, EventAfterTool:
		tp := payload.(ToolPayload)
		return claudeToolPayload{
			SessionID:      tp.SessionID,
			TranscriptPath: "",
			CWD:            tp.CWD,
			HookEventName:  ccEventName(h.Event),
			ToolName:       tp.ToolName,
			ToolInput:      parseToolInput(tp.ToolInput),
			ToolUseID:      tp.CallID,
			ToolResponse:   tp.Output,
		}
	case EventSessionStart, EventSessionEnd:
		sp := payload.(SessionPayload)
		return claudeSessionPayload{
			SessionID:      sp.SessionID,
			TranscriptPath: "",
			CWD:            sp.ProjectDir,
			HookEventName:  ccEventName(h.Event),
		}
	}
	return payload
}

func ccEventName(event Event) string {
	switch event {
	case EventBeforeTool:
		return "PreToolUse"
	case EventAfterTool:
		return "PostToolUse"
	case EventSessionStart:
		return "SessionStart"
	case EventSessionEnd:
		return "SessionEnd"
	}
	return string(event)
}

// claudeToolPayload is the CC stdin payload for PreToolUse / PostToolUse hooks.
type claudeToolPayload struct {
	SessionID      string         `json:"session_id"`
	TranscriptPath string         `json:"transcript_path"`
	CWD            string         `json:"cwd"`
	HookEventName  string         `json:"hook_event_name"`
	ToolName       string         `json:"tool_name"`
	ToolInput      map[string]any `json:"tool_input"`
	ToolUseID      string         `json:"tool_use_id,omitempty"`
	ToolResponse   string         `json:"tool_response,omitempty"`
}

// claudeSessionPayload is the CC stdin payload for SessionStart / SessionEnd
// hooks. goa's session lifecycle carries no source or reason, so those fields
// are omitted.
type claudeSessionPayload struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	CWD            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
}

// parseToolInput converts goa's JSON-string tool arguments into the CC
// tool_input object. Non-JSON input is wrapped under "input".
func parseToolInput(input string) map[string]any {
	var m map[string]any
	if err := json.Unmarshal([]byte(input), &m); err == nil && m != nil {
		return m
	}
	return map[string]any{"input": input}
}

// ---------------------------------------------------------------------------
// Claude Code matcher (claude mode: literal alternatives or unanchored regex).
// ---------------------------------------------------------------------------

var claudeLiteralRe = regexp.MustCompile(`^[A-Za-z0-9_\- ,|]+$`)

// claudeMatcherIsLiteral reports whether a matcher is an exact alternative list
// (letters/digits/underscore/hyphen/space/comma/pipe) rather than a regexp,
// matching Claude Code's matcher rules.
func claudeMatcherIsLiteral(pattern string) bool {
	return claudeLiteralRe.MatchString(pattern)
}

// matchesClaudeMatcher implements Claude Code's matcher semantics. A pattern
// of "*", "" or the absent sentinel matches everything. A literal pattern is
// split on "|" and "," (trimmed) and matched case-insensitively, because goa
// tool names are lowercase while CC tool names are Title-case (Bash vs bash).
// Any other pattern is treated as an unanchored Go regexp (RE2 subset of the
// JavaScript regexp CC uses); an invalid regexp never matches.
func matchesClaudeMatcher(pattern, query string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	if claudeMatcherIsLiteral(pattern) {
		for _, alt := range strings.FieldsFunc(pattern, func(r rune) bool { return r == '|' || r == ',' }) {
			if strings.EqualFold(strings.TrimSpace(alt), query) {
				return true
			}
		}
		return false
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false
	}
	return re.MatchString(query)
}

// ---------------------------------------------------------------------------
// Claude Code output codec (exit code + stdout JSON + stderr).
// ---------------------------------------------------------------------------

// claudeContextCap mirrors Claude Code's 10,000-character cap on output
// strings such as additionalContext.
const claudeContextCap = 10000

// claudeOutcome is the decoded result of one CC hook process.
type claudeOutcome struct {
	veto    bool
	reason  string
	context string
	system  string
}

// decodeClaudeOutcome applies Claude Code's exit-code/JSON output contract:
//
//   - stdout JSON is read on every exit code when it starts with '{' (malformed
//     JSON is treated as plain text);
//   - exit 2 blocks and JSON cannot override it; the blocking reason is the
//     JSON blocking decision's reason, or stderr otherwise;
//   - otherwise a valid JSON object alone decides: hookSpecificOutput.
//     permissionDecision allow/deny/ask/defer for PreToolUse, and the legacy
//     top-level decision approve/block anywhere (deny/ask/defer/block all fold
//     to a veto for goa's beforeTool; ask/defer are treated as blocking because
//     goa has no hook-driven confirmation or deferral surface);
//   - a non-zero exit without valid JSON is a non-blocking error.
func decodeClaudeOutcome(exitCode int, stdout, stderr string) claudeOutcome {
	o := claudeOutcome{}
	trimmedOut := strings.TrimSpace(stdout)
	trimmedErr := strings.TrimSpace(stderr)

	var parsed map[string]any
	if strings.HasPrefix(trimmedOut, "{") {
		_ = json.Unmarshal([]byte(trimmedOut), &parsed) // malformed JSON = plain text
	}
	if parsed != nil {
		o.applyStructured(parsed)
	}

	// Exit 2 blocks regardless of JSON; the blocking reason is the JSON
	// blocking reason or stderr.
	if exitCode == 2 {
		o.veto = true
		if o.reason == "" && trimmedErr != "" {
			o.reason = trimmedErr
		}
	}
	if o.veto && o.reason == "" {
		o.reason = "hook denied the tool call"
	}
	return o
}

// applyStructured folds a parsed CC stdout object into the outcome: universal
// fields, hookSpecificOutput (permissionDecision + additionalContext), and the
// legacy top-level decision (approve/block). CC reserves allow/deny/ask for
// permissionDecision; "block" is accepted defensively as a veto.
func (o *claudeOutcome) applyStructured(parsed map[string]any) {
	if s, ok := parsed["systemMessage"].(string); ok && s != "" {
		o.system = s
	}
	if hso, ok := parsed["hookSpecificOutput"].(map[string]any); ok {
		o.applyHookSpecific(hso)
	}
	if s, ok := parsed["decision"].(string); ok {
		if s == "block" {
			o.veto = true
		}
		if o.reason == "" {
			if r, ok := parsed["reason"].(string); ok && r != "" {
				o.reason = r
			}
		}
	}
}

// applyHookSpecific folds hookSpecificOutput: permissionDecision
// allow/deny/ask/defer (deny/ask/defer veto; ask/defer block because goa has
// no hook-driven confirmation or deferral surface) and additionalContext.
func (o *claudeOutcome) applyHookSpecific(hso map[string]any) {
	if s, ok := hso["additionalContext"].(string); ok && s != "" {
		o.context = capClaudeContext(s)
	}
	if s, ok := hso["permissionDecision"].(string); ok && s != "" {
		switch s {
		case "deny", "ask", "defer":
			o.veto = true
		case "allow":
			// Explicitly allowed.
		}
		if r, ok := hso["permissionDecisionReason"].(string); ok && r != "" {
			o.reason = r
		}
	}
}

// capClaudeContext truncates additionalContext at Claude Code's 10,000
// character cap (rune-safe), appending an elision notice.
func capClaudeContext(s string) string {
	if len(s) <= claudeContextCap {
		return s
	}
	runes := []rune(s)
	if len(runes) <= claudeContextCap {
		return s
	}
	return string(runes[:claudeContextCap]) + "\n[goa-system] additionalContext truncated at Claude Code's 10,000 character cap"
}

func exitCode(cmd *exec.Cmd, err error) int {
	if cmd != nil && cmd.ProcessState != nil {
		return cmd.ProcessState.ExitCode()
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	if err != nil {
		// Failed to start (e.g. binary not found) or another non-exit error:
		// surface a distinct non-zero code instead of masking as success (0).
		return -1
	}
	return 0
}

// Ensure Engine satisfies the interface expected by the agent.
var _ AgentHookEngine = (*Engine)(nil)

// AgentHookEngine is the subset of Engine used by the agentic package. It
// avoids an import cycle and keeps the agent decoupled from hook internals.
type AgentHookEngine interface {
	FireBeforeTool(ctx context.Context, payload ToolPayload) error
	FireAfterTool(ctx context.Context, payload ToolPayload) error
	FireSessionStart(ctx context.Context, payload SessionPayload) error
	FireSessionEnd(ctx context.Context, payload SessionPayload) error
}
