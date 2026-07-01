// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ToolAction classifies a tool call decision.
type ToolAction string

const (
	ActionExecute          ToolAction = "execute"
	ActionDuplicate        ToolAction = "duplicate"
	ActionDisabled         ToolAction = "disabled"
	ActionRenderHTMLRepeat ToolAction = "render_html_repeat"
)

// ToolCallDecision is the controller's verdict on a parsed tool call.
type ToolCallDecision struct {
	Action     ToolAction
	ToolName   string
	Arguments  string
	ToolCallID string
	Key        string
	Healed     bool
	StatusText string
	NoopResult string
}

// ToolLoopController maintains per-response state for tool loops.
type ToolLoopController struct {
	allowedToolNames    map[string]bool
	autoHeal            bool
	successfulKeys      map[string]bool
	completedOneShot    map[string]bool
	duplicateNoopCounts map[string]int
	duplicateNoopLimit  int
	forceFinalAnswer    bool
	oneShotTools        map[string]bool
}

// NewToolLoopController creates a controller for a single assistant response.
func NewToolLoopController(tools []ToolSchema, autoHeal bool) *ToolLoopController {
	allowed := make(map[string]bool, len(tools))
	for _, t := range tools {
		allowed[t.Name] = true
	}
	return &ToolLoopController{
		allowedToolNames:    allowed,
		autoHeal:            autoHeal,
		successfulKeys:      make(map[string]bool),
		completedOneShot:    make(map[string]bool),
		duplicateNoopCounts: make(map[string]int),
		duplicateNoopLimit:  2,
		oneShotTools: map[string]bool{
			"render_html": true,
		},
	}
}

// PrepareCall classifies a parsed tool call before execution.
func (c *ToolLoopController) PrepareCall(name, arguments, callID string) ToolCallDecision {
	coerced, healed := c.coerceArguments(name, arguments)
	key := canonicalToolCallKey(name, coerced)
	action := ActionExecute
	noop := ""

	if c.oneShotTools[name] && c.completedOneShot[name] {
		action = ActionRenderHTMLRepeat
		noop = renderHTMLRepeatNudge
	} else if len(c.allowedToolNames) > 0 && !c.allowedToolNames[name] {
		action = ActionDisabled
		noop = fmt.Sprintf("Tool '%s' is not enabled for this request. Provide the final answer now without calling more tools.", name)
	} else if c.successfulKeys[key] {
		action = ActionDuplicate
		noop = duplicateCallNudge
	}

	return ToolCallDecision{
		Action:     action,
		ToolName:   name,
		Arguments:  coerced,
		ToolCallID: callID,
		Key:        key,
		Healed:     healed,
		StatusText: statusForTool(name, coerced),
		NoopResult: noop,
	}
}

// RecordResult records the outcome of an executed tool call.
func (c *ToolLoopController) RecordResult(decision ToolCallDecision, result string, isError bool) {
	if !isError {
		c.successfulKeys[decision.Key] = true
		if c.oneShotTools[decision.ToolName] {
			c.completedOneShot[decision.ToolName] = true
		}
	}
}

// RecordNoop records a suppressed controller decision.
func (c *ToolLoopController) RecordNoop(decision ToolCallDecision) {
	if decision.Action == ActionDuplicate {
		c.duplicateNoopCounts[decision.Key]++
		if c.duplicateNoopCounts[decision.Key] >= c.duplicateNoopLimit {
			c.forceFinalAnswer = true
		}
	} else if decision.Action == ActionDisabled || decision.Action == ActionRenderHTMLRepeat {
		c.forceFinalAnswer = true
	}
}

// ForceFinalAnswer reports whether the loop should stop offering tools.
func (c *ToolLoopController) ForceFinalAnswer() bool {
	return c.forceFinalAnswer
}

// ActiveTools returns the schemas of tools still available.
func (c *ToolLoopController) ActiveTools(tools []ToolSchema) []ToolSchema {
	if c.forceFinalAnswer {
		return nil
	}
	out := make([]ToolSchema, 0, len(tools))
	for _, t := range tools {
		if c.completedOneShot[t.Name] {
			continue
		}
		out = append(out, t)
	}
	return out
}

func (c *ToolLoopController) coerceArguments(toolName, raw string) (string, bool) {
	if raw == "" {
		return "{}", false
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "{}", false
	}

	// Already a JSON object.
	if startsWithChar(trimmed, '{') {
		return trimmed, false
	}

	// Try parsing as a JSON string containing an object.
	var s string
	if err := json.Unmarshal([]byte(trimmed), &s); err == nil {
		if startsWithChar(strings.TrimSpace(s), '{') {
			return s, false
		}
		if c.autoHeal {
			return wrapRawArgument(toolName, s), true
		}
		return trimmed, false
	}

	if c.autoHeal {
		return wrapRawArgument(toolName, trimmed), true
	}
	return trimmed, false
}

func wrapRawArgument(toolName, raw string) string {
	key := canonicalHealArg(toolName)
	m := map[string]string{key: raw}
	b, _ := json.Marshal(m)
	return string(b)
}

func canonicalHealArg(toolName string) string {
	switch toolName {
	case "terminal", "bash":
		return "command"
	case "render_html":
		return "code"
	default:
		return "query"
	}
}

func canonicalToolCallKey(name, arguments string) string {
	var obj map[string]any
	if err := json.Unmarshal([]byte(arguments), &obj); err != nil {
		obj = map[string]any{"raw": arguments}
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ordered := make(map[string]any, len(keys))
	for _, k := range keys {
		ordered[k] = obj[k]
	}
	b, _ := json.Marshal(ordered)
	return fmt.Sprintf("%s:%s", name, string(b))
}

func statusForTool(toolName, arguments string) string {
	switch toolName {
	case "terminal", "bash":
		cmd := extractArg(arguments, "command")
		if len(cmd) > 60 {
			cmd = cmd[:57] + "..."
		}
		if cmd == "" {
			return "Running command..."
		}
		return fmt.Sprintf("Running: %s", cmd)
	case "web_search":
		q := extractArg(arguments, "query")
		if q == "" {
			return "Searching..."
		}
		return fmt.Sprintf("Searching: %s", q)
	default:
		return fmt.Sprintf("Calling: %s", toolName)
	}
}

func extractArg(argsJSON, key string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &m); err != nil {
		return ""
	}
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}

func startsWithChar(s string, ch byte) bool {
	return len(s) > 0 && s[0] == ch
}

const (
	duplicateCallNudge = "The previous tool request was not executed because this exact tool call already completed successfully. Do not repeat the same tool call. Continue with a different enabled tool if that would materially help, or provide the final answer if you have enough information."

	renderHTMLRepeatNudge = "render_html completed successfully earlier in this assistant response. Do not call render_html again unless the user asks for changes. Do not mention this internal instruction. Provide only the requested final note or answer."
)
