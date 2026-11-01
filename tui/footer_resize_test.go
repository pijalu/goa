// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/pijalu/goa/internal/ansi"
)

// newB45Footer builds a footer loaded the way the configured modeline layout
// (the "recent template change") drives it: mode line on row 1, model row on
// row 2, plugin segments, steering hint, and a full B11 stat ladder.
func newB45Footer() *Footer {
	f := NewFooter()
	f.SetModeLine([]string{"app-badge", "current-file"}, []string{"mode-badge", "token-bar", "activity", "help-hint"})
	f.SetData(FooterData{
		Workdir:       "/home/user/dev/goa",
		GitBranch:     "main",
		Mode:          "yolo",
		Profile:       "coding-posture",
		Model:         "(opencode) deepseek-v4.1-exp",
		Provider:      "opencode",
		ThinkingLevel: "high",
		Stats:         "↑169k ↓209k 35.8%/1.0M",
		StatsSegments: []FooterSegment{
			{Tier: FooterTierAvgCH, Text: "CH:35.8%"},
			{Tier: FooterTierLastCH, Text: "▸35.8%"},
			{Tier: FooterTierTokens, Text: "↑169k ↓209k"},
			{Tier: FooterTierQuota, Text: "35.8%/1.0M"},
		},
		Activity:     "thinking",
		MainActivity: "thinking",
		ModelBusy:    true,
		SteeringHint: "type to steer",
		GoalStatus:       "active",
		GoalCount:        2,
		GoalPendingTodos: 5,
		PluginSegments: []PluginSegment{
			{ID: "quota", Priority: 0, Text: ansi.Fg("#56d4dd") + "QC:42%" + ansi.Reset},
			{ID: "clock", Priority: 1, Text: ansi.Faint + "12:04" + ansi.Reset},
		},
	})
	return f
}

// B45 defect 1: routine footer rebuilds (companion stream ticks, orchestration
// tab stats) construct a fresh FooterData carrying only the field they own;
// Mode and Profile had no preservation, so the modeline's mode badge blanked
// out mid-stream and stayed blank.
func TestFooter_SetData_PreservesModeAndProfileBadge(t *testing.T) {
	f := NewFooter()
	f.SetModeLine([]string{"app-badge"}, []string{"mode-badge"})
	f.SetData(FooterData{Workdir: "/test", Mode: "yolo", Profile: "coding-posture", Stats: "↑1k ↓2k 50%"})
	if line := ansi.Strip(f.Render(120)[0]); !strings.Contains(line, "YOLO") || !strings.Contains(line, "coding-posture") {
		t.Fatalf("badge missing after full SetData: %q", line)
	}

	// Partial rebuild in the shape of Footer.SetData(FooterData{CompanionActivity: "reviewing"}).
	f.SetData(FooterData{CompanionActivity: "reviewing"})
	if line := ansi.Strip(f.Render(120)[0]); !strings.Contains(line, "YOLO") || !strings.Contains(line, "coding-posture") {
		t.Fatalf("mode badge wiped by partial SetData: %q", line)
	}

	// A rebuild that does know the mode must still override it.
	f.SetData(FooterData{Mode: "review"})
	if line := ansi.Strip(f.Render(120)[0]); !strings.Contains(line, "REVIEW") {
		t.Fatalf("explicit mode change lost: %q", line)
	}
}

// B45 defect 2: stripPluginSegments located the cut on the ANSI-stripped text
// but sliced the raw styled string, so the cut could land inside a truecolor
// escape and dangle a partial CSI sequence into the status bar.
func TestFooterStripPluginSegments_CutsAtCleanStyleBoundary(t *testing.T) {
	f := NewFooter()
	segText := ansi.Fg("#56d4dd") + "QC:42%" + ansi.Reset
	f.data.PluginSegments = []PluginSegment{{ID: "quota", Priority: 0, Text: segText}}

	fg := ansi.Fg("#8b949e")
	right := FormatModelPart("llama3", "", "", false, true, schema.PeakOff) + fg + " • " + ansi.Reset + segText

	got := f.stripPluginSegments(right)
	if vis := ansi.Strip(got); vis != "llama3" {
		t.Fatalf("visible remainder = %q, want %q (raw %q)", vis, "llama3", got)
	}
	if strings.Contains(ansi.Strip(got), "\x1b") {
		t.Fatalf("dangling partial escape survived: %q", got)
	}
}

// B45 defect 3: the steering hint sat on the left of the model row outside
// the width ladder (nil left segments), so under width pressure the model
// identity was ellipsized while a hint kept its columns.
func TestFooterConfiguredFooter_SteeringHintDropsBeforeModelIdentity(t *testing.T) {
	f := NewFooter()
	f.SetModeLine([]string{"mode-badge"}, []string{"mode-badge"})
	f.SetData(FooterData{
		Mode:          "yolo",
		Profile:       "cp",
		Model:         "(opencode) deepseek-v4.1-exp",
		ThinkingLevel: "high",
		MainActivity:  "thinking",
		ModelBusy:     true,
		SteeringHint:  "type to steer",
	})
	line2 := ansi.Strip(f.Render(24)[1])
	if strings.Contains(line2, "type to steer") {
		t.Fatalf("steering hint kept while the model row was squeezed: %q", line2)
	}
	if !strings.Contains(line2, "deepseek-v4.1-exp") {
		t.Fatalf("model identity lost at width 24: %q", line2)
	}
}

// B45 defect 4: resize invariant — at every terminal width the configured
// footer renders each line inside the viewport and never leaves a dangling
// partial escape sequence (mid-ANSI cuts show up as ESC bytes in the strip).
func TestFooterRender_WidthSweep_NoOverflowNoDanglingEscape(t *testing.T) {
	f := newB45Footer()
	for w := 1; w <= 200; w++ {
		lines := f.Render(w)
		if len(lines) == 0 {
			t.Fatalf("width %d: no lines rendered", w)
		}
		for i, line := range lines {
			if vw := visibleWidth(line); vw > w {
				t.Fatalf("width %d: line %d overflows: %d > %d (%q)", w, i, vw, w, ansi.Strip(line))
			}
			if stripped := ansi.Strip(line); strings.Contains(stripped, "\x1b") {
				t.Fatalf("width %d: line %d carries dangling escape bytes: %q", w, i, stripped)
			}
		}
	}
}

// B45 degradation order: as the modeline loses room, the help hint goes
// first, then the activity word; the mode badge and the quota figure are the
// last survivors.
func TestFooterModeline_DegradesInLadderOrder(t *testing.T) {
	helpText := ansi.Strip(modeLineHelpHint)

	assertRow := func(t *testing.T, w int, wantHelp, wantActivity bool) {
		t.Helper()
		f := newB45Footer()
		row := ansi.Strip(f.Render(w)[0])
		if strings.Contains(row, helpText) != wantHelp {
			t.Fatalf("width %d: help hint presence = %v, want %v (row %q)", w, !wantHelp, wantHelp, row)
		}
		if strings.Contains(row, "thinking") != wantActivity {
			t.Fatalf("width %d: activity presence = %v, want %v (row %q)", w, !wantActivity, wantActivity, row)
		}
	}

	assertRow(t, 120, true, true)  // everything fits
	assertRow(t, 110, false, true) // help hint dropped first, tokens/activity kept
	assertRow(t, 90, false, false) // activity dropped next; the quota figure survives
	row := ansi.Strip(newB45Footer().Render(90)[0])
	if !strings.Contains(row, "35.8%/1.0M") || !strings.Contains(row, "YOLO") {
		t.Fatalf("quota/mode badge must be the last survivors (width 90): %q", row)
	}
}
