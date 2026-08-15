// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/ansi"
)

// TerminalsTool is the unified terminal suite: one schema with the six
// persistent-terminal actions open/close/list/read/send/signal and a
// persistent-shell mode for bash. It replaces the former PTYExecTool
// (session management) and TerminalTool (one-shot sandbox) with a single
// service, preserving the terminal safety allow-list on every send.
type TerminalsTool struct {
	Mgr *internal.PTYManager
	// Blocked/Allowed/Bypass carry the terminal sandbox allow-list. When
	// Bypass is false, every send is checked against Blocked and Allowed
	// before it reaches the shell. Bypass is an explicit user opt-out
	// (tools.terminal.sandbox.enabled=false).
	Blocked []string
	Allowed []string
	Bypass  bool
	// ProjectDir is the default cwd for newly opened shells.
	ProjectDir string
}

// terminalsParams holds the parsed input for TerminalsTool. One schema covers
// all six actions; only the fields relevant to the chosen action are used.
type terminalsParams struct {
	Action          string `json:"action"`
	SessionID       string `json:"sessionId"`
	Type            string `json:"type"`
	Name            string `json:"name"`
	Cwd             string `json:"cwd"`
	Offset          int    `json:"offset"`
	Count           int    `json:"count"`
	Text            string `json:"text"`
	Submit          *bool  `json:"submit"`
	RunInBackground bool   `json:"run_in_background"`
	Signal          string `json:"signal"`
	Timeout         int    `json:"timeout"`
	Cols            int    `json:"cols"`
	Rows            int    `json:"rows"`
}

// Schema returns the single tool schema for the terminals service.
func (t *TerminalsTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "terminals",
		Description: "Manage persistent terminal sessions: open/close/list/read/send/signal. Open a persistent shell, send commands to it, and read its output across tool calls.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"open", "close", "list", "read", "send", "signal"},
					"description": "Terminal action to perform.",
				},
				"sessionId": map[string]any{"type": "string", "description": "Terminal session id returned by open or list."},
				"type":      map[string]any{"type": "string", "description": "Terminal backend type, usually \"shell\"."},
				"name":      map[string]any{"type": "string", "description": "Optional owner-local display name such as \"main\" or \"gdb\"; becomes the session id."},
				"cwd":       map[string]any{"type": "string", "description": "Initial working directory. Defaults to the project workspace root."},
				"offset":    map[string]any{"type": "integer", "description": "Newest-relative line offset for read (default 0)."},
				"count":     map[string]any{"type": "integer", "description": "Requested line count for read (default 500; backend caps apply)."},
				"text":      map[string]any{"type": "string", "description": "UTF-8 text to write to the terminal."},
				"submit": map[string]any{
					"type":        "boolean",
					"description": "Submit Enter after text (default true). Set false for control characters or incomplete REPL input.",
				},
				"run_in_background": map[string]any{
					"type":        "boolean",
					"description": "Return a job id immediately instead of waiting for output (default false). Collect output later with read.",
				},
				"signal": map[string]any{
					"type":        "string",
					"enum":        []string{"SIGINT", "SIGTERM", "SIGKILL", "SIGTSTP", "SIGHUP"},
					"description": "Signal to deliver to the session's foreground process group. Shell-targeted SIGKILL is rejected; use close.",
				},
				"timeout": map[string]any{"type": "integer", "description": "Read/send wait timeout in seconds (default: 5)."},
				"cols":    map[string]any{"type": "integer", "description": "PTY width in columns for open (default: 80)."},
				"rows":    map[string]any{"type": "integer", "description": "PTY height in rows for open (default: 24)."},
			},
			"required": []string{"action"},
		},
	}
}

// Execute runs the requested terminals action.
func (t *TerminalsTool) Execute(input string) (string, error) {
	var p terminalsParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		return "", tErr("invalid_input", fmt.Sprintf("Cannot parse parameters: %v", err))
	}

	if t.Mgr == nil {
		return "", tErr("no_manager", "PTY manager not initialized")
	}

	// A raw (healed) argument like "ls -la" arrives as {"text":"ls -la"}
	// without an action; default to send so the model's sloppy output still
	// lands in the shell.
	if p.Action == "" {
		if p.Text != "" {
			p.Action = "send"
		} else {
			return "", tErr("missing_action", "Action is required (open, close, list, read, send, signal)")
		}
	}

	switch p.Action {
	case "open":
		return t.open(p)
	case "close":
		return t.close(p)
	case "list":
		return t.list()
	case "read":
		return t.read(p)
	case "send":
		return t.send(p)
	case "signal":
		return t.signal(p)
	default:
		return "", tErr("invalid_action", fmt.Sprintf("Unknown action: %s", p.Action))
	}
}

// open creates a persistent terminal session. Only the "shell" backend is
// supported; it spawns an interactive bash that survives across tool calls.
func (t *TerminalsTool) open(p terminalsParams) (string, error) {
	if p.Type == "" {
		p.Type = "shell"
	}
	if p.Type != "shell" {
		return "", tErr("unknown_backend", fmt.Sprintf("Unknown terminal backend type: %q (only \"shell\" is supported)", p.Type))
	}

	id := p.Name
	if id == "" {
		id = fmt.Sprintf("term-%d", time.Now().UnixNano())
	}
	if p.Cols <= 0 {
		p.Cols = 80
	}
	if p.Rows <= 0 {
		p.Rows = 24
	}
	cwd := p.Cwd
	if cwd == "" {
		cwd = t.ProjectDir
	}

	session, err := t.Mgr.OpenShell(id, cwd, p.Cols, p.Rows)
	if err != nil {
		return "", tErr("open_failed", err.Error())
	}

	return fmt.Sprintf("[terminals: opened]\nID: %s\nType: %s\nCwd: %s\nPID: %d\n",
		session.ID, p.Type, cwd, session.Cmd.Process.Pid), nil
}

// close terminates a session and removes it from the manager.
func (t *TerminalsTool) close(p terminalsParams) (string, error) {
	if p.SessionID == "" {
		return "", tErr("missing_session", "Session ID is required for close action")
	}
	if err := t.Mgr.Stop(p.SessionID); err != nil {
		return "", tErr("close_failed", err.Error())
	}
	return fmt.Sprintf("[terminals: closed %s]\n", p.SessionID), nil
}

// list reports all active terminal sessions.
func (t *TerminalsTool) list() (string, error) {
	sessions := t.Mgr.List()
	if len(sessions) == 0 {
		return "[terminals: list]\nNo active sessions.\n", nil
	}
	var b strings.Builder
	b.WriteString("[terminals: list]\n")
	for _, s := range sessions {
		status := "running"
		if !s.Running {
			status = "stopped"
		}
		b.WriteString(fmt.Sprintf("  %s  PID %d  %s  (%s)\n", s.ID, s.PID, s.Command, status))
	}
	return b.String(), nil
}

// read returns a bounded page of retained output without sending input.
func (t *TerminalsTool) read(p terminalsParams) (string, error) {
	if p.SessionID == "" {
		return "", tErr("missing_session", "Session ID is required for read action")
	}
	if p.Count <= 0 {
		p.Count = 500
	}
	output, err := t.Mgr.ReadRange(p.SessionID, p.Offset, p.Count)
	if err != nil {
		return "", tErr("read_failed", err.Error())
	}
	if output == "" {
		return fmt.Sprintf("[terminals: read %s]\n(no output available)\n", p.SessionID), nil
	}
	clean := sanitizeTerminalOutput(output)
	return fmt.Sprintf("[terminals: read %s]\n%s\n", p.SessionID, clean), nil
}

// send writes text to a session. By default Enter is submitted and the call
// waits (up to timeout) for output, session exit, or silence. Background mode
// returns a job id immediately; output is collected later with read.
func (t *TerminalsTool) send(p terminalsParams) (string, error) {
	if p.SessionID == "" {
		sessions := t.Mgr.List()
		if len(sessions) == 1 {
			p.SessionID = sessions[0].ID
		} else {
			return "", tErr("missing_session", "Session ID is required for send action (or open a session first)")
		}
	}
	if p.Text == "" {
		return "", tErr("missing_text", "Text is required for send action")
	}

	if !t.Bypass {
		if err := t.checkSafety(p.Text); err != nil {
			return "", err
		}
	}

	payload := p.Text
	if p.Submit == nil || *p.Submit {
		payload += "\r"
	}

	if p.RunInBackground {
		if err := t.Mgr.Write(p.SessionID, payload); err != nil {
			return "", tErr("send_failed", err.Error())
		}
		jobID := fmt.Sprintf("termjob-%s-%d", p.SessionID, time.Now().UnixNano())
		return fmt.Sprintf("[terminals: sent %d bytes to %s in background]\nJob ID: %s\n", len(payload), p.SessionID, jobID), nil
	}

	timeout := t.waitTimeout(p.Timeout)
	output, err := t.Mgr.Send(p.SessionID, payload, timeout)
	if err != nil {
		return "", tErr("send_failed", err.Error())
	}
	if output == "" {
		return fmt.Sprintf("[terminals: sent %d bytes to %s]\n(no output yet)\n", len(payload), p.SessionID), nil
	}
	clean := sanitizeTerminalOutput(output)
	return fmt.Sprintf("[terminals: sent %d bytes to %s]\n%s\n", len(payload), p.SessionID, clean), nil
}

// signal delivers an allowed signal to the session's foreground process
// group. Shell-targeted SIGKILL is rejected (use close).
func (t *TerminalsTool) signal(p terminalsParams) (string, error) {
	if p.SessionID == "" {
		return "", tErr("missing_session", "Session ID is required for signal action")
	}
	if p.Signal == "" {
		return "", tErr("missing_signal", "Signal is required (SIGINT, SIGTERM, SIGKILL, SIGTSTP, SIGHUP)")
	}
	if p.Signal == "SIGKILL" {
		return "", tErr("sigkill_rejected", "Shell-targeted SIGKILL is rejected; use the close action to terminate the session")
	}
	sig, ok := terminalSignalValues[p.Signal]
	if !ok {
		return "", tErr("invalid_signal", fmt.Sprintf("Unknown signal: %s", p.Signal))
	}
	if err := t.Mgr.Signal(p.SessionID, sig); err != nil {
		return "", tErr("signal_failed", err.Error())
	}
	return fmt.Sprintf("[terminals: signaled %s with %s]\n", p.SessionID, p.Signal), nil
}

// checkSafety applies the terminal allow-list to text about to be sent to a
// shell: blocked commands at command position are rejected, and when an
// allowed list is configured the leading command must be on it.
func (t *TerminalsTool) checkSafety(text string) error {
	blocked := findBlockedCommands(text, t.Blocked)
	if len(blocked) > 0 {
		return tErr("blocked_command",
			fmt.Sprintf("Blocked command(s) for safety: %s", strings.Join(blocked, ", ")))
	}
	return t.checkAllowed(text)
}

func (t *TerminalsTool) checkAllowed(text string) error {
	if len(t.Allowed) == 0 {
		return nil
	}
	program := firstCommandToken(text)
	for _, allowed := range t.Allowed {
		if strings.EqualFold(program, allowed) {
			return nil
		}
	}
	return &internal.ToolError{
		Tool:     "terminals",
		Type:     "command_not_allowed",
		Detail:   fmt.Sprintf("Command %q is not in the allowed list", program),
		HintText: "Use one of the allowed commands or update tools.terminal.sandbox.allowed_commands in config.",
	}
}

func (t *TerminalsTool) waitTimeout(override int) time.Duration {
	if override > 0 {
		return time.Duration(override) * time.Second
	}
	return 5 * time.Second
}

// sanitizeTerminalOutput turns a raw PTY stream into displayable text: strip
// ANSI, resolve carriage-return rewrites, then sanitize residual control
// bytes (backspace, bell, ...) into visible '?'. The same pipeline guarded
// the old pty_exec read path.
func sanitizeTerminalOutput(output string) string {
	// Truncate very long output. Cut from the end on a rune boundary so the
	// result is always valid UTF-8 (a raw byte cut can split a multi-byte
	// rune and render as '�').
	if len(output) > 50000 {
		start := len(output) - 50000
		for start < len(output) && output[start]&0xC0 == 0x80 {
			start++ // skip UTF-8 continuation bytes
		}
		output = "... [truncated to last 50000 bytes]\n" + output[start:]
	}
	// Resolve PTY carriage-return semantics, then sanitize. Raw PTY streams
	// are full of '\r' (termios ONLCR even doubles them: "alpha\r\r\n"); a
	// bare '\r' reaching the TUI renderer moves the cursor to column 0 so
	// later text (and the tool box's background padding) overwrites the line
	// start — the "garbage pty read" rendering bug. Strip runs first so the
	// overwrite width is counted on plain text (escape-sequence bytes would
	// otherwise count as columns and leave half-overwritten CSI text behind);
	// normalizePTYOutput then resolves progress-style rewrites; Sanitize
	// turns any residual control bytes (backspace, bell, ...) into visible
	// '?' instead of terminal-corrupting output, matching bash/python/verify.
	return ansi.Sanitize(normalizePTYOutput(ansi.Strip(output)))
}

// normalizePTYOutput converts a raw PTY output stream into displayable text.
//   - "\r\n" (and doubled "\r\r\n" from termios ONLCR) collapses to "\n".
//   - A bare '\r' is a carriage return: the following text overwrites the
//     current line from column 0 (progress bars, spinners). The final
//     visible line state is kept — overwritten-prefix remnants are dropped.
func normalizePTYOutput(s string) string {
	if !strings.ContainsRune(s, '\r') {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if !strings.ContainsRune(line, '\r') {
			continue
		}
		segments := strings.Split(line, "\r")
		kept := ""
		for _, seg := range segments {
			kept = carriageReturnOverwrite(kept, seg)
		}
		lines[i] = kept
	}
	return strings.Join(lines, "\n")
}

// carriageReturnOverwrite applies one segment written after a carriage
// return: it overwrites 'cur' from column 0, leaving any longer tail
// untouched. Width is counted in runes (a display approximation — the
// overwrite target is plain text, escape sequences having been stripped).
func carriageReturnOverwrite(cur, seg string) string {
	if seg == "" {
		return cur
	}
	segRunes := []rune(seg)
	curRunes := []rune(cur)
	if len(segRunes) >= len(curRunes) {
		return seg
	}
	return seg + string(curRunes[len(segRunes):])
}

// IsRetryable returns false — terminal operations are deterministic.
func (t *TerminalsTool) IsRetryable(err error) bool { return false }

// Access returns shell category for all terminal executions.
func (t *TerminalsTool) Access(input string) ToolAccess {
	return ToolAccess{Category: "shell"}
}

// MutatesState reports that a successful send may change shared state.
func (t *TerminalsTool) MutatesState() bool { return true }

// LoopHints heals raw arguments into {"text": ...} (which defaults to the
// send action) and shows the sent text as the running status.
func (t *TerminalsTool) LoopHints() agentic.ToolLoopHints {
	return agentic.ToolLoopHints{HealArg: "text", Status: terminalsRunStatus}
}

// terminalsRunStatus is the TUI status line for an in-flight terminals call.
func terminalsRunStatus(arguments string) string {
	var p terminalsParams
	if err := json.Unmarshal([]byte(arguments), &p); err != nil {
		return ""
	}
	switch p.Action {
	case "send":
		text := p.Text
		if len(text) > 60 {
			text = text[:57] + "..."
		}
		if text == "" {
			return "Sending to terminal..."
		}
		return fmt.Sprintf("Running: %s", text)
	case "open":
		return "Opening terminal..."
	case "close":
		return fmt.Sprintf("Closing terminal %s", p.SessionID)
	case "signal":
		return fmt.Sprintf("Signaling terminal: %s", p.Signal)
	case "read":
		return fmt.Sprintf("Reading terminal %s", p.SessionID)
	default:
		return ""
	}
}

// tErr builds a ToolError for the terminals tool.
func tErr(typ, detail string) *internal.ToolError {
	return &internal.ToolError{
		Tool: "terminals", Type: typ, Detail: detail,
		HintText: "Use action=list to see active sessions, action=open to create one, or action=close to end one.",
	}
}

// Compile-time assertions: TerminalsTool satisfies the optional tool
// interfaces it implements (documentation, resource access, state mutation,
// loop hints).
var (
	_ Documentable          = (*TerminalsTool)(nil)
	_ Accessor              = (*TerminalsTool)(nil)
	_ agentic.StateMutator  = (*TerminalsTool)(nil)
	_ agentic.LoopAnnotated = (*TerminalsTool)(nil)
)

//go:embed terminals.short.md terminals.long.md
var terminalsDocs embed.FS

func (t *TerminalsTool) ShortDoc() string { return readDoc(terminalsDocs, "terminals.short.md") }
func (t *TerminalsTool) LongDoc() string  { return readDoc(terminalsDocs, "terminals.long.md") }

func (t *TerminalsTool) Examples() []string {
	return []string{
		`{"action": "open", "type": "shell", "name": "main"}`,
		`{"action": "send", "sessionId": "main", "text": "ls -la"}`,
		`{"action": "read", "sessionId": "main"}`,
		`{"action": "signal", "sessionId": "main", "signal": "SIGINT"}`,
		`{"action": "close", "sessionId": "main"}`,
	}
}
