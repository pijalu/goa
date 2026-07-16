// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/ansi"
)

// PTYExecTool allows the agent to execute commands in pseudo-terminals,
// supporting TTY-dependent applications (vim, htop, less, etc.).
type PTYExecTool struct {
	Mgr *internal.PTYManager
}

// ptyParams holds the parsed input for PTYExecTool.
type ptyParams struct {
	ID      string `json:"id"`
	Command string `json:"command"`
	Action  string `json:"action"`
	Input   string `json:"input"`
	Cols    int    `json:"cols"`
	Rows    int    `json:"rows"`
	Timeout int    `json:"timeout"`
	Tail    int    `json:"tail"`
}

// Schema returns the tool schema for pty_exec.
func (t *PTYExecTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "pty_exec",
		Description: "Run a command in a pseudo-terminal.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":      map[string]any{"type": "string", "description": "session ID (auto-generated if empty)"},
				"command": map[string]any{"type": "string", "description": "command to execute in the PTY"},
				"action":  map[string]any{"type": "string", "description": "start|write|read|resize|stop|list", "enum": []string{"start", "write", "read", "resize", "stop", "list"}},
				"input":   map[string]any{"type": "string", "description": "text to write to PTY stdin"},
				"cols":    map[string]any{"type": "integer", "description": "PTY width in columns (default: 80)"},
				"rows":    map[string]any{"type": "integer", "description": "PTY height in rows (default: 24)"},
				"timeout": map[string]any{"type": "integer", "description": "read timeout in seconds (default: 5)"},
				"tail":    map[string]any{"type": "integer", "description": "lines to read from tail (default: all)"},
			},
			"required": []string{"command", "action"},
		},
	}
}

// Execute runs the requested PTY action.
func (t *PTYExecTool) Execute(input string) (string, error) {
	var p ptyParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		return "", ptyErr("invalid_input", fmt.Sprintf("Cannot parse parameters: %v", err))
	}

	if t.Mgr == nil {
		return "", ptyErr("no_manager", "PTY manager not initialized")
	}

	switch p.Action {
	case "start":
		return t.ptyStart(p)
	case "write":
		return t.ptyWrite(p)
	case "read":
		return t.ptyRead(p)
	case "resize":
		return t.ptyResize(p)
	case "stop":
		return t.ptyStop(p)
	case "list":
		return t.ptyList()
	default:
		return "", ptyErr("invalid_action", fmt.Sprintf("Unknown action: %s", p.Action))
	}
}

func (t *PTYExecTool) ptyStart(p ptyParams) (string, error) {
	if p.Command == "" {
		return "", ptyErr("missing_command", "Command is required for start action")
	}
	id := p.ID
	if id == "" {
		id = fmt.Sprintf("pty-%d", time.Now().UnixNano())
	}
	if p.Cols <= 0 {
		p.Cols = 80
	}
	if p.Rows <= 0 {
		p.Rows = 24
	}

	session, err := t.Mgr.Start(id, p.Command, p.Cols, p.Rows)
	if err != nil {
		return "", ptyErr("start_failed", err.Error())
	}

	return fmt.Sprintf("[pty_exec: started]\nID: %s\nCommand: %s\nPID: %d\n",
		session.ID, session.Command, session.Cmd.Process.Pid), nil
}

func (t *PTYExecTool) ptyWrite(p ptyParams) (string, error) {
	if p.ID == "" {
		return "", ptyErr("missing_id", "Session ID is required for write action")
	}
	if p.Input == "" {
		return "", ptyErr("missing_input", "Input text is required for write action")
	}
	if err := t.Mgr.Write(p.ID, p.Input); err != nil {
		return "", ptyErr("write_failed", err.Error())
	}
	return fmt.Sprintf("[pty_exec: wrote %d bytes to %s]\n", len(p.Input), p.ID), nil
}

func (t *PTYExecTool) ptyRead(p ptyParams) (string, error) {
	if p.ID == "" {
		return "", ptyErr("missing_id", "Session ID is required for read action")
	}
	var output string
	var err error
	if p.Timeout > 0 {
		output, err = t.Mgr.ReadBlocking(p.ID, time.Duration(p.Timeout)*time.Second)
	} else {
		output, err = t.Mgr.Read(p.ID, p.Tail)
	}
	if err != nil {
		return "", ptyErr("read_failed", err.Error())
	}
	if output == "" {
		return fmt.Sprintf("[pty_exec: read %s]\n(no output available)\n", p.ID), nil
	}
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
	// Strip ANSI for clean output (preserve newlines). Use the shared
	// internal/ansi stripper so CSI and OSC sequences (e.g. window-title sets)
	// are handled correctly.
	clean := ansi.Strip(output)
	return fmt.Sprintf("[pty_exec: read %s]\n%s\n", p.ID, clean), nil
}

func (t *PTYExecTool) ptyResize(p ptyParams) (string, error) {
	if p.ID == "" {
		return "", ptyErr("missing_id", "Session ID is required for resize action")
	}
	if p.Cols <= 0 {
		p.Cols = 80
	}
	if p.Rows <= 0 {
		p.Rows = 24
	}
	if err := t.Mgr.Resize(p.ID, p.Cols, p.Rows); err != nil {
		return "", ptyErr("resize_failed", err.Error())
	}
	return fmt.Sprintf("[pty_exec: resized %s to %dx%d]\n", p.ID, p.Cols, p.Rows), nil
}

func (t *PTYExecTool) ptyStop(p ptyParams) (string, error) {
	if p.ID == "" {
		return "", ptyErr("missing_id", "Session ID is required for stop action")
	}
	if err := t.Mgr.Stop(p.ID); err != nil {
		return "", ptyErr("stop_failed", err.Error())
	}
	return fmt.Sprintf("[pty_exec: stopped %s]\n", p.ID), nil
}

func (t *PTYExecTool) ptyList() (string, error) {
	sessions := t.Mgr.List()
	if len(sessions) == 0 {
		return "[pty_exec: list]\nNo active sessions.\n", nil
	}
	var b strings.Builder
	b.WriteString("[pty_exec: list]\n")
	for _, s := range sessions {
		status := "running"
		if !s.Running {
			status = "stopped"
		}
		b.WriteString(fmt.Sprintf("  %s  PID %d  %s  (%s)\n", s.ID, s.PID, s.Command, status))
	}
	return b.String(), nil
}

// IsRetryable returns false — PTY operations are deterministic.
func (t *PTYExecTool) IsRetryable(err error) bool { return false }

func ptyErr(typ, detail string) *internal.ToolError {
	return &internal.ToolError{
		Tool: "pty_exec", Type: typ, Detail: detail,
		HintText: "Use action=list to see active sessions, or action=start to create a new one.",
	}
}
