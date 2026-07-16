// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/spinner"
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

func TestToolExecution_ReadFile_ExpandedShowsContent(t *testing.T) {
	tc := NewToolExecution("read", "README.md")
	tc.SetArgsJSON(`{"path":"README.md"}`)
	tc.SetOutput("read file README.md:1:2\n1 first\n2 second\n")
	tc.SetExpanded(true)
	rendered := strings.Join(tc.Render(80), "\n")
	stripped := ansi.Strip(rendered)
	// Expanded (Full): the file content is shown (pi parity), not just metadata.
	if !strings.Contains(stripped, "first") || !strings.Contains(stripped, "second") {
		t.Errorf("expanded read should show file content, got %q", rendered)
	}
	// The path still appears on the call line.
	if !strings.Contains(stripped, "README.md") {
		t.Errorf("expected file path, got %q", rendered)
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
}

// TestToolExecution_BashRenderer_NoDuplicateTook is the regression test for the
// duplicate-time bug: the bash renderer used to render its own "Took" line
// (parsed from the tool's Duration footer) in addition to the generic widget
// duration line, producing two "Took" rows. The widget duration line must be
// the single source of truth, and the Duration footer must be stripped from
// the body.
func TestToolExecution_BashRenderer_NoDuplicateTook(t *testing.T) {
	tc := NewToolExecution("bash", "go test ./tools/")
	tc.SetArgsJSON(`{"command":"go test ./tools/"}`)
	// Ensure the wall-clock duration crosses the visibility threshold so the
	// generic duration line is actually rendered.
	time.Sleep(20 * time.Millisecond)
	tc.SetOutput("ok  github.com/pijalu/goa/tools  0.507s\nDuration: 1.20s\n")
	tc.SetStatus(ToolSuccess)

	stripped := ansi.Strip(strings.Join(tc.Render(80), "\n"))
	if n := strings.Count(stripped, "Took"); n != 1 {
		t.Errorf("expected exactly one 'Took' line, got %d; rendered:\n%s", n, stripped)
	}
	if strings.Contains(stripped, "Duration:") {
		t.Errorf("Duration footer must be stripped from body; rendered:\n%s", stripped)
	}
}

// TestToolExecution_GenericRendererShowsNameAndArgs verifies that a tool
// WITHOUT a dedicated renderer (here "glob") no longer renders the uninformative
// literal "run tool". The generic renderer returns "", so ToolExecutionComponent
// falls back to "<toolName> <FormatToolArgs>" — e.g. "glob **/*.go".
func TestToolExecution_GenericRendererShowsNameAndArgs(t *testing.T) {
	tc := NewToolExecution("glob", "")
	tc.SetArgsJSON(`{"pattern":"**/*.go"}`)
	tc.SetStatus(ToolRunning)

	rendered := strings.Join(tc.Render(80), "\n")
	stripped := ansi.Strip(rendered)

	if strings.Contains(stripped, "run tool") {
		t.Errorf("generic tool should not show literal 'run tool'; got:\n%s", stripped)
	}
	if !strings.Contains(stripped, "glob") {
		t.Errorf("expected header to contain tool name 'glob'; got:\n%s", stripped)
	}
	if !strings.Contains(stripped, "**/*.go") {
		t.Errorf("expected header to contain formatted arg '**/*.go'; got:\n%s", stripped)
	}
}

func TestToolExecution_PartialArgs_ShowsPendingState(t *testing.T) {
	_, def := spinner.Default()
	SetSpinner(def)
	defer SetSpinner(spinner.Definition{})

	tc := NewToolExecution("write", "write path/to/file (streaming...)")

	// Tool should start in Pending state (not Running) with argsComplete=false.
	if tc.Status() != ToolPending {
		t.Errorf("expected ToolPending after creation with partial args, got %v", tc.Status())
	}
	if tc.argsComplete {
		t.Error("expected argsComplete=false for partial args")
	}

	// Set partial args incrementally (simulating streaming).
	tc.SetArgsPartial(`{"path":"test.go","content":"packa`)
	lines := tc.Render(80)
	rendered := strings.Join(lines, "\n")

	// Should show the tool header even with incomplete args.
	if !strings.Contains(rendered, "write") {
		t.Errorf("expected write header in render, got:\n%s", rendered)
	}
}

func TestToolExecution_PartialArgs_TransitionToComplete(t *testing.T) {
	tc := NewToolExecution("write", "write test.go")
	tc.SetArgsJSON(`{"path":"test.go","content":"package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n"}`)

	// Simulate streaming: initial partial, then complete.
	tc.SetArgsPartial(`{"path":"test.go","content":"package"}`)
	tc.SetArgsComplete()
	tc.SetStatus(ToolRunning)

	if tc.Status() != ToolRunning {
		t.Errorf("expected ToolRunning after args complete, got %v", tc.Status())
	}
	if !tc.argsComplete {
		t.Error("expected argsComplete=true after SetArgsComplete")
	}

	lines := tc.Render(80)
	rendered := strings.Join(lines, "\n")
	if !strings.Contains(rendered, "write") {
		t.Errorf("expected write header after transition, got:\n%s", rendered)
	}
}

func TestToolExecution_PartialArgs_RenderContextWiredCorrectly(t *testing.T) {
	tc := NewToolExecution("write", "write test.go")

	// Before completion: ArgsComplete should be false.
	tc.SetArgsPartial(`{"path":"test.go","content":"par"}`)
	tc.updateBox()

	// After completion: ArgsComplete should be true.
	tc.SetArgsComplete()
	tc.SetArgsJSON(`{"path":"test.go","content":"package main"}`)
}

// TestToolExecution_WriteStreaming_BodyShowsPartialContent verifies that while
// a write tool is streaming arguments, the partial content is rendered in the
// body so the user sees the file being written as it arrives.
func TestToolExecution_WriteStreaming_BodyShowsPartialContent(t *testing.T) {
	tc := NewToolExecution("write", "write test.go")
	tc.SetArgsPartial(`{"path":"test.go","content":"package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n"}`)

	lines := tc.Render(80)
	rendered := strings.Join(lines, "\n")
	stripped := ansi.Strip(rendered)
	if !strings.Contains(stripped, "package main") {
		t.Errorf("expected streamed content in body, got:\n%s", stripped)
	}
	if !strings.Contains(stripped, "println") {
		t.Errorf("expected streamed content to include println, got:\n%s", stripped)
	}
	if !strings.Contains(stripped, "write test.go") {
		t.Errorf("expected streaming header, got:\n%s", stripped)
	}
}

// TestToolExecution_RunningShowsElapsedDuration verifies that the tool widget
// renders an elapsed duration while the tool is still running/pending, and that
// the duration is only shown once it exceeds the 0.01s threshold.
func TestToolExecution_RunningShowsElapsedDuration(t *testing.T) {
	tc := NewToolExecution("bash", "sleep 1")
	tc.SetArgsJSON(`{"command":"sleep 1"}`)
	tc.SetStatus(ToolRunning)

	// Immediately after starting, the elapsed time is below the 0.01s
	// threshold, so no duration line should be rendered yet.
	lines := tc.Render(80)
	rendered := strings.Join(lines, "\n")
	stripped := ansi.Strip(rendered)
	if strings.Contains(stripped, "elapsed") {
		t.Errorf("expected duration hidden below threshold, got:\n%s", stripped)
	}

	// After waiting past the threshold, the elapsed duration should appear.
	time.Sleep(20 * time.Millisecond)
	lines = tc.Render(80)
	rendered = strings.Join(lines, "\n")
	stripped = ansi.Strip(rendered)
	if !strings.Contains(stripped, "elapsed") {
		t.Errorf("expected running tool to show elapsed duration, got:\n%s", stripped)
	}

	// After completion, the duration should switch to "Took ...".
	tc.SetOutput("done")
	tc.SetStatus(ToolSuccess)
	lines = tc.Render(80)
	rendered = strings.Join(lines, "\n")
	stripped = ansi.Strip(rendered)
	if !strings.Contains(stripped, "Took") {
		t.Errorf("expected completed tool to show final duration, got:\n%s", stripped)
	}
	if strings.Contains(stripped, "Took elapsed") {
		t.Errorf("duration label should not contain both prefixes, got:\n%s", stripped)
	}
}

// stubViewPolicy is a minimal ToolViewPolicy for testing the global Ctrl+O
// expand/collapse path without a full ChatViewport.
type stubViewPolicy struct {
	expanded     bool
	previewLines int
	showRead     bool
}

func (s stubViewPolicy) EffectiveToolsExpanded() bool { return s.expanded }
func (s stubViewPolicy) EffectivePreviewLines() int   { return s.previewLines }
func (s stubViewPolicy) ShowReadContent() bool        { return s.showRead }

// TestToolExecution_CtrlO_TogglesReadBody is the regression test for
// "Ctrl+O open/close does not change anything": flipping the global view
// policy (what Ctrl+O drives) must change a read block from header-only
// (Summary) to showing the file content (Full).
func TestToolExecution_CtrlO_TogglesReadBody(t *testing.T) {
	tc := NewToolExecution("read", "main.go")
	tc.SetArgsJSON(`{"path":"main.go"}`)
	tc.SetOutput("read file main.go:1:2\n     1  package main\n     2  \n(end — 2 lines shown)\n")

	// Collapsed (Summary): header only — no content.
	tc.SetToolViewPolicy(stubViewPolicy{expanded: false, previewLines: 10, showRead: true})
	collapsed := ansi.Strip(strings.Join(tc.Render(80), "\n"))
	if strings.Contains(collapsed, "package main") {
		t.Errorf("collapsed read should not show content, got %q", collapsed)
	}

	// Expand via the global policy (Ctrl+O): body now shows the file content.
	tc.SetToolViewPolicy(stubViewPolicy{expanded: true, previewLines: 10, showRead: true})
	expanded := ansi.Strip(strings.Join(tc.Render(80), "\n"))
	if !strings.Contains(expanded, "package main") {
		t.Errorf("expanded read should show content, got %q", expanded)
	}
}

// TestToolExecution_ReadFile_ShowReadFalsePreventsGlobalExpand verifies that
// when ShowReadContent() returns false, a read widget stays collapsed even when
// the global view policy says expanded=true. The per-widget toggle still works.
func TestToolExecution_ReadFile_ShowReadFalsePreventsGlobalExpand(t *testing.T) {
	tc := NewToolExecution("read", "main.go")
	tc.SetArgsJSON(`{"path":"main.go"}`)
	tc.SetOutput("read file main.go:1:2\n     1  package main\n     2  \n(end — 2 lines shown)\n")

	// Global expanded=true, but showRead=false → read stays collapsed.
	tc.SetToolViewPolicy(stubViewPolicy{expanded: true, previewLines: 10, showRead: false})
	rendered := ansi.Strip(strings.Join(tc.Render(80), "\n"))
	if strings.Contains(rendered, "package main") {
		t.Errorf("read should stay collapsed when showRead=false, got %q", rendered)
	}
	// The header (file path) should still be visible.
	if !strings.Contains(rendered, "main.go") {
		t.Errorf("read header should still show path, got %q", rendered)
	}

	// Per-widget toggle still works: SetExpanded(true) overrides the policy.
	tc.SetExpanded(true)
	expanded := ansi.Strip(strings.Join(tc.Render(80), "\n"))
	if !strings.Contains(expanded, "package main") {
		t.Errorf("per-widget toggle should show content, got %q", expanded)
	}
}

// TestToolExecution_CtrlO_TogglesWriteBody verifies the write block responds
// to the global expand policy: collapsed shows a 10-line preview + hint, full
// shows every line.
func TestToolExecution_CtrlO_TogglesWriteBody(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 15; i++ {
		fmt.Fprintf(&b, "L%02d\n", i)
	}
	tc := NewToolExecution("write", "out.txt")
	tc.SetArgs(map[string]any{"path": "out.txt", "content": b.String()})
	tc.SetPartial(true)

	tc.SetToolViewPolicy(stubViewPolicy{expanded: false, previewLines: 10})
	collapsed := ansi.Strip(strings.Join(tc.Render(80), "\n"))
	if strings.Contains(collapsed, "L15") {
		t.Errorf("collapsed write should hide L15, got %q", collapsed)
	}
	if !strings.Contains(collapsed, "more lines") {
		t.Errorf("collapsed write should show a 'more lines' hint, got %q", collapsed)
	}

	tc.SetToolViewPolicy(stubViewPolicy{expanded: true, previewLines: 10})
	expanded := ansi.Strip(strings.Join(tc.Render(80), "\n"))
	if !strings.Contains(expanded, "L15") {
		t.Errorf("expanded write should show L15, got %q", expanded)
	}
	if strings.Contains(expanded, "more lines") {
		t.Errorf("expanded write should not show a 'more lines' hint, got %q", expanded)
	}
}

// TestToolExecution_NoGenericStatsLine asserts the removed uniform
// "N lines in / M lines out" counter never renders for any tool.
func TestToolExecution_NoGenericStatsLine(t *testing.T) {
	for _, name := range []string{"read", "write", "bash"} {
		tc := NewToolExecution(name, "x")
		tc.SetArgs(map[string]any{"path": "x.go", "command": "ls", "content": "a\nb\nc\n"})
		tc.SetOutput("read file x:1:2\n1 a\n2 b\n(end — 2 lines shown)\n")
		tc.SetStatus(ToolSuccess)
		rendered := ansi.Strip(strings.Join(tc.Render(80), "\n"))
		if strings.Contains(rendered, "lines in") || strings.Contains(rendered, "lines out") {
			t.Errorf("%s block must not render the generic stats line, got %q", name, rendered)
		}
	}
}
