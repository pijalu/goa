// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/agentic"
)

// GoaCommandTool exposes all Goa commands to the LLM as a single tool
// named "goa". The agent uses this to execute commands like
// /mode, /help, etc. via the LLM.
type GoaCommandTool struct {
	router *CommandRouter
	ctxFn  func() Context
	ctx    Context
}

// NewGoaCommandTool creates a new GoaCommandTool with a fixed context.
func NewGoaCommandTool(router *CommandRouter, ctx Context) *GoaCommandTool {
	return &GoaCommandTool{router: router, ctx: ctx}
}

// NewGoaCommandToolWithContextFn creates a GoaCommandTool that resolves the
// execution context lazily. Used when the context is not available at the
// time the tool is constructed.
func NewGoaCommandToolWithContextFn(router *CommandRouter, ctxFn func() Context) *GoaCommandTool {
	return &GoaCommandTool{router: router, ctxFn: ctxFn}
}

// SetContextFn updates the lazy context provider.
func (g *GoaCommandTool) SetContextFn(ctxFn func() Context) {
	g.ctxFn = ctxFn
}

// Schema returns the tool schema for the goa_command tool.
func (g *GoaCommandTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "goa",
		Description: "Execute a Goa command (like /help, /mode, /model, /config, /skills). Use this to query or change Goa's behavior.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command_string": map[string]any{
					"type":        "string",
					"description": "The full command string, e.g., '/mode confirm' or '/help'",
				},
			},
			"required": []string{"command_string"},
		},
	}
}

// goaCommandParams holds the parsed input for GoaCommandTool.
type goaCommandParams struct {
	CommandString string `json:"command_string"`
}

// Execute runs the given command string through the command router.
func (g *GoaCommandTool) Execute(input string) (string, error) {
	cmdStr := input

	// Try JSON-wrapped input first
	if len(input) > 0 && input[0] == '{' {
		var p goaCommandParams
		if err := json.Unmarshal([]byte(input), &p); err == nil && p.CommandString != "" {
			cmdStr = p.CommandString
		}
	}

	cmdStr = strings.TrimSpace(cmdStr)
	if cmdStr == "" {
		return "", fmt.Errorf("no command provided")
	}
	if cmdStr[0] != '/' {
		cmdStr = "/" + cmdStr
	}

	result := g.router.Parse(cmdStr)
	if result == nil {
		return "", fmt.Errorf("not a command: %s", cmdStr)
	}

	ctx := g.ctx
	if g.ctxFn != nil {
		ctx = g.ctxFn()
	}

	output, err := g.router.Execute(ctx, result)
	if err != nil {
		return "", err
	}
	return output, nil
}

// IsRetryable returns false — command execution errors are deterministic.
func (g *GoaCommandTool) IsRetryable(err error) bool {
	return false
}
