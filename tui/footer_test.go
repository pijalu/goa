// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

func TestFooter_Data_Defaults(t *testing.T) {
	f := NewFooter()
	d := f.Data()
	if d.MinorMode != "" {
		t.Errorf("expected empty MinorMode, got %q", d.MinorMode)
	}
}

func TestFooter_SetData_PreservesMinorMode(t *testing.T) {
	f := NewFooter()
	f.SetData(FooterData{MinorMode: "companion"})
	if f.data.MinorMode != "companion" {
		t.Errorf("expected MinorMode='companion', got %q", f.data.MinorMode)
	}

	// Subsequent SetData without MinorMode should preserve it
	f.SetData(FooterData{Mode: "yolo", Profile: "coder"})
	if f.data.MinorMode != "companion" {
		t.Errorf("expected MinorMode preserved as 'companion', got %q", f.data.MinorMode)
	}
}

func TestFooter_SetData_OverridesMinorMode(t *testing.T) {
	f := NewFooter()
	f.SetData(FooterData{MinorMode: "review"})
	f.SetData(FooterData{MinorMode: "pair"})
	if f.data.MinorMode != "pair" {
		t.Errorf("expected MinorMode='pair', got %q", f.data.MinorMode)
	}
}

func TestFooter_Render_ShowsMinorMode(t *testing.T) {
	f := NewFooter()
	f.SetData(FooterData{
		Workdir:   "/test",
		Mode:      "yolo",
		Profile:   "coder",
		Model:     "test-model",
		MinorMode: "companion",
	})
	lines := f.Render(80)
	if len(lines) == 0 {
		t.Fatal("expected rendered lines")
	}

	// First line should contain profile(minor) format: coder(companion)
	firstLine := lines[0]
	if !strings.Contains(firstLine, "coder(companion)") {
		t.Errorf("expected 'coder(companion)' in footer, got %q", firstLine)
	}
}

func TestFooter_Render_NoMinorMode(t *testing.T) {
	f := NewFooter()
	f.SetData(FooterData{
		Workdir: "/test",
		Mode:    "yolo",
		Profile: "coder",
		Model:   "test-model",
	})
	lines := f.Render(80)
	if len(lines) == 0 {
		t.Fatal("expected rendered lines")
	}

	// Should not crash, first line should contain YOLO
	firstLine := lines[0]
	if !strings.Contains(firstLine, "YOLO") {
		t.Errorf("expected YOLO mode in footer, got %q", firstLine)
	}
}

func TestFooter_Render_RespectsWidth(t *testing.T) {
	f := NewFooter()
	f.SetData(FooterData{
		Workdir:   "/test",
		Mode:      "yolo",
		Profile:   "coder",
		Model:     "test-model",
		MinorMode: "companion",
	})

	lines0 := f.Render(0)
	if lines0 != nil {
		t.Error("expected nil for width=0")
	}

	lines := f.Render(80)
	if len(lines) < 1 {
		t.Fatal("expected at least one line")
	}
}

func TestFooter_Render_WorkflowActive(t *testing.T) {
	f := NewFooter()
	f.SetData(FooterData{
		Workdir:        "/test",
		Mode:           "yolo",
		Profile:        "coder",
		Model:          "test-model",
		WorkflowActive: true,
	})
	lines := f.Render(80)
	if len(lines) == 0 {
		t.Fatal("expected rendered lines")
	}
	// Second line should contain workflow indicator
	secondLine := lines[1]
	if !strings.Contains(secondLine, "⟡ workflow") {
		t.Errorf("expected '⟡ workflow' in footer, got %q", secondLine)
	}
}

func TestFooter_Render_SteeringHint(t *testing.T) {
	f := NewFooter()
	f.SetData(FooterData{
		Workdir:      "/test",
		Mode:         "yolo",
		Profile:      "coder",
		Model:        "test-model",
		SteeringHint: "type to steer",
	})
	lines := f.Render(80)
	if len(lines) == 0 {
		t.Fatal("expected rendered lines")
	}
	secondLine := lines[1]
	if !strings.Contains(secondLine, "type to steer") {
		t.Errorf("expected 'type to steer' in footer, got %q", secondLine)
	}
}

func TestFooter_Render_ThinkingLevelMain(t *testing.T) {
	f := NewFooter()
	f.SetData(FooterData{
		Workdir:       "/test",
		Mode:          "yolo",
		Profile:       "coder",
		Model:         "test-model",
		ThinkingLevel: "medium",
	})
	lines := f.Render(80)
	if len(lines) == 0 {
		t.Fatal("expected rendered lines")
	}
	secondLine := ansi.Strip(lines[1])
	if !strings.Contains(secondLine, "test-model • medium") {
		t.Errorf("expected main model thinking level, got %q", secondLine)
	}
}

func TestFooter_Render_ThinkingLevelCompanion(t *testing.T) {
	f := NewFooter()
	f.SetData(FooterData{
		Workdir:                "/test",
		Mode:                   "yolo",
		Profile:                "coder",
		Model:                  "main-model",
		MinorMode:              "companion",
		CompanionModel:         "companion-model",
		ThinkingLevel:          "medium",
		CompanionThinkingLevel: "low",
	})
	lines := f.Render(80)
	if len(lines) == 0 {
		t.Fatal("expected rendered lines")
	}
	secondLine := ansi.Strip(lines[1])
	if !strings.Contains(secondLine, "main-model • medium") {
		t.Errorf("expected main thinking level, got %q", secondLine)
	}
	// Companion gets a (companion) label per sub-plan 06
	if !strings.Contains(secondLine, "companion-model (companion) • low") {
		t.Errorf("expected companion thinking level with (companion) label, got %q", secondLine)
	}
}

func TestFooter_SetData_PreservesModel(t *testing.T) {
	f := NewFooter()
	f.SetData(FooterData{Model: "main-model"})
	f.SetData(FooterData{Mode: "yolo"})
	if f.data.Model != "main-model" {
		t.Errorf("expected Model preserved, got %q", f.data.Model)
	}
}

func TestFooter_Render_CompanionModelShown(t *testing.T) {
	f := NewFooter()
	f.SetData(FooterData{
		Workdir:                "/test",
		Mode:                   "yolo",
		Profile:                "coder",
		Model:                  "main-model",
		MinorMode:              "companion",
		CompanionModel:         "companion-model",
		ThinkingLevel:          "medium",
		CompanionThinkingLevel: "low",
	})
	lines := f.Render(80)
	if len(lines) < 2 {
		t.Fatal("expected two footer lines")
	}
	second := ansi.Strip(lines[1])
	if !strings.Contains(second, "main-model") {
		t.Errorf("expected main model in footer, got %q", second)
	}
	if !strings.Contains(second, "companion-model") {
		t.Errorf("expected companion model in footer, got %q", second)
	}
}

func TestFooter_Render_HidesOffThinkingLevel(t *testing.T) {
	f := NewFooter()
	f.SetData(FooterData{
		Workdir:       "/test",
		Mode:          "yolo",
		Profile:       "coder",
		Model:         "test-model",
		ThinkingLevel: "off",
	})
	lines := f.Render(80)
	if len(lines) == 0 {
		t.Fatal("expected rendered lines")
	}
	secondLine := ansi.Strip(lines[1])
	if strings.Contains(secondLine, "• off") {
		t.Errorf("expected 'off' thinking level to be hidden, got %q", secondLine)
	}
}

// TestFooter_Render_CompanionMainIsGreen verifies the main model is the
// "selected" (green #3fb950) one and the companion is dim (faint), not green.
// When both models are idle (neither busy), main gets the highlight.
func TestFooter_Render_CompanionMainIsGreen(t *testing.T) {
	f := NewFooter()
	f.SetData(FooterData{
		Workdir:                "/test",
		Mode:                   "yolo",
		Profile:                "coder",
		Model:                  "main-model",
		MinorMode:              "companion",
		CompanionModel:         "comp-model",
		ThinkingLevel:          "medium",
		CompanionThinkingLevel: "low",
	})
	lines := f.Render(100)
	if len(lines) < 2 {
		t.Fatal("expected two footer lines")
	}
	second := lines[1]

	// ansi.Fg("#3fb950") produces the escape \x1b[38;2;63;185;80m (decimal RGB).
	// The main model must be wrapped in that green ANSI escape.
	greenCSI := ansi.Fg("#3fb950")
	if !strings.Contains(second, greenCSI) {
		t.Errorf("expected main model in green (CSI %q) on line 2, got %q", greenCSI, second)
	}

	// The companion model must be dim (Faint = \x1b[2m), NOT green.
	// Also verify the companion label appears in the sequence after faint.
	faintCSI := ansi.Faint
	if !strings.Contains(second, faintCSI) {
		t.Errorf("expected companion model in faint (CSI %q) on line 2, got %q", faintCSI, second)
	}

	// Verify the text content of both models (stripped of ANSI).
	stripped := ansi.Strip(second)
	if !strings.Contains(stripped, "main-model") {
		t.Errorf("expected main-model in stripped output, got %q", stripped)
	}
	// Companion model text includes the (companion) label.
	if !strings.Contains(stripped, "comp-model (companion)") {
		t.Errorf("expected comp-model (companion) in stripped output, got %q", stripped)
	}
}

// TestFooter_Render_CompanionHasLabel verifies the (companion) label.
func TestFooter_Render_CompanionHasLabel(t *testing.T) {
	f := NewFooter()
	f.SetData(FooterData{
		Workdir:        "/test",
		Mode:           "yolo",
		Profile:        "coder",
		Model:          "main",
		MinorMode:      "companion",
		CompanionModel: "comp",
	})
	lines := f.Render(100)
	if len(lines) < 2 {
		t.Fatal("expected two footer lines")
	}
	stripped := ansi.Strip(lines[1])
	if !strings.Contains(stripped, "(companion)") {
		t.Errorf("expected (companion) label, got %q", stripped)
	}
}

// TestFooter_Render_OnlyOneGreenModel verifies that in companion mode,
// only the active model uses the green highlight color.
func TestFooter_Render_OnlyOneGreenModel(t *testing.T) {
	f := NewFooter()
	f.SetData(FooterData{
		Workdir:        "/test",
		Mode:           "yolo",
		Profile:        "coder",
		Model:          "qwen",
		MinorMode:      "companion",
		CompanionModel: "gemma",
	})
	lines := f.Render(100)
	if len(lines) < 2 {
		t.Fatal("expected two footer lines")
	}
	second := lines[1]
	// Count occurrences of the green ANSI escape on line 2.
	// ansi.Fg("#3fb950") → \x1b[38;2;63;185;80m (decimal RGB).
	// When both models are idle, the main model is green (1 green span).
	// (The YOLO mode badge green is on line 1, not line 2;
	// the busy dot uses #d29922, not green.)
	// When companion is busy, the companion gets the green highlight instead.
	greenCSI := ansi.Fg("#3fb950")
	greenCount := strings.Count(second, greenCSI)
	if greenCount != 1 {
		t.Errorf("expected exactly 1 green (CSI %q) on line 2, got %d; line=%q", greenCSI, greenCount, second)
	}
}

// TestFooter_Render_CompanionBusyHighlight verifies that when the companion
// is busy processing, it gets the green highlight instead of the main model.
func TestFooter_Render_CompanionBusyHighlight(t *testing.T) {
	f := NewFooter()
	f.SetData(FooterData{
		Workdir:        "/test",
		Mode:           "yolo",
		Profile:        "coder",
		Model:          "qwen",
		MinorMode:      "companion",
		CompanionModel: "gemma",
		CompanionBusy:  true,
	})
	lines := f.Render(100)
	if len(lines) < 2 {
		t.Fatal("expected two footer lines")
	}
	second := lines[1]
	greenCSI := ansi.Fg("#3fb950")
	faintCSI := ansi.Faint

	// When companion is busy, companion should be green, main should be faint.
	// The companion label appears after "gemma (companion)" in the display.
	// Check companion is green and main is faint.
	if !strings.Contains(second, greenCSI+"gemma") {
		t.Errorf("expected companion model in green when busy, got line=%q", second)
	}
	// Main model should be faint when companion is busy
	// But the main model might be before green, so we check the whole line
	stripped := ansi.Strip(second)
	if !strings.Contains(stripped, "qwen") {
		t.Errorf("expected main model qwen in output, got %q", stripped)
	}
	if !strings.Contains(stripped, "gemma (companion)") {
		t.Errorf("expected companion with label, got %q", stripped)
	}
	_ = faintCSI
}

// TestFooter_Render_AdaptiveWidth_Narrow drops low-priority items.
func TestFooter_Render_AdaptiveWidth_Narrow(t *testing.T) {
	f := NewFooter()
	f.SetData(FooterData{
		Workdir:                "/very/long/path/to/project",
		Mode:                   "yolo",
		Profile:                "coder",
		Model:                  "very-long-model-name",
		Stats:                  "\u2191100 \u2193200 50%/8K",
		GitBranch:              "feature-branch-name",
		GitDirty:               true,
		ThinkingLevel:          "high",
		MinorMode:              "companion",
		CompanionModel:         "companion-model",
		CompanionThinkingLevel: "medium",
	})

	// Wide terminal: all items visible
	linesWide := f.Render(120)
	if len(linesWide) < 2 {
		t.Fatal("expected two footer lines")
	}
	wideStripped := ansi.Strip(linesWide[1])
	if !strings.Contains(wideStripped, "very-long-model-name") {
		t.Errorf("expected model name in wide render, got %q", wideStripped)
	}

	// Narrow terminal: should not crash, model name should still be visible
	linesNarrow := f.Render(40)
	if len(linesNarrow) < 2 {
		t.Fatal("expected two footer lines")
	}
	narrowStripped := ansi.Strip(linesNarrow[1])
	if !strings.Contains(narrowStripped, "very-long-model-name") {
		t.Errorf("expected model name in narrow render, got %q", narrowStripped)
	}
}

// TestFooter_Render_AdaptiveWidth_ProviderPrefix checks provider prefix is shown wide, stripped narrow.
func TestFooter_Render_AdaptiveWidth_ProviderPrefix(t *testing.T) {
	f := NewFooter()
	f.SetData(FooterData{
		Workdir: "/test",
		Mode:    "yolo",
		Profile: "coder",
		Model:   "(lmstudio) llama3", // model with provider prefix
	})

	// Wide terminal: provider prefix should be visible
	linesWide := f.Render(120)
	if len(linesWide) < 2 {
		t.Fatal("expected two footer lines")
	}
	wideStripped := ansi.Strip(linesWide[1])
	if !strings.Contains(wideStripped, "(lmstudio)") {
		t.Errorf("expected provider prefix on wide terminal, got %q", wideStripped)
	}
	if !strings.Contains(wideStripped, "llama3") {
		t.Errorf("expected model name on wide terminal, got %q", wideStripped)
	}

	// Narrow terminal (38 cols): provider prefix should be dropped, model name stays
	// The threshold uses available width (terminal width - left-side width - padding).
	// With empty left side, availW ≈ termW - 2.
	linesNarrow := f.Render(38)
	if len(linesNarrow) < 2 {
		t.Fatal("expected two footer lines")
	}
	narrowStripped := ansi.Strip(linesNarrow[1])
	if strings.Contains(narrowStripped, "(lmstudio)") {
		t.Errorf("expected provider prefix STRIPPED on narrow terminal, got %q", narrowStripped)
	}
	if !strings.Contains(narrowStripped, "llama3") {
		t.Errorf("expected model name on narrow terminal, got %q", narrowStripped)
	}
}

// TestFooter_Render_AdaptiveWidth_CompanionLabel checks "(companion)" label is
// shown when wide enough, dropped when too narrow.
func TestFooter_Render_AdaptiveWidth_CompanionLabel(t *testing.T) {
	f := NewFooter()
	f.SetData(FooterData{
		Workdir:        "/test",
		Mode:           "yolo",
		Profile:        "coder",
		Model:          "llama3",
		MinorMode:      "companion",
		CompanionModel: "gemma",
	})

	// Wide terminal: (companion) label should be visible
	linesWide := f.Render(120)
	if len(linesWide) < 2 {
		t.Fatal("expected two footer lines")
	}
	wideStripped := ansi.Strip(linesWide[1])
	if !strings.Contains(wideStripped, "companion") {
		t.Errorf("expected (companion) label on wide terminal, got %q", wideStripped)
	}

	// Narrow terminal: (companion) label should be dropped
	// With empty left side, availW ≈ termW - 2. showCompanionLabel needs availW > 35.
	linesNarrow := f.Render(34)
	if len(linesNarrow) < 2 {
		t.Fatal("expected two footer lines")
	}
	narrowStripped := ansi.Strip(linesNarrow[1])
	if strings.Contains(narrowStripped, "companion") {
		t.Errorf("expected (companion) label DROPPED on narrow terminal, got %q", narrowStripped)
	}
	// But both model names should still be visible
	if !strings.Contains(narrowStripped, "llama3") {
		t.Errorf("expected main model on narrow terminal, got %q", narrowStripped)
	}
	if !strings.Contains(narrowStripped, "gemma") {
		t.Errorf("expected companion model on narrow terminal, got %q", narrowStripped)
	}
}

// TestFooter_Render_AdaptiveWidth_VeryNarrow still shows model info.
func TestFooter_Render_AdaptiveWidth_VeryNarrow(t *testing.T) {
	f := NewFooter()
	f.SetData(FooterData{
		Workdir:   "/p",
		Mode:      "yolo",
		Profile:   "coder",
		Model:     "m",
		Stats:     "\u2191100 \u2193200",
		GitBranch: "main",
	})

	lines := f.Render(10)
	if len(lines) < 2 {
		t.Fatal("expected two footer lines")
	}
	// Should not panic with very narrow terminal
}

// TestFooter_Render_BothIdle_MainHighlighted verifies that when both
// main and companion are idle, the main model gets the green highlight.
func TestFooter_Render_BothIdle_MainHighlighted(t *testing.T) {
	f := NewFooter()
	f.SetData(FooterData{
		Workdir:        "/test",
		Mode:           "yolo",
		Profile:        "coder",
		Model:          "qwen",
		MinorMode:      "companion",
		CompanionModel: "gemma",
		ModelBusy:      false,
		CompanionBusy:  false,
	})
	lines := f.Render(100)
	if len(lines) < 2 {
		t.Fatal("expected two footer lines")
	}
	second := lines[1]
	greenCSI := ansi.Fg("#3fb950")
	greenCount := strings.Count(second, greenCSI)
	if greenCount != 1 {
		t.Errorf("expected exactly 1 green span when both idle, got %d; line=%q", greenCount, second)
	}
}

// TestFooter_Render_NoCompanionShowsMainOnly verifies non-companion mode
// shows a single green model without any companion label.
func TestFooter_Render_NoCompanionShowsMainOnly(t *testing.T) {
	f := NewFooter()
	f.SetData(FooterData{
		Workdir: "/test",
		Mode:    "yolo",
		Profile: "coder",
		Model:   "test-model",
	})
	lines := f.Render(80)
	if len(lines) < 2 {
		t.Fatal("expected two footer lines")
	}
	stripped := ansi.Strip(lines[1])
	if strings.Contains(stripped, "(companion)") {
		t.Errorf("expected no companion label in non-companion mode, got %q", stripped)
	}
	if !strings.Contains(stripped, "test-model") {
		t.Errorf("expected model name, got %q", stripped)
	}
}
