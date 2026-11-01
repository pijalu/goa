// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/ansi"
)

func (w *wizardComponent) renderWelcome(width int) []string {
	return []string{
		"",
		ansi.Fg("#3fb950") + ansi.Bold + "  \u27e1 Goa -- goa coding agent" + ansi.Reset,
		"",
		"  Welcome! Let's get you set up.",
		"",
		"  We'll configure:",
		"    1. An LLM provider (required)",
		"    2. A model for the provider",
		"",
		"  Everything else uses the built-in defaults:",
		"    coding-posture mode · yolo autonomy · thinking level high",
		"",
		"  Use /profile at any time to switch persona.",
		"",
		ansi.Faint + "  [Enter] Start setup  [Esc] Skip (use defaults)  [Ctrl+C] Quit" + ansi.Reset,
	}
}

// wizardTotalSteps is the number of user-facing wizard steps so the progress
// indicator stays accurate.
const wizardTotalSteps = 2

// stepForState returns the logical step number for the current wizard screen.
// Internal substates that may be skipped (e.g., endpoint/key entry for presets
// that do not need them) are collapsed into the same step as their parent group
// so the progress indicator never jumps backwards or skips numbers.
func (w *wizardComponent) stepForState(st wizardState) int {
	switch st {
	case stateProviderType, stateProviderEndpoint, stateProviderKey, stateProviderTest:
		return 1
	case stateModelSelect, stateModelSetup, stateModelAdvanced, stateDone:
		return 2
	}
	return 1
}

// renderHeader returns a consistent step header line. total should match the
// number of user-facing wizard steps so the progress indicator is accurate.
func (w *wizardComponent) renderHeader(title string, step, total int) []string {
	progress := fmt.Sprintf("Step %d/%d", step, total)
	return []string{
		"",
		ansi.Bold + ansi.Fg("#58a6ff") + "  " + title + ansi.Reset +
			strings.Repeat(" ", 2) +
			ansi.Faint + progress + ansi.Reset,
		"",
	}
}

// visibleProviderPresets is how many provider presets the wizard list shows at
// once. The catalog keeps growing (every models.dev provider with a base URL is
// a preset), so the list is windowed around the selection — the same idiom
// renderModelSelect uses — instead of rendering every row: a screen taller than
// the terminal scrolls its own header out of view.
const visibleProviderPresets = 12

func (w *wizardComponent) renderProviderType(width int) []string {
	presets := PresetProviders()
	s := w.currentSlot()
	var lines []string
	lines = append(lines, w.renderHeader("LLM Provider", w.stepForState(w.state), wizardTotalSteps)...)
	lines = append(lines, "  Choose your LLM provider:")
	lines = append(lines, "")
	start, end := providerListWindow(len(presets), s.selectedPresetIndex)
	if start > 0 {
		lines = append(lines, ansi.Faint+fmt.Sprintf("  (%d more ↑)", start)+ansi.Reset)
	}
	for i := start; i < end; i++ {
		p := presets[i]
		marker := " "
		disp := strings.TrimPrefix(p.Endpoint, "https://")
		disp = strings.TrimPrefix(disp, "http://")
		meta := " (no key needed)"
		if p.NeedsAPIKey {
			meta = " (requires API key)"
		}
		line := fmt.Sprintf("  %s %d) %-16s -- %s%s", marker, i+1, p.Name, disp, meta)
		if i == s.selectedPresetIndex {
			line = ansi.Bold + ansi.Fg("#58a6ff") + ">" + line[1:] + ansi.Reset
		}
		lines = append(lines, line)
	}
	if end < len(presets) {
		lines = append(lines, ansi.Faint+fmt.Sprintf("  (%d more ↓)", len(presets)-end)+ansi.Reset)
	}
	customKey := len(presets) + 1
	marker := " "
	line := fmt.Sprintf("  %s %d) %-16s -- any OpenAI-compatible endpoint", marker, customKey, "Custom")
	if s.selectedPresetIndex == -1 {
		line = ansi.Bold + ansi.Fg("#58a6ff") + ">" + line[1:] + ansi.Reset
	}
	lines = append(lines, "")
	lines = append(lines, line)
	lines = append(lines, "")
	lines = append(lines, ansi.Faint+"  [Up/Down] Navigate  [Enter] Select  [Esc] Back  [1-9] Quick pick"+ansi.Reset)
	return lines
}

// providerListWindow returns the [start,end) preset range to render so the
// selected entry stays visible while the list fits the terminal. A selection of
// -1 means the Custom row (rendered after the list) is selected, so the window
// shows the tail of the list and stays adjacent to it.
func providerListWindow(total, selected int) (int, int) {
	if total <= visibleProviderPresets {
		return 0, total
	}
	if selected < 0 {
		return total - visibleProviderPresets, total
	}
	start := selected - visibleProviderPresets/2
	if start < 0 {
		start = 0
	}
	if start+visibleProviderPresets > total {
		start = total - visibleProviderPresets
	}
	return start, start + visibleProviderPresets
}

func (w *wizardComponent) renderProviderEndpoint(width int) []string {
	var lines []string
	lines = append(lines, w.renderHeader("Provider Endpoint", w.stepForState(w.state), wizardTotalSteps)...)
	if w.inputMode != "endpoint" {
		lines = append(lines, "  Enter the API endpoint URL:", "  (enter in the input line at the bottom)")
	} else {
		disp := ansi.Faint + "(type here)" + ansi.Reset
		if w.editor.Text() != "" {
			disp = ansi.RenderWithCursor(w.editor.Text(), w.editor.Cursor())
		}
		lines = append(lines, "", "> "+disp, "")
	}
	lines = append(lines, ansi.Faint+"  [Enter] Confirm  [Esc] Back"+ansi.Reset)
	return lines
}

func (w *wizardComponent) renderProviderKey(width int) []string {
	s := w.currentSlot()
	var lines []string
	lines = append(lines, w.renderHeader("API Key", w.stepForState(w.state), wizardTotalSteps)...)
	if w.inputMode != "apikey" {
		// Using TUI's input overlay. Show a simple instruction.
		lines = append(lines, fmt.Sprintf("  API key for %s:", s.providerName))
		lines = append(lines, "  (enter in the input line at the bottom)")
	} else {
		// Fallback: embedded editor mode (used when ShowInput is unavailable).
		disp := ansi.Faint + "(paste key)" + ansi.Reset
		if w.editor.Text() != "" {
			masked := strings.Repeat("*", len([]rune(w.editor.Text())))
			disp = ansi.RenderWithCursor(masked, w.editor.Cursor())
		}
		lines = append(lines, "", "> "+disp, "")
	}
	lines = append(lines, ansi.Faint+"  [Enter] Confirm  [Esc] Back"+ansi.Reset)
	return lines
}

func (w *wizardComponent) renderProviderTest(width int) []string {
	s := w.currentSlot()
	var lines []string
	lines = append(lines, w.renderHeader("Provider Review", w.stepForState(w.state), wizardTotalSteps)...)
	lines = append(lines,
		fmt.Sprintf("  Provider:  %s", s.providerName),
		fmt.Sprintf("  Endpoint:  %s", s.endpoint),
		fmt.Sprintf("  API Key:   %s", maskKey(s.apiKey)),
		"",
		ansi.Faint+"  (press Enter to continue)"+ansi.Reset,
		"",
		ansi.Faint+"  [Enter] Continue  [Esc] Back"+ansi.Reset,
	)
	return lines
}

func (w *wizardComponent) renderModelSelect(width int) []string {
	s := w.currentSlot()
	var lines []string
	lines = append(lines, w.renderHeader("Select Model", w.stepForState(w.state), wizardTotalSteps)...)
	lines = append(lines, "  Available models from "+s.providerName+":")
	lines = append(lines, "")

	displayCount := 12
	if len(s.availableModels) < displayCount {
		displayCount = len(s.availableModels)
	}

	startIdx := 0
	if s.selectedModelIdx >= displayCount {
		startIdx = s.selectedModelIdx - displayCount + 1
	}
	endIdx := startIdx + displayCount
	if endIdx > len(s.availableModels) {
		endIdx = len(s.availableModels)
	}

	for i := startIdx; i < endIdx; i++ {
		marker := " "
		model := s.availableModels[i]
		if i == s.selectedModelIdx {
			marker = ">"
			model = ansi.Bold + ansi.Fg("#58a6ff") + model + ansi.Reset
		}
		lines = append(lines, fmt.Sprintf("  %s %s", marker, model))
	}

	if len(s.availableModels) > displayCount {
		lines = append(lines, "")
		lines = append(lines, ansi.Faint+fmt.Sprintf("  (%d more... scroll)", len(s.availableModels)-endIdx)+ansi.Reset)
	}

	lines = append(lines, "")
	lines = append(lines, ansi.Faint+"  [Up/Down] Navigate  [Enter] Select  [Esc] Skip (use defaults)  [1-9] Quick pick"+ansi.Reset)
	return lines
}

func (w *wizardComponent) renderModelSetup(width int) []string {
	s := w.currentSlot()
	var lines []string
	lines = append(lines, w.renderHeader("Model Setup", w.stepForState(w.state), wizardTotalSteps)...)
	lines = append(lines, "  Configure your first model (or skip to use defaults):")
	lines = append(lines, "")
	fields := []struct{ label, committed string }{
		{"Model ID", s.modelID},
		{"Model name", s.modelName},
		{"Temperature", s.modelTemp},
		{"Max tokens", s.modelMaxTokens},
	}
	for i, f := range fields {
		marker := " "
		disp := f.committed
		if i == s.modelFieldIdx {
			marker = ">"
			disp = ansi.RenderWithCursor(w.editor.Text(), w.editor.Cursor())
			disp = ansi.Bold + disp + ansi.Reset
		}
		lines = append(lines, fmt.Sprintf("  %s %-12s %s", marker, f.label+":", disp))
	}
	lines = append(lines, "")
	lines = append(lines, ansi.Faint+"  [Enter] Continue  [Esc] Back  [Up/Down] Switch field  [\u2190/\u2192] Move cursor"+ansi.Reset)
	return lines
}

func (w *wizardComponent) renderModelAdvanced(width int) []string {
	s := w.currentSlot()
	reasoning := "disabled"
	if s.modelReasoning {
		reasoning = "enabled"
	}
	return []string{
		"",
		ansi.Faint + "  -- Main Model Advanced Options" + strings.Repeat("-", 14) + ansi.Reset,
		"",
		"  Optional reasoning / thinking settings:",
		"",
		fmt.Sprintf("  1) Reasoning:    %s", ansi.Bold+reasoning+ansi.Reset),
		fmt.Sprintf("  2) Thinking lvl: %s", ansi.Bold+s.modelThinkingLevel+ansi.Reset),
		"",
		ansi.Faint + "  [1-2] Toggle  [Enter] Continue  [Esc] Back" + ansi.Reset,
	}
}

// savedConfigPath returns the absolute path of the home config file the
// wizard wrote (honoring --home / GOA_HOME). Falls back to the conventional
// display path when no loader is attached (unit tests).
func (w *wizardComponent) savedConfigPath() string {
	if w.loader != nil {
		return w.loader.HomeConfigPath()
	}
	if home, ok := internal.GoaHome(); ok {
		return filepath.Join(home, ".goa", "config.yaml")
	}
	return "~/.goa/config.yaml"
}

func (w *wizardComponent) renderDone(width int) []string {
	check := ansi.Fg("#3fb950") + "OK" + ansi.Reset
	var lines []string
	lines = append(lines, w.renderHeader("Setup Complete", w.stepForState(w.state), wizardTotalSteps)...)
	lines = append(lines, "  "+check+ansi.Bold+"  All set!"+ansi.Reset)
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("  %s  Provider:  %s", check, w.main.providerName))
	if w.main.modelID != "" {
		lines = append(lines, fmt.Sprintf("  %s  Model:     %s (thinking: %s)", check, w.main.modelID, w.main.modelThinkingLevel))
	}
	lines = append(lines, fmt.Sprintf("  %s  Mode:      %s (%s)", check, wizardDefaultMajor, wizardDefaultAutonomy))
	lines = append(lines, fmt.Sprintf("  %s  Config:    %s", check, w.savedConfigPath()))
	lines = append(lines, "")
	lines = append(lines, "  Use /profile anytime to switch persona.")
	lines = append(lines, "")
	lines = append(lines, ansi.Faint+"  [Enter] Start Goa"+ansi.Reset)
	if w.saveErr != nil {
		lines = append(lines, "")
		lines = append(lines, ansi.Fg("#f85149")+"  Warning: failed to save config: "+w.saveErr.Error()+ansi.Reset)
	}
	return lines
}
