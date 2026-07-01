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
