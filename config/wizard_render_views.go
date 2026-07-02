// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"fmt"
	"strings"

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
		"    3. Companion model settings",
		"    4. Execution mode",
		"",
		"  Use /profile at any time to switch persona.",
		"",
		ansi.Faint + "  [Enter] Start setup  [Esc] Skip (use defaults)  [Ctrl+C] Quit" + ansi.Reset,
	}
}

// stepForState returns the logical step number for the current wizard screen.
// Internal substates that may be skipped (e.g., endpoint/key entry for presets
// that do not need them) are collapsed into the same step as their parent group
// so the progress indicator never jumps backwards or skips numbers.
func (w *wizardComponent) stepForState(st wizardState) int {
	switch st {
	case stateProviderType, stateProviderEndpoint, stateProviderKey, stateProviderTest:
		return 1
	case stateModelSelect, stateModelSetup, stateModelAdvanced:
		return 2
	case stateWebFetchSummary:
		return 3
	case stateCompanionModel, stateCompanionProviderType, stateCompanionProviderEndpoint,
		stateCompanionProviderKey, stateCompanionProviderTest,
		stateCompanionModelSelect, stateCompanionModelSetup, stateCompanionModelAdvanced:
		return 3
	case stateMode:
		return 4
	case stateSkillMode:
		return 5
	case stateAdvancedOptions:
		return 6
	case statePromptPreview:
		return 7
	case stateWorkflowPreview, stateDone:
		return 8
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

func (w *wizardComponent) renderProviderType(width int) []string {
	presets := PresetProviders()
	s := w.currentSlot()
	var lines []string
	lines = append(lines, w.renderHeader("LLM Provider", w.stepForState(w.state), 9)...)
	lines = append(lines, "  Choose your LLM provider:")
	lines = append(lines, "")
	for i, p := range presets {
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

func (w *wizardComponent) renderProviderEndpoint(width int) []string {
	var lines []string
	lines = append(lines, w.renderHeader("Provider Endpoint", w.stepForState(w.state), 9)...)
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
	lines = append(lines, w.renderHeader("API Key", w.stepForState(w.state), 9)...)
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
	lines = append(lines, w.renderHeader("Provider Review", w.stepForState(w.state), 9)...)
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
	lines = append(lines, w.renderHeader("Select Model", w.stepForState(w.state), 9)...)
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
	lines = append(lines, w.renderHeader("Model Setup", w.stepForState(w.state), 9)...)
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

func (w *wizardComponent) renderMode(width int) []string {
	modes := []string{
		"Solo -- Auto-run tools constrained to this codebase",
		"Yolo -- All tool calls execute automatically",
		"Confirm -- Pause before each tool call",
		"Review -- Queue edits for batch approval",
	}
	var lines []string
	lines = append(lines, w.renderHeader("Execution Mode", w.stepForState(w.state), 9)...)
	lines = append(lines, "  Choose how Goa should run tools:")
	lines = append(lines, "")
	for i, m := range modes {
		marker := " "
		line := fmt.Sprintf("  %s  %s", marker, m)
		if i == w.selectedMode {
			line = ansi.Bold + fmt.Sprintf("  %s  %s", "*", m) + ansi.Reset
		}
		lines = append(lines, line)
	}
	lines = append(lines, "")
	lines = append(lines, ansi.Faint+"  [Up/Down] Navigate  [Enter] Confirm  [Esc] Back  [1-4] Quick pick"+ansi.Reset)
	return lines
}

func (w *wizardComponent) renderModelAdvanced(width int) []string {
	s := w.currentSlot()
	reasoning := "disabled"
	if s.modelReasoning {
		reasoning = "enabled"
	}
	title := "Main Model Advanced Options"
	if w.state == stateCompanionModelAdvanced {
		title = "Companion Model Advanced Options"
	}
	return []string{
		"",
		ansi.Faint + "  -- " + title + strings.Repeat("-", 42-len(title)) + ansi.Reset,
		"",
		"  Optional reasoning / thinking settings:",
		"",
		fmt.Sprintf("  1) Reasoning:    %s", ansi.Bold+reasoning+ansi.Reset),
		fmt.Sprintf("  2) Thinking lvl: %s", ansi.Bold+s.modelThinkingLevel+ansi.Reset),
		"",
		ansi.Faint + "  [1-2] Toggle  [Enter] Continue  [Esc] Back" + ansi.Reset,
	}
}

func (w *wizardComponent) renderWebFetchSummary(width int) []string {
	var lines []string
	lines = append(lines, w.renderHeader("WebFetch Summarization", w.stepForState(w.state), 9)...)
	lines = append(lines, "  Enable sub-agent summarization for web pages fetched with webfetch?")
	lines = append(lines, "")
	if w.webfetchSummaryEnabled == 1 {
		lines = append(lines, ansi.Bold+"  * Yes -- summarize fetched pages with a sub-agent"+ansi.Reset)
		lines = append(lines, "    No  -- fetch pages as Markdown only")
	} else {
		lines = append(lines, "    Yes -- summarize fetched pages with a sub-agent")
		lines = append(lines, ansi.Bold+"  * No  -- fetch pages as Markdown only"+ansi.Reset)
	}
	lines = append(lines, "")
	lines = append(lines, ansi.Faint+"  [Up/Down] Navigate  [Enter] Confirm  [Esc] Back  [1-2] Quick pick"+ansi.Reset)
	return lines
}

func (w *wizardComponent) renderCompanionModel(width int) []string {
	var lines []string
	lines = append(lines, w.renderHeader("Companion Model", w.stepForState(w.state), 10)...)
	lines = append(lines, fmt.Sprintf("  Use the same model (%s) for the companion agent?", w.main.modelName))
	lines = append(lines, "")
	if w.companionModelSelected {
		lines = append(lines, ansi.Bold+"  * Yes -- use "+w.main.modelName+" for companion"+ansi.Reset)
		lines = append(lines, "    No  -- configure separately")
	} else {
		lines = append(lines, "    Yes -- use "+w.main.modelName+" for companion")
		lines = append(lines, ansi.Bold+"  * No  -- configure separately"+ansi.Reset)
	}
	lines = append(lines, "")
	lines = append(lines, ansi.Faint+"  [Up/Down] Navigate  [Enter] Confirm  [Esc] Back"+ansi.Reset)
	return lines
}

func (w *wizardComponent) renderDreamModel(width int) []string {
	var lines []string
	lines = append(lines, w.renderHeader("Memory Dreams", w.stepForState(w.state), 10)...)
	lines = append(lines, "  Memory dreams consolidate raw memory files into a single cleaned file.")
	lines = append(lines, "")
	if w.dreamEnabled == 1 {
		lines = append(lines, ansi.Bold+"  * Yes -- enable memory dreams"+ansi.Reset)
		lines = append(lines, "    No  -- disable memory dreams")
	} else {
		lines = append(lines, "    Yes -- enable memory dreams")
		lines = append(lines, ansi.Bold+"  * No  -- disable memory dreams"+ansi.Reset)
	}
	lines = append(lines, "")
	lines = append(lines, ansi.Faint+"  [Up/Down] Navigate  [Enter] Confirm  [Esc] Back"+ansi.Reset)
	return lines
}

func (w *wizardComponent) renderSkillMode(width int) []string {
	modes := []string{
		"Sub-agent -- skills run in isolated agents",
		"Inline    -- skill instructions returned to main agent",
	}
	var lines []string
	lines = append(lines, w.renderHeader("Skill Execution Mode", w.stepForState(w.state), 9)...)
	lines = append(lines, "  Choose how skill instructions should be executed:")
	lines = append(lines, "")
	for i, m := range modes {
		marker := " "
		line := fmt.Sprintf("  %s  %s", marker, m)
		if i == w.skillMode {
			line = ansi.Bold + fmt.Sprintf("  %s  %s", "*", m) + ansi.Reset
		}
		lines = append(lines, line)
	}
	lines = append(lines, "")
	lines = append(lines, ansi.Faint+"  [Up/Down] Navigate  [Enter] Confirm  [Esc] Back  [1-2] Quick pick"+ansi.Reset)
	return lines
}

func (w *wizardComponent) renderAdvancedOptions(width int) []string {
	var lines []string
	lines = append(lines, w.renderHeader("Advanced Options", w.stepForState(w.state), 9)...)
	lines = append(lines, "  Fine-tune compression, tool limits, and edit fuzziness:")
	lines = append(lines, "")
	lines = append(lines, w.renderAdvancedModeLine())
	if w.advancedMode == 1 {
		lines = append(lines, w.renderAdvancedDetailLines()...)
	}
	lines = append(lines, "")
	lines = append(lines, ansi.Faint+"  [1] Toggle advanced  [2] Compression  [3] Fuzzy edits  [Up/Down] Switch field  [Enter] Continue  [Esc] Back"+ansi.Reset)
	return lines
}

func (w *wizardComponent) renderAdvancedModeLine() string {
	advMode := "use defaults"
	if w.advancedMode == 1 {
		advMode = "customize"
	}
	return fmt.Sprintf("  1) Advanced mode: %s", ansi.Bold+advMode+ansi.Reset)
}

func (w *wizardComponent) renderAdvancedDetailLines() []string {
	var lines []string
	lines = append(lines, w.renderCompressionLine())
	if w.compressEnabled == 1 {
		lines = append(lines, fmt.Sprintf("     Max tokens:    %s", w.renderAdvancedValue(0, w.compressMaxTokens)))
		lines = append(lines, fmt.Sprintf("     Threshold %%:   %s", w.renderAdvancedValue(1, w.compressThreshold)))
	}
	lines = append(lines, fmt.Sprintf("  3) Max tool repeat: %s", w.renderAdvancedValue(2, w.maxToolRepeat)))
	lines = append(lines, fmt.Sprintf("  4) Fuzzy edits:   %s", ansi.Bold+w.renderFuzzState()+ansi.Reset))
	return lines
}

func (w *wizardComponent) renderCompressionLine() string {
	compress := "disabled"
	if w.compressEnabled == 1 {
		compress = "enabled"
	}
	return fmt.Sprintf("  2) Compression:   %s", ansi.Bold+compress+ansi.Reset)
}

func (w *wizardComponent) renderAdvancedValue(fieldIdx int, committed string) string {
	if w.advancedFieldIdx == fieldIdx {
		return ansi.Bold + ansi.RenderWithCursor(w.editor.Text(), w.editor.Cursor()) + ansi.Reset
	}
	return committed
}

func (w *wizardComponent) renderFuzzState() string {
	if w.allowFuzzEdits == 1 {
		return "on"
	}
	return "off"
}

func (w *wizardComponent) renderCompanionModelSetup(width int) []string {
	var lines []string
	lines = append(lines, w.renderHeader("Companion Model Setup", w.stepForState(w.state), 9)...)
	lines = append(lines, "  Enter the model name for the companion agent:")
	lines = append(lines, "")
	if w.editor.Text() == "" {
		if w.companion.modelName != "" {
			w.editor.SetText(w.companion.modelName)
		} else {
			w.editor.SetText(w.main.modelName)
		}
	}
	lines = append(lines, fmt.Sprintf("  Model: %s", ansi.Bold+ansi.RenderWithCursor(w.editor.Text(), w.editor.Cursor())+ansi.Reset))
	lines = append(lines, "")
	lines = append(lines, ansi.Faint+"  [Enter] Continue  [Esc] Back"+ansi.Reset)
	return lines
}

func (w *wizardComponent) renderPromptPreview(width int) []string {
	var lines []string
	lines = append(lines, w.renderHeader("Prompts", w.stepForState(w.state), 9)...)
	lines = append(lines, "  Built-in prompts can be copied to .goa/prompts/ for customization.")
	lines = append(lines, "")
	lines = append(lines, "  Built-in prompts: companion, planner, coder, tester, pair, task, pipeline, tools")
	lines = append(lines, "")
	lines = append(lines, "  Copy prompts to .goa/prompts/ for customization?")
	lines = append(lines, "")
	if w.previewYesNo == 0 {
		lines = append(lines, ansi.Bold+"  * Yes -- copy to .goa/prompts/"+ansi.Reset)
		lines = append(lines, "    No  -- use embedded defaults")
	} else {
		lines = append(lines, "    Yes -- copy to .goa/prompts/")
		lines = append(lines, ansi.Bold+"  * No  -- use embedded defaults"+ansi.Reset)
	}
	lines = append(lines, "")
	lines = append(lines, ansi.Faint+"  [Up/Down] Navigate  [Enter] Confirm  [Esc] Back"+ansi.Reset)
	return lines
}

func (w *wizardComponent) renderWorkflowPreview(width int) []string {
	var lines []string
	lines = append(lines, w.renderHeader("Workflows", w.stepForState(w.state), 9)...)
	lines = append(lines, "  Built-in workflows can be copied to .goa/workflows/ for customization.")
	lines = append(lines, "")
	lines = append(lines, "  Built-in workflows: implement-feature, review, pair")
	lines = append(lines, "")
	lines = append(lines, "  Copy workflows to .goa/workflows/ for customization?")
	lines = append(lines, "")
	if w.previewYesNo == 0 {
		lines = append(lines, ansi.Bold+"  * Yes -- copy to .goa/workflows/"+ansi.Reset)
		lines = append(lines, "    No  -- use embedded defaults")
	} else {
		lines = append(lines, "    Yes -- copy to .goa/workflows/")
		lines = append(lines, ansi.Bold+"  * No  -- use embedded defaults"+ansi.Reset)
	}
	lines = append(lines, "")
	lines = append(lines, ansi.Faint+"  [Up/Down] Navigate  [Enter] Confirm  [Esc] Back"+ansi.Reset)
	return lines
}

func (w *wizardComponent) renderDone(width int) []string {
	modeNames := []string{"Solo", "Yolo", "Confirm", "Review"}
	check := ansi.Fg("#3fb950") + "OK" + ansi.Reset
	var lines []string
	lines = append(lines, w.renderHeader("Setup Complete", w.stepForState(w.state), 9)...)
	lines = append(lines, "  "+check+ansi.Bold+"  All set!"+ansi.Reset)
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("  %s  Provider:  %s", check, w.main.providerName))
	if w.selectedMode >= 0 && w.selectedMode < len(modeNames) {
		lines = append(lines, fmt.Sprintf("  %s  Mode:      %s", check, modeNames[w.selectedMode]))
	}
	lines = append(lines, fmt.Sprintf("  %s  Config:    ~/.goa/config.yaml", check))
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
