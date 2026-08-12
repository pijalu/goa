// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strconv"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/tui"
)

// TestUI_ReadWidgetAllRowsKeepStatusBg is the Bug D baseline: a read
// tool call rendered as the LAST chat entry (directly above the spinner)
// must flip ALL its rows to the success background — none may keep the
// pending background. Driven through the real app event flow and replayed
// through a TermEmulator that tracks per-cell background.
func TestUI_ReadWidgetAllRowsKeepStatusBg(t *testing.T) {
	sc := newUIScenario(t, 80, 24)

	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolCallID: "r1", ToolName: "read", ToolInput: `{"path":"x.go","start_line":1,"max_lines":100}`})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolStart, State: agentic.StateToolCall,
		ToolCallID: "r1", ToolName: "read"})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolResult, State: agentic.StateToolResult,
		ToolCallID: "r1", ToolName: "read", Text: "read file x.go:1:100\n[file: 100 lines]"})

	emu := tui.NewTermEmulator(24, 80)
	for _, w := range sc.term.writes {
		emu.Process(w)
	}

	pendingBg := bgParams(t, "tool_pending_bg")
	for r := 0; r < 24; r++ {
		for col, bg := range emu.VisibleBg(r) {
			if bg == pendingBg {
				t.Fatalf("row %d col %d kept the pending bg after success (row %q)",
					r, col, strings.TrimSpace(emu.Visible(r)))
			}
		}
	}
}

// bgParams converts a theme color to the SGR params the emulator tracks.
func bgParams(t *testing.T, key string) string {
	t.Helper()
	hex := tui.TheTheme.ColorHex(key)
	if hex == "" {
		t.Fatalf("theme has no %s", key)
	}
	r, g, b := ansi.HexToRGB(hex)
	return "48;2;" + strconv.Itoa(int(r)) + ";" + strconv.Itoa(int(g)) + ";" + strconv.Itoa(int(b))
}
