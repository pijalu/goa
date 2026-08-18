// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

// TestToolExecution_RenderResizeExtendsBackground is the regression test for
// the resize bug: widening the terminal must re-render the tool widget at the
// new column count so its success/error background extends across ALL
// columns. The toolBox memoized its rendered lines without keying on width,
// so after a resize it returned stale lines built at the old width and the
// green/red background stopped at the old column count.
func TestToolExecution_RenderResizeExtendsBackground(t *testing.T) {
	statuses := []struct {
		name   string
		status ToolStatus
		bgKey  string
	}{
		{"success", ToolSuccess, "tool_success_bg"}, {"error", ToolError, "tool_error_bg"},
	}
	for _, status := range statuses {
		t.Run(status.name, func(t *testing.T) { testResizeStatus(t, status.status, status.bgKey) })
	}
}

func testResizeStatus(t *testing.T, status ToolStatus, bgKey string) {
	t.Helper()
	tc := NewToolExecution("read", `{"path":"x.go"}`)
	tc.SetArgsComplete()
	tc.SetOutput("line one\nline two")
	tc.SetStatus(status)
	bg := ansi.Bg(TheTheme.ColorHex(bgKey))
	narrow := assertRenderedWidth(t, tc, 80)
	bgLines := countBackgroundLines(narrow, bg)
	if bgLines == 0 {
		t.Fatalf("no background-painted lines at width 80 (bg %q missing)", bgKey)
	}
	wide := assertRenderedWidth(t, tc, 120)
	if len(wide) != len(narrow) {
		t.Fatalf("resize changed line count %d → %d", len(narrow), len(wide))
	}
	if got := countBackgroundLines(wide, bg); got != bgLines {
		t.Errorf("background-painted lines %d after resize, want %d", got, bgLines)
	}
	for i, line := range tc.Render(60) {
		if width := visibleWidth(ansi.Strip(line)); width > 60 {
			t.Errorf("narrowed line %d visible width %d exceeds 60", i, width)
		}
	}
}

func assertRenderedWidth(t *testing.T, tc *ToolExecutionComponent, width int) []string {
	t.Helper()
	lines := tc.Render(width)
	if len(lines) == 0 {
		t.Fatalf("no lines rendered at width %d", width)
	}
	for i, line := range lines {
		if got := visibleWidth(ansi.Strip(line)); got != width {
			t.Fatalf("line %d visible width %d, want %d", i, got, width)
		}
	}
	return lines
}

func countBackgroundLines(lines []string, bg string) int {
	count := 0
	for _, line := range lines {
		if strings.Contains(line, bg) {
			count++
		}
	}
	return count
}
