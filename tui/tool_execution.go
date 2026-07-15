// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pijalu/goa/internal/ansi"
)

// partialStringFieldRe extracts quoted string fields from incomplete JSON
// objects during streaming tool-call argument display.
var partialStringFieldRe = regexp.MustCompile(`"([^"]+)":"((?:\\.|[^"\\])*)`)

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
	argsComplete bool
	renderer  ToolRenderer
	generic   genericRenderer
	startTime time.Time

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
		lines = append(lines, b.bgLine(ansiMuted(b.duration), width))
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
		startTime: time.Now(),
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
		ArgsComplete: tc.argsComplete,
		Args:         tc.args,
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
	tc.box.body = tc.buildBody(renderer, ctx)

	// Duration
	tc.renderDuration()

	// Background: bash/terminal renderers request the default background so
	// the output looks like raw shell output rather than a colored box.
	if dbr, ok := renderer.(interface{ DefaultBackground() bool }); ok && dbr.DefaultBackground() {
		tc.box.bgAnsi = ""
	} else {
		tc.box.bgAnsi = tc.bgANSI()
	}

	tc.box.Invalidate()
}

// buildBody chooses the right renderer path for the tool body. When the tool
// has produced output, RenderResult is used. While arguments are still
// streaming, a StreamingRenderer gets its RenderPartial hook; otherwise the
// legacy RenderResult("", partial) path is used.
func (tc *ToolExecutionComponent) buildBody(renderer ToolRenderer, ctx RenderContext) string {
	if tc.output != "" {
		return renderer.RenderResult(tc.output, ctx)
	}
	if !tc.isPartial {
		return ""
	}
	if sr, ok := renderer.(StreamingRenderer); ok {
		return sr.RenderPartial(tc.args, ctx)
	}
	return renderer.RenderResult("", ctx)
}

// renderDuration computes the duration string based on current status and
// elapsed time. It keeps the mutable duration state out of updateBox so the
// latter stays within the complexity budget. The stored value is the full
// display line ("elapsed X.XXs" or "Took X.XXs"); the box builder uses it
// directly. Durations of 0.01s or less are hidden to avoid noisy flicker for
// instantaneous tools.
func (tc *ToolExecutionComponent) renderDuration() {
	const minDuration = 10 * time.Millisecond // 0.01s
	elapsed := time.Since(tc.startTime)
	if elapsed <= minDuration {
		tc.box.duration = ""
		return
	}
	d := formatDuration(elapsed)
	switch tc.status {
	case ToolSuccess, ToolError:
		// Cache the final duration so repeated renders stay stable once the
		// tool has finished.
		if tc.duration == "" {
			tc.duration = d
		}
		tc.box.duration = "Took " + tc.duration
	case ToolPending, ToolRunning:
		tc.box.duration = "elapsed " + d
	default:
		tc.box.duration = tc.duration
	}
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

// SetArgsComplete marks the tool call arguments as fully received.
// This triggers the renderer to show the final header (no longer streaming).
func (tc *ToolExecutionComponent) SetArgsComplete() {
	tc.argsComplete = true
	tc.updateBox()
	tc.Invalidate()
}

// SetArgsPartial updates the header display with partial tool call
// arguments during streaming. Unlike SetArgsJSON, this does NOT attempt
// json.Unmarshal (partial JSON would fail). The renderer handles
// incomplete JSON via the ArgsComplete field in RenderContext.
func (tc *ToolExecutionComponent) SetArgsPartial(args string) {
	tc.toolArgs = args
	tc.updatePartialArgs(args)
	tc.updateBox()
	tc.Invalidate()
	if tc.onInvalidate != nil {
		tc.onInvalidate()
	}
}

// updatePartialArgs merges best-effort parsed fields from a partial JSON
// argument string into tc.args so renderers can display streaming content
// (e.g. write/edit content) before the full JSON is complete.
func (tc *ToolExecutionComponent) updatePartialArgs(raw string) {
	// If the partial string happens to be complete now, use the real parser.
	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
		tc.args = parsed
		return
	}

	// Extract string fields from the incomplete JSON. This is intentionally
	// a narrow, best-effort path for streaming tool-call display only.
	for _, m := range partialStringFieldRe.FindAllStringSubmatch(raw, -1) {
		if len(m) != 3 {
			continue
		}
		key := m[1]
		value := m[2]
		if u, err := strconv.Unquote(`"` + value + `"`); err == nil {
			value = u
		}
		if tc.args == nil {
			tc.args = make(map[string]any)
		}
		tc.args[key] = value
	}
}

// SetArgs parses and stores the structured arguments for renderer use.
func (tc *ToolExecutionComponent) SetArgs(args map[string]any) {
	tc.args = args
	tc.updateBox()
	tc.Invalidate()
}

// SetArgsJSON parses JSON arguments and stores them for the renderer.
// When the JSON is successfully parsed, args are marked as complete.
func (tc *ToolExecutionComponent) SetArgsJSON(argsJSON string) {
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err == nil {
		tc.args = args
		tc.argsComplete = true
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

// ToolName returns the name of the tool being executed.
func (tc *ToolExecutionComponent) ToolName() string {
	return tc.toolName
}

// ArgsComplete returns whether all tool call arguments have been received.
func (tc *ToolExecutionComponent) ArgsComplete() bool {
	return tc.argsComplete
}

// IsPartial reports whether the widget is still streaming/running and its
// output is a partial snapshot (e.g. streamed progress from a long-running
// tool). The final result clears it.
func (tc *ToolExecutionComponent) IsPartial() bool {
	return tc.isPartial
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

// Render renders the tool execution widget. While the tool is running, the
// elapsed duration is recomputed on every frame so the user sees live timing.
func (tc *ToolExecutionComponent) Render(width int) []string {
	if tc.status == ToolPending || tc.status == ToolRunning {
		if !tc.startTime.IsZero() {
			elapsed := fmt.Sprintf("elapsed %s", formatDuration(time.Since(tc.startTime)))
			if tc.box.duration != elapsed {
				// Rebuild the whole box so the spinner icon and duration both
				// refresh on the next render cycle.
				tc.updateBox()
			}
		}
	}
	return tc.Container.Render(width)
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

// formatDuration returns a concise human-readable duration string.
// Sub-second values show two decimals; seconds and up show one decimal.
func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%.2fs", d.Seconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

