// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"
)

// TestCompletionEcho_BashContent pins the echo contract for a renderer
// WITHOUT a ResultSummarizer: the echo is the widget's final header (icon +
// call identity) plus output stats — never raw output lines, never the
// collapsed-view truncation hint. This is the regression test for the
// reported corruption where a scrolled-off tool's result was echoed as
// "← ✓ … N earlier lines (ctrl+o to expand)" + duplicated output lines.
func TestCompletionEcho_BashContent(t *testing.T) {
	swapToolRenderer(t, "bash", echoTestBashRenderer{})

	tc := NewToolExecution("bash", FormatToolArgs("bash", `{"command":"grep -n foo bar.go"}`))
	tc.SetArgs(map[string]any{"command": "grep -n foo bar.go"})
	tc.SetArgsComplete()
	tc.SetStatus(ToolRunning)
	tc.SetOutput("l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nl10\nl11\nl12")
	tc.SetStatus(ToolSuccess)
	tc.SetPartial(false)

	echo := tc.CompletionEcho()
	if strings.Contains(echo, "\n") {
		t.Errorf("echo must be a single line, got %q", echo)
	}
	if strings.Contains(echo, "ctrl+o") || strings.Contains(echo, "earlier lines") {
		t.Errorf("echo leaked the collapsed-view truncation hint: %q", echo)
	}
	if strings.Contains(echo, "l1") || strings.Contains(echo, "l12") {
		t.Errorf("echo replayed raw output lines: %q", echo)
	}
	if !strings.HasPrefix(echo, "✓ $ grep -n foo bar.go") {
		t.Errorf("echo must start with icon + call header, got %q", echo)
	}
	if !strings.Contains(echo, "12 lines") {
		t.Errorf("echo should carry output stats, got %q", echo)
	}
}

// swapToolRenderer replaces the registered renderer for name with r for the
// duration of the test, restoring the previous entry afterwards (the
// registry is process-global; deleting would orphan other tests).
func swapToolRenderer(t *testing.T, name string, r ToolRenderer) {
	t.Helper()
	prev, ok := ToolRendererRegistry[name]
	ToolRendererRegistry[name] = r
	t.Cleanup(func() {
		if ok {
			ToolRendererRegistry[name] = prev
		} else {
			delete(ToolRendererRegistry, name)
		}
	})
}

// TestCompletionEcho_Summarizer verifies that a renderer implementing
// ResultSummarizer contributes its one-line outcome between the header and
// the stats.
func TestCompletionEcho_Summarizer(t *testing.T) {
	swapToolRenderer(t, "goal", echoTestGoalRenderer{})

	tc := NewToolExecution("goal", FormatToolArgs("goal", `{"action":"cancel","goalId":"minty.puma"}`))
	tc.SetArgs(map[string]any{"action": "cancel", "goalId": "minty.puma"})
	tc.SetArgsComplete()
	tc.SetOutput(`{"cancelled":{"name":"minty.puma","objective":"G05 — do the thing"}}`)
	tc.SetStatus(ToolSuccess)
	tc.SetPartial(false)

	echo := tc.CompletionEcho()
	if !strings.HasPrefix(echo, "✓ ") {
		t.Errorf("echo must start with the success icon, got %q", echo)
	}
	if !strings.Contains(echo, "Cancelled minty.puma: G05") {
		t.Errorf("echo should carry the renderer's one-line summary, got %q", echo)
	}
	if strings.Contains(echo, "\n") {
		t.Errorf("echo must stay one line even with a summary, got %q", echo)
	}
}

// TestCompletionEcho_ErrorIcon: a failed tool echoes the ✗ header.
func TestCompletionEcho_ErrorIcon(t *testing.T) {
	tc := NewToolExecution("bash", FormatToolArgs("bash", `{"command":"false"}`))
	tc.SetArgs(map[string]any{"command": "false"})
	tc.SetArgsComplete()
	tc.SetOutput("Error: boom")
	tc.SetStatus(ToolError)
	tc.SetPartial(false)

	echo := tc.CompletionEcho()
	if !strings.HasPrefix(echo, "✗ ") {
		t.Errorf("error echo must start with ✗, got %q", echo)
	}
}

// TestCompletionEcho_AgentLabel: the echo inherits the agent attribution
// prefix so team-mode completions identify their owner.
func TestCompletionEcho_AgentLabel(t *testing.T) {
	tc := NewToolExecution("bash", FormatToolArgs("bash", `{"command":"ls"}`))
	tc.SetArgs(map[string]any{"command": "ls"})
	tc.SetArgsComplete()
	tc.SetAgentLabel("coder")
	tc.SetOutput("ok")
	tc.SetStatus(ToolSuccess)
	tc.SetPartial(false)

	echo := tc.CompletionEcho()
	if !strings.Contains(echo, "[coder]") {
		t.Errorf("echo must carry the agent label, got %q", echo)
	}
}

// ── test renderers ──

// echoTestBashRenderer mimics the real bash renderer: no ResultSummarizer,
// collapsed body starts with the truncation hint.
type echoTestBashRenderer struct{}

func (echoTestBashRenderer) RenderCall(args map[string]any, _ RenderContext) string {
	cmd, _ := args["command"].(string)
	return "$ " + cmd
}
func (echoTestBashRenderer) RenderResult(output string, _ RenderContext) string {
	lines := strings.Split(output, "\n")
	if len(lines) > 5 {
		return "… " + "7 earlier lines (ctrl+o to expand)\n" + strings.Join(lines[len(lines)-5:], "\n")
	}
	return output
}
func (echoTestBashRenderer) PreviewLines() int             { return 5 }
func (echoTestBashRenderer) HideResultWhenCollapsed() bool { return false }

// echoTestGoalRenderer implements ResultSummarizer.
type echoTestGoalRenderer struct{}

func (echoTestGoalRenderer) RenderCall(_ map[string]any, _ RenderContext) string {
	return "◆ Cancelled goal minty.puma"
}
func (echoTestGoalRenderer) RenderResult(output string, _ RenderContext) string {
	return "Cancelled minty.puma: G05 — do the thing"
}
func (echoTestGoalRenderer) SummarizeResult(output string, _ RenderContext) string {
	return "Cancelled minty.puma: G05 — do the thing"
}
func (echoTestGoalRenderer) PreviewLines() int             { return 3 }
func (echoTestGoalRenderer) HideResultWhenCollapsed() bool { return false }
