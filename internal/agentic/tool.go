// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import "context"

// ToolSchema describes a tool's interface for the LLM, including its name,
// description, and JSON Schema for input validation.
type ToolSchema struct {
	// Name is the tool identifier used in tool calls.
	Name string
	// Description explains what the tool does to the LLM.
	Description string
	// Schema is a JSON Schema object describing the tool's parameters.
	Schema map[string]interface{}
}

// ToolResult carries the output of a tool execution plus control signals.
type ToolResult struct {
	Output string
	Error  error
	// StopTurn stops the current tool batch after this result.
	// Used by goal tools: non-active statuses stop the turn so the
	// model's summary response comes immediately.
	StopTurn bool
}

// Tool is the interface implemented by tools that can be used by an Agent.
// Each tool must provide a schema for LLM discovery and an Execute method
// that performs the actual work.
type Tool interface {
	// Schema returns the tool's metadata and parameter schema.
	Schema() ToolSchema
	// Execute runs the tool with the given JSON-encoded input and returns
	// the result as a string or an error.
	Execute(input string) (string, error)
	// IsRetryable returns true if the given error from Execute is transient
	// and should be retried (e.g., network timeout). Most tool errors are
	// deterministic (bad input) and should return false.
	IsRetryable(err error) bool
}

// ResultTool is an optional interface a Tool may implement to return a
// ToolResult with control signals such as StopTurn. The agent loop checks
// for this interface first and falls back to Execute() for tools that do not
// implement it, keeping the base Tool contract unchanged.
type ResultTool interface {
	Tool
	ExecuteWithResult(input string) (ToolResult, error)
}

// ContextTool is an optional interface a Tool may implement to receive the
// caller's context.Context. Tools that perform I/O (network, shell, MCP) should
// implement ExecuteContext so the agent's turn context (and thus Stop() / user
// cancellation) propagates into the tool and can interrupt long-running or
// hung calls. The agent prefers ContextTool over ResultTool/Execute when both
// are present.
type ContextTool interface {
	Tool
	ExecuteContext(ctx context.Context, input string) (string, error)
}

// BaseTool provides a default IsRetryable implementation that returns false.
// Embed this in tool structs to satisfy the Tool interface without retry.
type BaseTool struct{}

// IsRetryable returns false by default — most tool errors are deterministic.
func (BaseTool) IsRetryable(err error) bool { return false }

// ToolLoopHints carries tool-supplied metadata used by the tool-loop
// controller (ToolLoopController). Tools implement the optional LoopAnnotated
// interface to provide these hints so the controller never needs to hardcode
// tool names (e.g. "bash", "terminal", "render_html"). The zero value is a
// safe default: not one-shot, heal arg falls back to "query", default status.
type ToolLoopHints struct {
	// OneShot reports that repeat calls within a single assistant response are
	// suppressed after the first one succeeds (e.g. a render/preview tool).
	OneShot bool
	// HealArg is the argument key used when wrapping a raw (non-JSON) argument
	// into an object during auto-heal. Empty falls back to "query".
	HealArg string
	// Status returns a short, human-readable status line shown to the TUI for
	// an in-flight call with the given arguments. Return "" to use the
	// controller's default ("Calling: <name>").
	Status func(arguments string) string
}

// LoopAnnotated is an optional interface a Tool may implement to influence how
// the tool-loop controller treats its calls. It keeps the controller free of
// per-tool special cases: to add one-shot behavior, a custom heal key, or a
// custom status line, implement this interface on the tool instead of editing
// the controller's switches (Open/Closed Principle).
type LoopAnnotated interface {
	Tool
	LoopHints() ToolLoopHints
}

// StateMutator is an optional interface a Tool may implement to declare that a
// successful execution can change shared state (the filesystem, a shell
// environment, an external service). The tool-loop guardrails use it to reset
// the "no-progress" repeat horizon: when a mutating call succeeds, the world
// may have changed, so re-running a previously seen exact call is meaningful
// again rather than a stall. Tools that only read (read, search, fetch) must
// NOT implement this. Tools that can write (edit, write, bash, python,
// terminal, sub-agents) should return true.
type StateMutator interface {
	Tool
	// MutatesState reports whether a successful execution of this tool may
	// change observable state. It is consulted only after a call succeeds.
	MutatesState() bool
}
