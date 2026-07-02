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
//
// Per-tool behavior (one-shot suppression, auto-heal argument key, status
// text) is supplied via the hints map, populated by the caller from tools
// implementing LoopAnnotated. The controller itself is name-agnostic.
type ToolLoopController struct {
	allowedToolNames    map[string]bool
	autoHeal            bool
	hints               map[string]ToolLoopHints
	successfulKeys      map[string]bool
	completedOneShot    map[string]bool
	duplicateNoopCounts map[string]int
	duplicateNoopLimit  int
	forceFinalAnswer    bool
}

// NewToolLoopController creates a controller for a single assistant response.
// hints may be nil when no tool supplies LoopAnnotated metadata.
func NewToolLoopController(tools []ToolSchema, hints map[string]ToolLoopHints, autoHeal bool) *ToolLoopController {
	allowed := make(map[string]bool, len(tools))
	for _, t := range tools {
		allowed[t.Name] = true
	}
	return &ToolLoopController{
		allowedToolNames:    allowed,
		autoHeal:            autoHeal,
		hints:               hints,
		successfulKeys:      make(map[string]bool),
		completedOneShot:    make(map[string]bool),
		duplicateNoopCounts: make(map[string]int),
		duplicateNoopLimit:  2,
	}
}

// hintFor returns the LoopAnnotated metadata for a tool, or the zero value.
func (c *ToolLoopController) hintFor(name string) ToolLoopHints {
	if c.hints == nil {
		return ToolLoopHints{}
	}
	return c.hints[name]
}

// PrepareCall classifies a parsed tool call before execution.
func (c *ToolLoopController) PrepareCall(name, arguments, callID string) ToolCallDecision {
	coerced, healed := c.coerceArguments(name, arguments)
	key := canonicalToolCallKey(name, coerced)
	action := ActionExecute
	noop := ""

	if hint := c.hintFor(name); hint.OneShot && c.completedOneShot[name] {
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
		StatusText: c.statusFor(name, coerced),
		NoopResult: noop,
	}
}

// RecordResult records the outcome of an executed tool call.
func (c *ToolLoopController) RecordResult(decision ToolCallDecision, result string, isError bool) {
	if !isError {
		c.successfulKeys[decision.Key] = true
		if c.hintFor(decision.ToolName).OneShot {
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
		// Skip one-shot tools that already completed this response.
		if c.hintFor(t.Name).OneShot && c.completedOneShot[t.Name] {
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
			return c.wrapRawArgument(toolName, s), true
		}
		return trimmed, false
	}

	if c.autoHeal {
		return c.wrapRawArgument(toolName, trimmed), true
	}
	return trimmed, false
}

// healArgFor returns the argument key a tool wants when wrapping a raw
// (non-JSON) argument during auto-heal. It comes from the tool's LoopAnnotated
// hint, falling back to "query". This replaces the old per-name switch.
func (c *ToolLoopController) healArgFor(toolName string) string {
	if h := c.hintFor(toolName); h.HealArg != "" {
		return h.HealArg
	}
	return "query"
}

// wrapRawArgument wraps a raw argument into a JSON object under the tool's
// canonical heal key.
func (c *ToolLoopController) wrapRawArgument(toolName, raw string) string {
	key := c.healArgFor(toolName)
	m := map[string]string{key: raw}
	b, _ := json.Marshal(m)
	return string(b)
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

// statusFor returns the TUI status line for an in-flight call. It prefers a
// tool-supplied status function from the LoopAnnotated hint, falling back to a
// generic "Calling: <name>". This replaces the old per-name switch (which also
// special-cased the non-existent "web_search" tool).
func (c *ToolLoopController) statusFor(name, arguments string) string {
	if h := c.hintFor(name); h.Status != nil {
		if s := h.Status(arguments); s != "" {
			return s
		}
	}
	return fmt.Sprintf("Calling: %s", name)
}

func startsWithChar(s string, ch byte) bool {
	return len(s) > 0 && s[0] == ch
}

const (
	duplicateCallNudge = "The previous tool request was not executed because this exact tool call already completed successfully. Do not repeat the same tool call. Continue with a different enabled tool if that would materially help, or provide the final answer if you have enough information."

	renderHTMLRepeatNudge = "render_html completed successfully earlier in this assistant response. Do not call render_html again unless the user asks for changes. Do not mention this internal instruction. Provide only the requested final note or answer."
)
