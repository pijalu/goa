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
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
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

// Config is the user-facing hook configuration.
type Config struct {
	// Hooks is the list of lifecycle hooks to execute.
	Hooks []Hook `yaml:"hooks"`
}

// Hook describes a single command to execute for a lifecycle event.
type Hook struct {
	// Event is the lifecycle event that triggers this hook.
	Event Event `yaml:"event"`
	// Command is the executable to run.
	Command string `yaml:"command"`
	// Args are optional fixed arguments prepended to the payload.
	Args []string `yaml:"args,omitempty"`
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

// FireBeforeTool executes all beforeTool hooks and returns an error if any hook
// vetoes the tool execution (non-zero exit).
func (e *Engine) FireBeforeTool(ctx context.Context, payload ToolPayload) error {
	return e.fireAll(ctx, EventBeforeTool, payload)
}

// FireAfterTool executes all afterTool hooks.
func (e *Engine) FireAfterTool(ctx context.Context, payload ToolPayload) error {
	return e.fireAll(ctx, EventAfterTool, payload)
}

// FireSessionStart executes all sessionStart hooks.
func (e *Engine) FireSessionStart(ctx context.Context, payload SessionPayload) error {
	return e.fireAll(ctx, EventSessionStart, payload)
}

// FireSessionEnd executes all sessionEnd hooks.
func (e *Engine) FireSessionEnd(ctx context.Context, payload SessionPayload) error {
	return e.fireAll(ctx, EventSessionEnd, payload)
}

func (e *Engine) fireAll(ctx context.Context, event Event, payload any) error {
	for _, h := range e.cfg.Hooks {
		if h.Event != event {
			continue
		}
		if err := e.fire(ctx, h, payload); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) fire(ctx context.Context, h Hook, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("hook %s: marshal payload: %w", h.Event, err)
	}

	entry := Entry{
		Event:     h.Event,
		Command:   h.Command,
		Args:      h.Args,
		Payload:   string(data),
		StartedAt: time.Now(),
	}
	defer func() {
		entry.FinishedAt = time.Now()
		_ = e.store.Record(entry)
	}()

	args := append([]string{}, h.Args...)
	cmd := exec.CommandContext(ctx, h.Command, args...)
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.CombinedOutput()
	entry.Output = string(out)
	entry.ExitCode = exitCode(cmd, err)
	if err != nil && h.Event == EventBeforeTool {
		return fmt.Errorf("beforeTool hook %q vetoed: %w\n%s", h.Command, err, string(out))
	}
	return nil
}

func exitCode(cmd *exec.Cmd, err error) int {
	if cmd != nil && cmd.ProcessState != nil {
		return cmd.ProcessState.ExitCode()
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
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
