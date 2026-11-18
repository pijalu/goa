// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/tui"
)

func TestUIScenario_TruncatedNativeArgumentsRenderProtocolErrorWithoutExecution(t *testing.T) {
	sc := newUIScenario(t, 120, 12)
	const raw = `{"path":"test.txt","new_content":"unfinished`
	const result = "Error: [provider error: truncated_tool_arguments]\n" +
		"The provider did not supply one complete, valid JSON arguments object. The tool was not executed. " +
		"Reissue one complete native tool call with full JSON arguments."

	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolName: "edit", ToolCallID: "native-truncated", ToolInput: raw,
	})
	// The agent deliberately emits no EventToolStart: this call never reaches
	// the scheduler or a concrete edit implementation.
	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventToolResult, State: agentic.StateToolResult,
		ToolName: "edit", ToolCallID: "native-truncated", ToolResult: result, Text: result,
	})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventEnd})

	assertNativeArgumentFilmstrip(t, sc)
	widgets := toolWidgets(sc)
	if len(widgets) != 1 {
		t.Fatalf("tool widgets = %d, want one", len(widgets))
	}
	if widgets[0].Status() != tui.ToolError {
		t.Fatalf("tool widget status = %v, want terminal error", widgets[0].Status())
	}
	assertNativeArgumentTerminal(t, sc)
}

func assertNativeArgumentFilmstrip(t *testing.T, sc *uiScenario) {
	t.Helper()
	film := sc.filmstrip().Render()
	if !strings.Contains(film, "truncated_tool_arguments") || !strings.Contains(film, "not executed") {
		t.Fatalf("filmstrip missing actionable protocol diagnostic:\n%s", film)
	}
	if strings.Contains(film, "[edit error: invalid_input]") || strings.Contains(film, "Cannot parse parameters") {
		t.Fatalf("old generic edit failure leaked into filmstrip:\n%s", film)
	}
	for i, frame := range sc.filmstrip().Frames() {
		visible := strings.Join(frame.Frame.Visible, "\n")
		if strings.Contains(visible, "◉") {
			t.Fatalf("frame %d shows concrete execution although no start was emitted:\n%s", i, visible)
		}
	}
}

func assertNativeArgumentTerminal(t *testing.T, sc *uiScenario) {
	t.Helper()
	// Inspect actual terminal output too: visible cells plus native scrollback.
	emu := tui.NewTermEmulator(12, 120)
	for _, write := range sc.term.writes {
		emu.Process(write)
	}
	rows := append([]string{}, emu.Scrollback()...)
	for row := 0; row < 12; row++ {
		rows = append(rows, emu.Visible(row))
	}
	terminal := strings.Join(rows, "\n")
	if !strings.Contains(terminal, "truncated_tool_arguments") {
		t.Fatalf("terminal visible rows/scrollback missing protocol error:\n%s", terminal)
	}
	if strings.Contains(terminal, "[edit error: invalid_input]") {
		t.Fatalf("terminal contains old generic failure:\n%s", terminal)
	}
	t.Logf("validated filmstrip and terminal rows:\n%s", terminal)
}
