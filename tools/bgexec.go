// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/background"
)

// BGExecTool manages background processes using a durable task manager.
type BGExecTool struct {
	mgr *background.Manager
}

// NewBGExecTool creates a new BGExecTool with an in-memory manager.
func NewBGExecTool() *BGExecTool {
	mgr, _ := background.NewManager("")
	t := &BGExecTool{mgr: mgr}
	// Register cleanup on exit
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-c
		t.StopAll()
	}()
	return t
}

// NewBGExecToolWithPath creates a BGExecTool that persists task metadata to
// the given JSON path.
func NewBGExecToolWithPath(path string) (*BGExecTool, error) {
	mgr, err := background.NewManager(path)
	if err != nil {
		return nil, err
	}
	t := &BGExecTool{mgr: mgr}
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-c
		t.StopAll()
	}()
	return t, nil
}

// Schema returns the tool schema for bg_exec.
func (t *BGExecTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "bg_exec",
		Description: "Manage background processes.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type": "string", "description": "Action to perform",
					"enum": []string{"start", "status", "read", "write", "stop", "list"},
				},
				"command": map[string]any{
					"type": "string", "description": "Command to start",
				},
				"id": map[string]any{
					"type": "string", "description": "Process ID for status/read/write/stop actions",
				},
				"workdir": map[string]any{
					"type": "string", "description": "Working directory",
				},
				"env": map[string]any{
					"type": "object", "additionalProperties": map[string]any{"type": "string"},
					"description": "Environment variables",
				},
				"input": map[string]any{
					"type": "string", "description": "Input to write to process stdin",
				},
				"tail_lines": map[string]any{
					"type": "integer", "description": "Number of recent lines to read (default: 20)",
				},
			},
			"required": []string{"action"},
		},
	}
}

// bgExecParams holds fields common across all bg_exec actions.
type bgExecParams struct {
	Action    string            `json:"action"`
	ID        string            `json:"id"`
	Command   string            `json:"command"`
	Workdir   string            `json:"workdir"`
	Env       map[string]string `json:"env"`
	Input     string            `json:"input"`
	TailLines int               `json:"tail_lines"`
}

// Execute runs the requested background process action.
func (t *BGExecTool) Execute(input string) (string, error) {
	var p bgExecParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		return "", &internal.ToolError{
			Tool: "bg_exec", Type: "invalid_input",
			Detail:   fmt.Sprintf("Cannot parse parameters: %v", err),
			HintText: "Ensure your input is valid JSON with the required fields.",
		}
	}
	switch p.Action {
	case "start":
		return t.start(p)
	case "status":
		return t.status(p)
	case "read":
		return t.read(p)
	case "write":
		return t.write(p)
	case "stop":
		return t.stop(p)
	case "list":
		return t.list()
	default:
		return "", &internal.ToolError{
			Tool: "bg_exec", Type: "unknown_action",
			Detail:   fmt.Sprintf("Unknown action: %s", p.Action),
			HintText: "Use one of: start, status, read, write, stop, list",
		}
	}
}

func (t *BGExecTool) IsRetryable(err error) bool { return false }

//go:embed bgexec.short.md bgexec.long.md
var bgexecDocs embed.FS

func (t *BGExecTool) ShortDoc() string { return readDoc(bgexecDocs, "bgexec.short.md") }
func (t *BGExecTool) LongDoc() string  { return readDoc(bgexecDocs, "bgexec.long.md") }

func (t *BGExecTool) Examples() []string {
	return []string{
		`{"action": "start", "command": "npm run dev"}`,
		`{"action": "status", "id": "proc-1"}`,
		`{"action": "read", "id": "proc-1", "tail_lines": 50}`,
		`{"action": "stop", "id": "proc-1"}`,
	}
}

func (t *BGExecTool) start(p bgExecParams) (string, error) {
	if p.Command == "" {
		return "", &internal.ToolError{
			Tool: "bg_exec", Type: "missing_command",
			Detail: "No command provided", HintText: "Provide a command in the 'command' field.",
		}
	}

	task, err := t.mgr.Start(p.Command, p.Workdir, p.Env)
	if err != nil {
		return "", bgErr("start_failed", err.Error())
	}
	return fmt.Sprintf("[bg_exec: start] Process %s started — PID %d\nCommand: %s", task.ID, task.PID, p.Command), nil
}

func (t *BGExecTool) status(p bgExecParams) (string, error) {
	id := p.ID
	if id == "" {
		return "", bgErr("missing_id", "id is required for status action")
	}

	task := t.mgr.Get(id)
	if task == nil {
		return "", &internal.ToolError{
			Tool: "bg_exec", Type: "process_not_found",
			Detail:   fmt.Sprintf("Process %q not found", id),
			HintText: "Use list action to see active processes.",
		}
	}

	if task.Status == background.StatusCompleted || task.Status == background.StatusError || task.Status == background.StatusKilled {
		return fmt.Sprintf("[bg_exec: status] %s — exited with code %d (ran for %s)",
			id, task.ExitCode, time.Since(task.StartTime).Round(time.Second)), nil
	}
	return fmt.Sprintf("[bg_exec: status] %s — running (PID %d, uptime %s)",
		id, task.PID, time.Since(task.StartTime).Round(time.Second)), nil
}

func (t *BGExecTool) read(p bgExecParams) (string, error) {
	id := p.ID
	tailLines := p.TailLines
	if tailLines <= 0 {
		tailLines = 20
	}

	if id == "" {
		return "", bgErr("missing_id", "id is required for read action")
	}

	if t.mgr.Get(id) == nil {
		return "", &internal.ToolError{
			Tool: "bg_exec", Type: "process_not_found",
			Detail:   fmt.Sprintf("Process %q not found", id),
			HintText: "Use list action to see active processes.",
		}
	}

	stdoutLines, stderrLines := t.mgr.ReadOutput(id, tailLines)

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "[bg_exec: read] %s — last %d lines\n", id, tailLines)
	fmt.Fprintf(&buf, "--- stdout ---\n")
	for _, line := range stdoutLines {
		fmt.Fprintln(&buf, line)
	}
	fmt.Fprintf(&buf, "--- stderr ---\n")
	for _, line := range stderrLines {
		fmt.Fprintln(&buf, line)
	}
	return buf.String(), nil
}

func (t *BGExecTool) write(p bgExecParams) (string, error) {
	id := p.ID
	text := p.Input

	if id == "" {
		return "", bgErr("missing_id", "id is required for write action")
	}

	if err := t.mgr.WriteInput(id, text); err != nil {
		return "", bgErr("write_failed", fmt.Sprintf("write to stdin: %v", err))
	}
	return fmt.Sprintf("[bg_exec: write] %s — wrote %d bytes to stdin", id, len(text)), nil
}

func (t *BGExecTool) stop(p bgExecParams) (string, error) {
	id := p.ID
	if id == "" {
		return "", bgErr("missing_id", "id is required for stop action")
	}

	task, err := t.mgr.Stop(id, sigkillGrace)
	if err != nil {
		if task == nil {
			return "", bgErr("process_not_found", fmt.Sprintf("Process %q not found", id))
		}
		return "", bgErr("signal_failed", err.Error())
	}

	if task.Status == background.StatusCompleted || task.Status == background.StatusError {
		return fmt.Sprintf("[bg_exec: stop] %s — already exited (code %d)", id, task.ExitCode), nil
	}
	return fmt.Sprintf("[bg_exec: stop] %s — terminated", id), nil
}

// sigkillGrace is the time stop waits after SIGTERM before SIGKILL.
const sigkillGrace = 5 * time.Second

func (t *BGExecTool) list() (string, error) {
	tasks := t.mgr.List()
	if len(tasks) == 0 {
		return "[bg_exec: list] No active processes", nil
	}

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "[bg_exec: list] %d process(es)\n", len(tasks))
	for _, task := range tasks {
		switch task.Status {
		case background.StatusRunning:
			fmt.Fprintf(&buf, "  %s — PID %d, running %s\n", task.ID, task.PID, time.Since(task.StartTime).Round(time.Second))
		default:
			fmt.Fprintf(&buf, "  %s — %s (%d), ran %s\n", task.ID, task.Status, task.ExitCode, time.Since(task.StartTime).Round(time.Second))
		}
	}
	return buf.String(), nil
}

// StopAll terminates all running background processes.
func (t *BGExecTool) StopAll() {
	t.mgr.StopAll(3 * time.Second)
}

// Manager returns the underlying background manager. Exported for tests.
func (t *BGExecTool) Manager() *background.Manager { return t.mgr }

// bgErr builds a *internal.ToolError for bg_exec actions.
func bgErr(typ, detail string) *internal.ToolError {
	return &internal.ToolError{
		Tool: "bg_exec", Type: typ, Detail: detail,
		HintText: "Use action=list to see active processes.",
	}
}
