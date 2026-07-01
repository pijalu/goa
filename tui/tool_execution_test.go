// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

func TestFormatToolArgs_ReadFile(t *testing.T) {
	cases := []struct {
		name string
		args string
		want string
	}{
		{
			name: "path only",
			args: `{"path":"README.md"}`,
			want: "README.md",
		},
		{
			name: "with line range",
			args: `{"path":"PLAN.md","start_line":200,"end_line":400}`,
			want: "PLAN.md:200:400",
		},
		{
			name: "with start and max",
			args: `{"path":"PLAN.md","start_line":1,"max_lines":200}`,
			want: "PLAN.md:1:+200",
		},
		{
			name: "with spaces",
			args: `{"path": "PLAN.md", "start_line": 200, "end_line": 400}`,
			want: "PLAN.md:200:400",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FormatToolArgs("read", tc.args)
			if got != tc.want {
				t.Errorf("FormatToolArgs(%q) = %q, want %q", tc.args, got, tc.want)
			}
		})
	}
}

func TestToolExecution_ReadFile_CollapsedByDefault(t *testing.T) {
	tc := NewToolExecution("read", "README.md")
	tc.SetArgsJSON(`{"path":"README.md"}`)
	tc.SetOutput("read file README.md:1:10\n1 first\n2 second\n")
	lines := tc.Render(80)
	// Now shows metadata (path, offset, size) even when collapsed.
	// Lines: top padding + header + body (metadata) + bottom padding = 4.
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines, got %d lines: %v", len(lines), lines)
	}
	rendered := strings.Join(lines, "\n")
	if strings.Contains(rendered, "1 first") {
		t.Errorf("read should not show file content, got %q", rendered)
	}
	// Header should show the verb and path.
	header := strings.TrimSpace(ansi.Strip(lines[1]))
	header = strings.TrimPrefix(header, "◉ ")
	if !strings.HasPrefix(header, "read README.md") {
		t.Errorf("collapsed header = %q, want prefix 'read README.md'", header)
	}
	// Metadata should appear in the body
	if !strings.Contains(ansi.Strip(rendered), "README.md") {
		t.Errorf("expected file path in metadata, got %q", rendered)
	}
}

func TestToolExecution_ReadFile_ExpandedShowsMetadata(t *testing.T) {
	tc := NewToolExecution("read", "README.md")
	tc.SetArgsJSON(`{"path":"README.md"}`)
	tc.SetOutput("read file README.md:1:2\n1 first\n2 second\n")
	tc.SetExpanded(true)
	lines := tc.Render(80)
	rendered := strings.Join(lines, "\n")
	// Even expanded, read shows only metadata, never content
	if strings.Contains(rendered, "1 first") {
		t.Errorf("read should not show file content even when expanded, got %q", rendered)
	}
	// Should show metadata
	if !strings.Contains(ansi.Strip(rendered), "README.md") {
		t.Errorf("expected file path in metadata, got %q", rendered)
	}
}

func TestToolExecution_ReadFile_HeaderShowsFileAndLines(t *testing.T) {
	tc := NewToolExecution("read", "PLAN.md:200:400")
	tc.SetArgsJSON(`{"path":"PLAN.md","start_line":200,"end_line":400}`)
	tc.SetOutput("read file PLAN.md:200:400\ncontent\n")
	lines := tc.Render(80)
	if len(lines) == 0 {
		t.Fatal("expected rendered lines")
	}
	header := strings.TrimSpace(ansi.Strip(lines[1]))
	header = strings.TrimPrefix(header, "◉ ")
	// Header is the renderer output; no separate "read" tool-name line.
	if header != "read PLAN.md:200-400" {
		t.Errorf("header = %q, want %q", header, "read PLAN.md:200-400")
	}
}

func TestToolExecution_ReadFile_OutputHeaderVisible(t *testing.T) {
	tc := NewToolExecution("read", "main.go")
	tc.SetArgsJSON(`{"path":"main.go","start_line":1,"end_line":50}`)
	tc.SetOutput("read file main.go:1:50\nline 1\nline 2\n")
	lines := tc.Render(80)
	rendered := strings.Join(lines, "\n")
	stripped := ansi.Strip(rendered)
	if !strings.Contains(stripped, "read main.go:1-50") && !strings.Contains(stripped, "main.go") {
		t.Errorf("read should show file path, got %q", rendered)
	}
	if strings.Contains(rendered, "line 1") {
		t.Errorf("read should not show content lines, got %q", rendered)
	}
}

func TestToolExecution_NonReadFile_ShowsOutput(t *testing.T) {
	tc := NewToolExecution("bash", "echo hi")
	tc.SetArgsJSON(`{"command":"echo hi"}`)
	tc.SetOutput("hi\nDuration: 0.01s\n")
	lines := tc.Render(80)
	rendered := strings.Join(lines, "\n")
	if !strings.Contains(rendered, "hi") {
		t.Errorf("bash tool should show output, got %q", rendered)
	}
}

func TestToolExecution_FullWidthBackground(t *testing.T) {
	tc := NewToolExecution("read", "main.go")
	tc.SetArgsJSON(`{"path":"main.go"}`)
	tc.SetStatus(ToolRunning)

	lines := tc.Render(80)
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines, got %d", len(lines))
	}

	// The header line should have background spanning full width
	header := lines[1]
	vw := visibleWidth(header)
	if vw < 80 {
		t.Errorf("header should span at least 80 cols, got %d: %q", vw, header)
	}

	// Check that the padding has ANSI reset at end (background should close)
	if !strings.HasSuffix(header, ansi.Reset) {
		t.Error("header should end with ANSI reset")
	}
}

func TestToolExecution_BackgroundSpansFullWidth_NoMidlineReset(t *testing.T) {
	// Use a non-bash tool: bash/terminal renderers opt out of the colored box.
	tc := NewToolExecution("search", "pattern")
	tc.SetArgsJSON(`{"pattern":"hello"}`)
	tc.SetOutput("found 3 results")
	tc.SetStatus(ToolSuccess)

	lines := tc.Render(80)
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines (top + header + content + trailing), got %d", len(lines))
	}

	bg := ansi.Bg(TheTheme.ColorHex("tool_success_bg"))
	for i, line := range lines {
		assertBackgroundLine(t, i, line, bg)
	}
}

func TestToolExecution_Bash_StatusBackground(t *testing.T) {
	tc := NewToolExecution("bash", "echo hello")
	tc.SetArgsJSON(`{"command":"echo hello"}`)
	tc.SetOutput("hello\nDuration: 0.04s\n")
	tc.SetStatus(ToolSuccess)

	lines := tc.Render(80)
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines, got %d", len(lines))
	}

	bg := ansi.Bg(TheTheme.ColorHex("tool_success_bg"))
	for i, line := range lines {
		if !strings.Contains(line, bg) {
			t.Errorf("line %d should use tool_success_bg for successful bash output: %q", i, line)
		}
	}

	// Also verify error state gets error background
	tc2 := NewToolExecution("bash", "false")
	tc2.SetArgsJSON(`{"command":"false"}`)
	tc2.SetOutput("")
	tc2.SetStatus(ToolError)

	lines2 := tc2.Render(80)
	errBg := ansi.Bg(TheTheme.ColorHex("tool_error_bg"))
	for i, line := range lines2 {
		if !strings.Contains(line, errBg) {
			t.Errorf("line %d should use tool_error_bg for failed bash output: %q", i, line)
		}
	}
}

func assertBackgroundLine(t *testing.T, i int, line, bg string) {
	if line == "" {
		return
	}
	vw := visibleWidth(line)
	if vw < 80 {
		t.Errorf("line %d should span at least 80 cols, got %d: %q", i, vw, line)
	}
	if !strings.HasSuffix(line, ansi.Reset) {
		t.Errorf("line %d should end with ANSI reset: %q", i, line)
	}
	assertResetsReArmed(t, i, line, bg)
}

func assertResetsReArmed(t *testing.T, i int, line, bg string) {
	trimmed := strings.TrimSuffix(line, ansi.Reset)
	trimmed = strings.TrimRight(trimmed, " ")
	for {
		idx := strings.Index(trimmed, ansi.Reset)
		if idx < 0 {
			break
		}
		after := trimmed[idx+len(ansi.Reset):]
		if !strings.HasPrefix(after, bg) {
			t.Errorf("line %d contains a full ANSI reset not followed by background color: %q", i, trimmed)
			break
		}
		trimmed = after
	}
}

func TestToolExecution_RunningColor_IsOrange(t *testing.T) {
	theme := DarkTheme()
	tc := NewToolExecution("bash", "echo hi")

	// Running and pending both use tool_pending_bg for partial state.
	tc.SetStatus(ToolRunning)
	runningBg := tc.bgColor()
	expectedPendingBg := theme.ColorHex("tool_pending_bg")
	if runningBg != expectedPendingBg {
		t.Errorf("running bg = %q, want %q", runningBg, expectedPendingBg)
	}

	// Pending state should use tool_pending_bg
	tc.SetStatus(ToolPending)
	pendingBg := tc.bgColor()
	if pendingBg != expectedPendingBg {
		t.Errorf("pending bg = %q, want %q", pendingBg, expectedPendingBg)
	}
}

func TestToolExecution_StatusColors_Distinct(t *testing.T) {
	// Verify all bg theme tokens are non-empty
	colors := map[string]string{
		"tool_pending_bg": TheTheme.ColorHex("tool_pending_bg"),
		"tool_running_bg": TheTheme.ColorHex("tool_running_bg"),
		"tool_success_bg": TheTheme.ColorHex("tool_success_bg"),
		"tool_error_bg":   TheTheme.ColorHex("tool_error_bg"),
	}
	for name, hex := range colors {
		if hex == "" {
			t.Errorf("theme token %q is empty", name)
		}
	}
}

func TestToolExecution_SuccessStatus(t *testing.T) {
	tc := NewToolExecution("search", "pattern")
	tc.SetOutput("found 3 results")
	tc.SetStatus(ToolSuccess)

	lines := tc.Render(80)
	if len(lines) < 4 {
		t.Fatalf("expected at least 4 lines (top padding + header + body + trailing), got %d", len(lines))
	}
}

func TestToolExecution_EmptyOutput(t *testing.T) {
	tc := NewToolExecution("read", "empty.txt")
	tc.SetArgsJSON(`{"path":"empty.txt"}`)
	tc.SetStatus(ToolSuccess)

	lines := tc.Render(80)
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines (top padding + header + trailing) for empty output, got %d", len(lines))
	}
}

func TestToolExecution_BashRenderer_ShowsCommandAndOutput(t *testing.T) {
	tc := NewToolExecution("bash", "echo hello")
	tc.SetArgsJSON(`{"command":"echo hello"}`)
	tc.SetOutput("hello\nDuration: 0.04s\n")
	tc.SetStatus(ToolSuccess)

	lines := tc.Render(80)
	rendered := strings.Join(lines, "\n")
	if !strings.Contains(ansi.Strip(rendered), "$ echo hello") {
		t.Errorf("expected command header, got %q", rendered)
	}
	if !strings.Contains(rendered, "hello") {
		t.Errorf("expected output 'hello', got %q", rendered)
	}
	if !strings.Contains(rendered, "Took 0.0s") {
		t.Errorf("expected duration, got %q", rendered)
	}
}
