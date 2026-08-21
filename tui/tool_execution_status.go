// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pijalu/goa/internal/ansi"
)

func (tc *ToolExecutionComponent) SetOnInvalidate(fn func()) {
	tc.onInvalidate = fn
}

// SetToolViewPolicy attaches the global tool-view policy (effective expand
// mode + preview line count) from the owning ChatViewport. Must be called
// before the first render so the widget honours the config/Ctrl+O state.
func (tc *ToolExecutionComponent) SetToolViewPolicy(p ToolViewPolicy) {
	tc.viewPolicy = p
	tc.invalidateBody()
	tc.updateBox()
	tc.Invalidate()
	if tc.onInvalidate != nil {
		tc.onInvalidate()
	}
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
	tc.outputBytes = len(output)
	tc.outputLines = strings.Count(output, "\n")
	if output != "" && !strings.HasSuffix(output, "\n") {
		tc.outputLines++ // final partial line
	}
	tc.invalidateBody()
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
	old := tc.status
	tc.status = status
	if status == ToolSuccess || status == ToolError {
		tc.isPartial = false
	}
	// Restart the elapsed timer when execution actually begins so the display
	// reflects the execution phase only (streaming/approval time excluded) and
	// stays within the tool's timeout bound. Re-setting ToolRunning (duplicate
	// non-delta events) must NOT restart the timer.
	if status == ToolRunning && old != ToolRunning {
		tc.startTime = time.Now()
	}
	tc.invalidateBody()
	tc.updateBox()
	tc.Invalidate()
	if tc.onInvalidate != nil {
		tc.onInvalidate()
	}
	if tc.onStatusChange != nil && old != status {
		tc.onStatusChange(old, status)
	}
}

// SetPartial marks the component as still streaming/running.
func (tc *ToolExecutionComponent) SetPartial(partial bool) {
	tc.isPartial = partial
	tc.invalidateBody()
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
		if tc.argsComplete {
			// Queued behind the scheduler (conflict / MaxParallel): show the
			// hourglass so the wait is visually distinct from execution
			// (Bug W: use ⧖ instead of the dots for waiting).
			return "⧖", TheTheme.ColorHex("tool_running")
		}
		return "◉", TheTheme.ColorHex("tool_running")
	case ToolRunning:
		// Static amber dot for the on-going marker — never the animated
		// spinner frame (keep the yellow dot).
		return "●", TheTheme.ColorHex("tool_running")
	case ToolSuccess:
		return "✓", TheTheme.ColorHex("tool_success")
	case ToolError:
		return "✗", TheTheme.ColorHex("tool_error")
	default:
		return "·", TheTheme.ColorHex("system_msg")
	}
}

// effectiveExpanded returns the effective expanded state for the widget,
// considering both the per-widget toggle (tc.expanded) and the global view
// policy. For read tools, the showRead policy prevents global expansion when
// false so read output stays silent by default, while the per-widget toggle
// (Ctrl+O/Enter on the block) still works.
func (tc *ToolExecutionComponent) effectiveExpanded() bool {
	// An explicit per-widget toggle wins over the global policy in both
	// directions, and persists across streaming re-renders.
	if tc.expandedSet {
		return tc.expanded
	}
	if tc.viewPolicy == nil {
		return tc.expanded
	}
	if !tc.viewPolicy.EffectiveToolsExpanded() {
		return tc.expanded
	}
	if tc.toolName == "read" && !tc.viewPolicy.ShowReadContent() {
		return tc.expanded
	}
	return true
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
		tc.setExpandedExplicit(!tc.effectiveExpanded())
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
