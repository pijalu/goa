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
		{"success", ToolSuccess, "tool_success_bg"},
		{"error", ToolError, "tool_error_bg"},
	}
	for _, st := range statuses {
		t.Run(st.name, func(t *testing.T) {
			tc := NewToolExecution("read", `{"path":"x.go"}`)
			tc.SetArgsComplete()
			tc.SetOutput("line one\nline two")
			tc.SetStatus(st.status)

			bg := ansi.Bg(TheTheme.ColorHex(st.bgKey))
			narrow := tc.Render(80)
			if len(narrow) == 0 {
				t.Fatal("no lines rendered at width 80")
			}
			bgLines := 0
			for i, line := range narrow {
				if got := visibleWidth(ansi.Strip(line)); got != 80 {
					t.Fatalf("narrow line %d visible width %d, want 80", i, got)
				}
				if strings.Contains(line, bg) {
					bgLines++
				}
			}
			if bgLines == 0 {
				t.Fatalf("no background-painted lines at width 80 (bg %q missing)", st.bgKey)
			}

			// Resize wider: every line must be rebuilt at the new width, with
			// the background still wrapping content + padding.
			wide := tc.Render(120)
			if len(wide) != len(narrow) {
				t.Fatalf("resize changed line count %d → %d", len(narrow), len(wide))
			}
			wideBg := 0
			for i, line := range wide {
				if got := visibleWidth(ansi.Strip(line)); got != 120 {
					t.Errorf("wide line %d visible width %d, want 120 (stale cached render from width 80?)", i, got)
				}
				if strings.Contains(line, bg) {
					wideBg++
				}
			}
			if wideBg != bgLines {
				t.Errorf("background-painted lines %d after resize, want %d (same box rows painted)", wideBg, bgLines)
			}

			// Resize narrower must also rebuild (no overflow past the width).
			small := tc.Render(60)
			for i, line := range small {
				if got := visibleWidth(ansi.Strip(line)); got > 60 {
					t.Errorf("narrowed line %d visible width %d exceeds 60 (stale cached render?)", i, got)
				}
			}
		})
	}
}
