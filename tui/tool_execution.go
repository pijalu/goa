// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/pijalu/goa/internal/ansi"
)

// ToolStatus represents the execution state of a tool call.
type ToolStatus int

const (
	ToolPending ToolStatus = iota
	ToolRunning
	ToolSuccess
	ToolError
)

//── ToolExecutionComponent (Container: Box) ──
//
// Architecture: Box(1,1,bg) → renders [topPad, header, body..., bottomPad] with bg

// ToolExecutionComponent displays a single tool call with expand/collapse,
// status colors, and visual truncation.
type ToolExecutionComponent struct {
	Container
	box       *toolBox
	toolName  string
	toolArgs  string
	args      map[string]any
	output    string
	expanded  bool
	status    ToolStatus
	duration  string
	isPartial bool
	renderer  ToolRenderer
	generic   genericRenderer

	// onInvalidate is called when internal state changes (output, status,
	// duration) so the owning ChatViewport can invalidate its render cache.
	onInvalidate func()

	// agentLabel is the owning agent's display label (e.g. "coder"). When set,
	// it is rendered as a colored prefix on the tool header so multiple agents'
	// tool calls are distinguishable in the chat viewport.
	agentLabel string
}

// toolBox renders the tool header, body, and trailing blank with the
// appropriate background color.
type toolBox struct {
	header   string
	body     string
	duration string
	bgAnsi   string
	rendered []string
}

func (b *toolBox) Render(width int) []string {
	if b.rendered == nil {
		b.rendered = b.build(width)
	}
	return b.rendered
}
func (b *toolBox) HandleInput(string) {}
func (b *toolBox) Invalidate()        { b.rendered = nil }

func (b *toolBox) build(width int) []string {
	if width <= 0 {
		return nil
	}
	var lines []string

	// Top padding (like Box paddingY=1)
	lines = append(lines, b.bgLine("", width))

	// Header
	lines = append(lines, b.bgLine(b.header, width))

	// Body
	if b.body != "" {
		for _, line := range strings.Split(b.body, "\n") {
			lines = append(lines, b.bgLine(line, width))
		}
	}

	// Duration
	if b.duration != "" {
		lines = append(lines, b.bgLine(ansiMuted(fmt.Sprintf("Took %s", b.duration)), width))
	}

	// Bottom padding (like Box paddingY=1)
	lines = append(lines, b.bgLine("", width))

	return lines
}

func (b *toolBox) bgLine(s string, width int) string {
	return padToWidthStyled(" "+s, width, b.bgAnsi)
}

// ── Construction ──

// NewToolExecution creates a new tool execution component.
func NewToolExecution(toolName, toolArgs string) *ToolExecutionComponent {
	tc := &ToolExecutionComponent{
		toolName:  toolName,
		toolArgs:  toolArgs,
		status:    ToolPending,
		isPartial: true,
		renderer:  GetToolRenderer(toolName),
		box:       &toolBox{},
	}
	tc.updateBox()
	tc.AddChild(tc.box)
	return tc
}

// updateBox rebuilds the box header and body from current state.
func (tc *ToolExecutionComponent) updateBox() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("goa: ToolExecutionComponent.updateBox panic (tool=%s): %v", tc.toolName, r)
			// Leave a minimal visible header so the widget does not vanish.
			tc.box.header = ansi.Fg(TheTheme.ColorHex("tool_running")) + "·" + " " + ansiBoldToolTitle(tc.toolName)
			tc.box.body = ""
			tc.box.duration = ""
			tc.box.bgAnsi = tc.bgANSI()
			tc.box.Invalidate()
		}
	}()

	renderer := tc.renderer
	if renderer == nil {
		renderer = tc.generic
	}

	ctx := RenderContext{
		Expanded:     tc.expanded,
		IsPartial:    tc.isPartial,
		IsError:      tc.status == ToolError,
		ArgsComplete: true,
	}

	// Build header
	icon, iconColor := tc.statusIcon()
	call := renderer.RenderCall(tc.args, ctx)
	if call == "" {
		call = ansiBoldToolTitle(tc.toolName)
		if tc.toolArgs != "" {
			call += " " + ansiToolOutput(tc.toolArgs)
		}
	}
	if tc.agentLabel != "" {
		call = ansi.Fg(hashColor(tc.agentLabel)) + "[" + tc.agentLabel + "]" + ansi.Reset + " " + call
	}
	tc.box.header = ansi.Fg(iconColor) + icon + " " + ansi.FgReset + call

	// Build body
	if tc.output != "" {
		tc.box.body = renderer.RenderResult(tc.output, ctx)
	} else {
		tc.box.body = ""
	}

	// Duration
	tc.box.duration = tc.duration

	// Background: bash/terminal renderers request the default background so
	// the output looks like raw shell output rather than a colored box.
	if dbr, ok := renderer.(interface{ DefaultBackground() bool }); ok && dbr.DefaultBackground() {
		tc.box.bgAnsi = ""
	} else {
		tc.box.bgAnsi = tc.bgANSI()
	}

	tc.box.Invalidate()
}

// ── Setters ──

// SetExpanded toggles between preview and full output.
func (tc *ToolExecutionComponent) SetExpanded(expanded bool) {
	tc.expanded = expanded
	tc.updateBox()
	tc.Invalidate()
}

// SetToolArgs sets the formatted arguments string.
func (tc *ToolExecutionComponent) SetToolArgs(args string) {
	tc.toolArgs = args
	tc.updateBox()
	tc.Invalidate()
}

// SetArgs parses and stores the structured arguments for renderer use.
func (tc *ToolExecutionComponent) SetArgs(args map[string]any) {
	tc.args = args
	tc.updateBox()
	tc.Invalidate()
}

// SetArgsJSON parses JSON arguments and stores them for the renderer.
func (tc *ToolExecutionComponent) SetArgsJSON(argsJSON string) {
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err == nil {
		tc.args = args
	}
	tc.toolArgs = FormatToolArgs(tc.toolName, argsJSON)
	tc.updateBox()
	tc.Invalidate()
}

// SetOnInvalidate registers a callback invoked whenever the component's
// internal state changes, allowing the owning viewport to invalidate its
// render cache.
func (tc *ToolExecutionComponent) SetOnInvalidate(fn func()) {
	tc.onInvalidate = fn
}

// SetAgentLabel sets the display label prefix for the tool widget.
func (tc *ToolExecutionComponent) SetAgentLabel(label string) {
	tc.agentLabel = label
	tc.updateBox()
	tc.Invalidate()
	if tc.onInvalidate != nil {
		tc.onInvalidate()
	}
}

// SetOutput sets the tool's output text.
func (tc *ToolExecutionComponent) SetOutput(output string) {
	tc.output = output
	tc.updateBox()
	tc.Invalidate()
	if tc.onInvalidate != nil {
		tc.onInvalidate()
	}
}

// Status returns the current execution status.
func (tc *ToolExecutionComponent) Status() ToolStatus {
	return tc.status
}

// SetStatus changes the execution status.
func (tc *ToolExecutionComponent) SetStatus(status ToolStatus) {
	tc.status = status
	if status == ToolSuccess || status == ToolError {
		tc.isPartial = false
	}
	tc.updateBox()
	tc.Invalidate()
	if tc.onInvalidate != nil {
		tc.onInvalidate()
	}
}

// SetPartial marks the component as still streaming/running.
func (tc *ToolExecutionComponent) SetPartial(partial bool) {
	tc.isPartial = partial
	tc.updateBox()
	tc.Invalidate()
	if tc.onInvalidate != nil {
		tc.onInvalidate()
	}
}

// SetDuration sets the execution duration string (e.g., "0.04s").
func (tc *ToolExecutionComponent) SetDuration(d string) {
	tc.duration = d
	tc.updateBox()
	tc.Invalidate()
	if tc.onInvalidate != nil {
		tc.onInvalidate()
	}
}

// ── Rendering (delegated to Container which renders spacer + box children) ──

// Invalidate clears cached rendering state.
func (tc *ToolExecutionComponent) Invalidate() {
	tc.Container.Invalidate()
}

// ── Helpers ──

func (tc *ToolExecutionComponent) bgColor() string {
	switch tc.status {
	case ToolPending, ToolRunning:
		return TheTheme.ColorHex("tool_pending_bg")
	case ToolSuccess:
		return TheTheme.ColorHex("tool_success_bg")
	case ToolError:
		return TheTheme.ColorHex("tool_error_bg")
	default:
		return ""
	}
}

func (tc *ToolExecutionComponent) statusIcon() (icon string, color string) {
	switch tc.status {
	case ToolPending:
		return "◉", TheTheme.ColorHex("tool_running")
	case ToolRunning:
		if frame := CurrentSpinnerFrame(); frame != "" {
			return frame, TheTheme.ColorHex("tool_running")
		}
		return "⟳", TheTheme.ColorHex("tool_running")
	case ToolSuccess:
		return "✓", TheTheme.ColorHex("tool_success")
	case ToolError:
		return "✗", TheTheme.ColorHex("tool_error")
	default:
		return "·", TheTheme.ColorHex("system_msg")
	}
}

func (tc *ToolExecutionComponent) bgANSI() string {
	bgHex := tc.bgColor()
	if bgHex == "" {
		return ""
	}
	return ansi.Bg(bgHex)
}

// HandleInput processes key events for expand/collapse.
func (tc *ToolExecutionComponent) HandleInput(data string) {
	if matchesKey(data, "ctrl+o") || matchesKey(data, "enter") {
		tc.SetExpanded(!tc.expanded)
	}
}

// ── ToolArgs formatting ──

// FormatToolArgs formats tool arguments for display.
func FormatToolArgs(name string, argsJSON string) string {
	switch name {
	case "read":
		return formatReadFileArgs(argsJSON)
	case "write":
		return extractJSONField(argsJSON, "path")
	case "edit":
		path := extractJSONField(argsJSON, "path")
		op := extractJSONField(argsJSON, "operation")
		if op != "" {
			return fmt.Sprintf("%s (%s)", path, op)
		}
		return path
	case "search":
		pattern := extractJSONField(argsJSON, "pattern")
		path := extractJSONField(argsJSON, "path")
		if path != "" {
			return fmt.Sprintf("%s in %s", pattern, path)
		}
		return pattern
	case "bash":
		cmd := extractJSONField(argsJSON, "command")
		if len(cmd) > 60 {
			cmd = cmd[:57] + "..."
		}
		return cmd
	default:
		fields := []string{"path", "command", "name", "pattern", "id"}
		for _, f := range fields {
			if v := extractJSONField(argsJSON, f); v != "" {
				return v
			}
		}
		return ""
	}
}

// extractJSONField extracts a string field from a JSON string using proper JSON parsing.
func extractJSONField(raw, field string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return ""
	}
	if v, ok := m[field].(string); ok {
		return v
	}
	return ""
}

// formatReadFileArgs formats read arguments as path[:start][:end|+max].
func formatReadFileArgs(argsJSON string) string {
	path := extractJSONField(argsJSON, "path")
	start := extractJSONIntField(argsJSON, "start_line")
	end := extractJSONIntField(argsJSON, "end_line")
	maxLines := extractJSONIntField(argsJSON, "max_lines")
	if start == "" && end == "" && maxLines == "" {
		return path
	}
	parts := []string{path}
	if start != "" {
		parts = append(parts, start)
	} else {
		parts = append(parts, "1")
	}
	if end != "" {
		parts = append(parts, end)
	} else if maxLines != "" {
		parts = append(parts, "+"+maxLines)
	}
	return strings.Join(parts, ":")
}

// extractJSONIntField extracts an integer field from a JSON string.
func extractJSONIntField(raw, field string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return ""
	}
	switch v := m[field].(type) {
	case float64:
		if v == float64(int(v)) {
			return fmt.Sprintf("%d", int(v))
		}
		return fmt.Sprintf("%g", v)
	case string:
		return v
	}
	return ""
}
