// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/tui"
)

func TestWizardComponent_renderModelAdvancedDefaults(t *testing.T) {
	done := make(chan *WizardResult, 1)
	w := newWizardComponent(&Config{}, nil, "/tmp", done)
	w.initDefaults()
	w.state = stateModelAdvanced

	lines := w.renderModelAdvanced(80)
	got := ansi.Strip(strings.Join(lines, "\n"))
	if !strings.Contains(got, "Reasoning:    enabled") {
		t.Errorf("expected reasoning enabled in render, got:\n%s", got)
	}
	if !strings.Contains(got, "Thinking lvl: high") {
		t.Errorf("expected thinking level high in render, got:\n%s", got)
	}
}

func TestWizardComponent_initDefaults_ReasoningEnabledHigh(t *testing.T) {
	done := make(chan *WizardResult, 1)
	w := newWizardComponent(&Config{}, nil, "/tmp", done)
	w.initDefaults()

	if !w.main.modelReasoning {
		t.Errorf("main.modelReasoning = %v, want true", w.main.modelReasoning)
	}
	if w.main.modelThinkingLevel != "high" {
		t.Errorf("main.modelThinkingLevel = %q, want high", w.main.modelThinkingLevel)
	}
	if !w.companion.modelReasoning {
		t.Errorf("companion.modelReasoning = %v, want true", w.companion.modelReasoning)
	}
	if w.companion.modelThinkingLevel != "high" {
		t.Errorf("companion.modelThinkingLevel = %q, want high", w.companion.modelThinkingLevel)
	}
}

func TestWizardComponent_initModelSetup(t *testing.T) {
	done := make(chan *WizardResult, 1)
	w := newWizardComponent(&Config{}, nil, "/tmp", done)
	w.main.selectedPresetIndex = 0

	w.initModelSetup("")
	if w.state != stateModelSetup {
		t.Errorf("state = %d, want stateModelSetup", w.state)
	}
	if w.main.modelID != "default" {
		t.Errorf("modelID = %q, want default", w.main.modelID)
	}
	if w.editor.Text() != "default" {
		t.Errorf("editor.Text() = %q, want default", w.editor.Text())
	}

	w.initModelSetup("gpt-4o-mini")
	if w.main.modelName != "gpt-4o-mini" {
		t.Errorf("modelName = %q, want gpt-4o-mini", w.main.modelName)
	}
	if w.editor.Text() != "default" {
		t.Errorf("editor.Text() = %q, want default", w.editor.Text())
	}
}

func TestWizardComponent_commitEditorToField(t *testing.T) {
	done := make(chan *WizardResult, 1)
	w := newWizardComponent(&Config{}, nil, "/tmp", done)
	w.initModelSetup("")

	w.editor.SetText("my-model")
	w.commitEditorToField()
	if w.main.modelID != "my-model" {
		t.Errorf("modelID = %q, want my-model", w.main.modelID)
	}

	w.main.modelFieldIdx = 1
	w.editor.SetText("My Model")
	w.commitEditorToField()
	if w.main.modelName != "My Model" {
		t.Errorf("modelName = %q, want My Model", w.main.modelName)
	}

	w.main.modelFieldIdx = 2
	w.editor.SetText("0.7")
	w.commitEditorToField()
	if w.main.modelTemp != "0.7" {
		t.Errorf("modelTemp = %q, want 0.7", w.main.modelTemp)
	}
}

func TestWizardComponent_loadFieldIntoEditor(t *testing.T) {
	done := make(chan *WizardResult, 1)
	w := newWizardComponent(&Config{}, nil, "/tmp", done)
	w.main.modelID = "abc"
	w.main.modelName = "def"
	w.main.modelTemp = "0.5"

	w.main.modelFieldIdx = 0
	w.loadFieldIntoEditor()
	if w.editor.Text() != "abc" {
		t.Errorf("editor.Text() = %q, want abc", w.editor.Text())
	}

	w.main.modelFieldIdx = 1
	w.loadFieldIntoEditor()
	if w.editor.Text() != "def" {
		t.Errorf("editor.Text() = %q, want def", w.editor.Text())
	}

	w.main.modelFieldIdx = 2
	w.loadFieldIntoEditor()
	if w.editor.Text() != "0.5" {
		t.Errorf("editor.Text() = %q, want 0.5", w.editor.Text())
	}
}

func TestWizardComponent_editorCursorMovement(t *testing.T) {
	done := make(chan *WizardResult, 1)
	w := newWizardComponent(&Config{}, nil, "/tmp", done)
	w.initModelSetup("")

	// Type additional text
	w.editor.SetText("default-model")
	w.editor.SetCursor(7)
	if w.editor.Cursor() != 7 {
		t.Errorf("cursor = %d, want 7", w.editor.Cursor())
	}

	// Move left
	w.editor.HandleKey(tui.KeyLeft)
	if w.editor.Cursor() != 6 {
		t.Errorf("cursor after left = %d, want 6", w.editor.Cursor())
	}

	// Move right
	w.editor.HandleKey(tui.KeyRight)
	if w.editor.Cursor() != 7 {
		t.Errorf("cursor after right = %d, want 7", w.editor.Cursor())
	}

	// Home
	w.editor.HandleKey(tui.KeyHome)
	if w.editor.Cursor() != 0 {
		t.Errorf("cursor after home = %d, want 0", w.editor.Cursor())
	}

	// End
	w.editor.HandleKey(tui.KeyEnd)
	if w.editor.Cursor() != 13 {
		t.Errorf("cursor after end = %d, want 13", w.editor.Cursor())
	}
}

func TestWizardComponent_editorBackspace(t *testing.T) {
	done := make(chan *WizardResult, 1)
	w := newWizardComponent(&Config{}, nil, "/tmp", done)
	w.initModelSetup("")

	w.editor.SetText("hello")
	w.editor.HandleKey(tui.KeyBackspace)
	if w.editor.Text() != "hell" {
		t.Errorf("text = %q, want hell", w.editor.Text())
	}
}

func TestWizardComponent_editorDelete(t *testing.T) {
	done := make(chan *WizardResult, 1)
	w := newWizardComponent(&Config{}, nil, "/tmp", done)
	w.initModelSetup("")

	w.editor.SetText("hello")
	w.editor.SetCursor(1)
	w.editor.HandleKey(tui.KeyDelete)
	if w.editor.Text() != "hllo" {
		t.Errorf("text = %q, want hllo", w.editor.Text())
	}
}

func TestWizardComponent_editorCtrlU(t *testing.T) {
	done := make(chan *WizardResult, 1)
	w := newWizardComponent(&Config{}, nil, "/tmp", done)
	w.initModelSetup("")

	w.editor.SetText("hello world")
	w.editor.SetCursor(6)
	w.editor.HandleKey(tui.KeyCtrlU)
	if w.editor.Text() != "world" {
		t.Errorf("text = %q, want world", w.editor.Text())
	}
	if w.editor.Cursor() != 0 {
		t.Errorf("cursor = %d, want 0", w.editor.Cursor())
	}
}

func TestWizardComponent_editorCtrlK(t *testing.T) {
	done := make(chan *WizardResult, 1)
	w := newWizardComponent(&Config{}, nil, "/tmp", done)
	w.initModelSetup("")

	w.editor.SetText("hello world")
	w.editor.SetCursor(6)
	w.editor.HandleKey(tui.KeyCtrlK)
	if w.editor.Text() != "hello " {
		t.Errorf("text = %q, want hello ", w.editor.Text())
	}
}

func TestWizardComponent_editorCtrlW(t *testing.T) {
	done := make(chan *WizardResult, 1)
	w := newWizardComponent(&Config{}, nil, "/tmp", done)
	w.initModelSetup("")

	w.editor.SetText("hello world")
	w.editor.SetCursor(11)
	w.editor.HandleKey(tui.KeyCtrlW)
	if w.editor.Text() != "hello " {
		t.Errorf("text = %q, want hello ", w.editor.Text())
	}
}

func TestWizardComponent_handleUpDownSavesField(t *testing.T) {
	done := make(chan *WizardResult, 1)
	w := newWizardComponent(&Config{}, nil, "/tmp", done)
	w.initModelSetup("")

	// Edit model ID
	w.editor.SetText("custom-id")
	w.handleDown() // should save modelID and load modelName
	if w.main.modelID != "custom-id" {
		t.Errorf("modelID = %q, want custom-id", w.main.modelID)
	}
	if w.editor.Text() != w.main.modelName {
		t.Errorf("editor.Text() = %q, want %q", w.editor.Text(), w.main.modelName)
	}

	// Edit model name
	w.editor.SetText("Custom Name")
	w.handleDown() // should save modelName and load modelTemp
	if w.main.modelName != "Custom Name" {
		t.Errorf("modelName = %q, want Custom Name", w.main.modelName)
	}
	if w.editor.Text() != w.main.modelTemp {
		t.Errorf("editor.Text() = %q, want %q", w.editor.Text(), w.main.modelTemp)
	}
}

func TestWizardComponent_commitTextInput(t *testing.T) {
	done := make(chan *WizardResult, 1)
	w := newWizardComponent(&Config{}, nil, "/tmp", done)

	w.inputMode = "endpoint"
	w.editor.SetText("https://api.example.com")
	w.commitTextInput()
	if w.main.endpoint != "https://api.example.com" {
		t.Errorf("endpoint = %q, want https://api.example.com", w.main.endpoint)
	}
	if w.editor.Text() != "" {
		t.Errorf("editor should be cleared, got %q", w.editor.Text())
	}

	w.inputMode = "apikey"
	w.editor.SetText("sk-secret")
	w.commitTextInput()
	if w.main.apiKey != "sk-secret" {
		t.Errorf("apiKey = %q, want sk-secret", w.main.apiKey)
	}
}

func TestRenderWithCursor_ansi(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		cursor int
		want   string
	}{
		{"empty", "", 0, ansi.Reverse + " " + ansi.Reset},
		{"at_end", "hello", 5, "hello" + ansi.Reverse + " " + ansi.Reset},
		{"at_start", "hello", 0, ansi.Reverse + "h" + ansi.Reset + "ello"},
		{"middle", "hello", 2, "he" + ansi.Reverse + "l" + ansi.Reset + "lo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ansi.RenderWithCursor(tt.text, tt.cursor)
			if got != tt.want {
				t.Errorf("RenderWithCursor(%q, %d) = %q, want %q", tt.text, tt.cursor, got, tt.want)
			}
		})
	}
}

func TestWizardComponent_enterAdvancesNotTypesText(t *testing.T) {
	done := make(chan *WizardResult, 1)
	w := newWizardComponent(&Config{}, nil, "/tmp", done)
	w.state = stateProviderEndpoint
	w.inputMode = "endpoint"
	w.editor.SetText("https://api.example.com")

	// Pressing Enter should advance to the next state, not type "enter"
	w.HandleInput(tui.KeyEnter)

	if w.state != stateProviderKey {
		t.Errorf("state = %d, want stateProviderKey; Enter did not advance", w.state)
	}
	if w.editor.Text() == "https://api.example.comenter" || w.editor.Text() == "enter" {
		t.Errorf("editor.Text() = %q; Enter was treated as text input", w.editor.Text())
	}
}

func TestWizardComponent_enterAdvancesLastField(t *testing.T) {
	done := make(chan *WizardResult, 1)
	w := newWizardComponent(&Config{}, nil, "/tmp", done)
	w.state = stateModelSetup
	w.inputMode = "model"
	w.main.modelFieldIdx = 2 // Last field (temperature)
	w.editor.SetText("0.5")

	// Pressing Enter on last field should advance to the model advanced screen
	w.HandleInput(tui.KeyEnter)

	if w.state != stateModelAdvanced {
		t.Errorf("state = %d, want stateModelAdvanced; Enter did not advance from last field", w.state)
	}
}

func TestModelsEndpoint(t *testing.T) {
	tests := []struct {
		endpoint string
		want     string
	}{
		{"https://api.openai.com/v1", "https://api.openai.com/v1/models"},
		{"https://api.openai.com/v1/chat/completions", "https://api.openai.com/v1/models"},
		{"http://localhost:1234/v1/chat/completions", "http://localhost:1234/v1/models"},
		{"http://localhost:1234", "http://localhost:1234/models"},
	}
	for _, tt := range tests {
		t.Run(tt.endpoint, func(t *testing.T) {
			got := modelsEndpoint(tt.endpoint)
			if got != tt.want {
				t.Errorf("modelsEndpoint(%q) = %q, want %q", tt.endpoint, got, tt.want)
			}
		})
	}
}

func TestWizardComponent_escapeFromAdvancedOptionsGoesSkillMode(t *testing.T) {
	done := make(chan *WizardResult, 1)
	w := newWizardComponent(&Config{}, nil, "/tmp", done)
	w.state = stateAdvancedOptions
	w.advancedMode = 0

	w.HandleInput(tui.KeyEscape)
	if w.state != stateSkillMode {
		t.Errorf("state = %d, want stateSkillMode; Escape did not go back from advanced options", w.state)
	}
}

func TestWizardComponent_escapeFromEveryStateGoesBack(t *testing.T) {
	cases := []struct {
		state    wizardState
		wantBack wizardState
		setup    func(*wizardComponent)
	}{
		{stateProviderType, stateWelcome, nil},
		{stateProviderEndpoint, stateProviderType, func(w *wizardComponent) { w.inputMode = "endpoint" }},
		{stateProviderKey, stateProviderType, func(w *wizardComponent) { w.inputMode = "apikey" }},
		{stateProviderTest, stateProviderType, nil},
		{stateModelSelect, stateProviderTest, func(w *wizardComponent) { w.main.availableModels = []string{"a"} }},
		{stateModelSetup, stateProviderTest, func(w *wizardComponent) { w.initModelSetup("") }},
		{stateModelAdvanced, stateModelSetup, nil},
		{stateWebFetchSummary, stateModelAdvanced, nil},
		{stateCompanionModel, stateWebFetchSummary, nil},
		{stateCompanionProviderType, stateCompanionModel, nil},
		{stateCompanionProviderEndpoint, stateCompanionProviderType, func(w *wizardComponent) { w.inputMode = "endpoint" }},
		{stateCompanionProviderKey, stateCompanionProviderType, func(w *wizardComponent) { w.inputMode = "apikey" }},
		{stateCompanionProviderTest, stateCompanionProviderType, nil},
		{stateCompanionModelSelect, stateCompanionProviderTest, func(w *wizardComponent) { w.companion.availableModels = []string{"a"} }},
		{stateCompanionModelSetup, stateCompanionProviderTest, func(w *wizardComponent) { w.initModelSetup("") }},
		{stateCompanionModelAdvanced, stateCompanionModelSetup, nil},
		{stateMode, stateCompanionModelSetup, nil},
		{stateMode, stateCompanionModel, func(w *wizardComponent) { w.companionModelSelected = true }},
		{stateSkillMode, stateMode, nil},
		{stateAdvancedOptions, stateSkillMode, nil},
		{statePromptPreview, stateAdvancedOptions, nil},
		{stateWorkflowPreview, statePromptPreview, nil},
		{stateDone, stateWorkflowPreview, nil},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("state%d", tc.state), func(t *testing.T) {
			done := make(chan *WizardResult, 1)
			w := newWizardComponent(&Config{}, nil, "/tmp", done)
			w.state = tc.state
			if tc.setup != nil {
				tc.setup(w)
				w.state = tc.state
			}
			w.HandleInput(tui.KeyEscape)
			if w.state != tc.wantBack {
				t.Errorf("state = %d, want %d", w.state, tc.wantBack)
			}
		})
	}
}

func TestWizardComponent_executionModeSolo(t *testing.T) {
	cfg := &Config{}
	done := make(chan *WizardResult, 1)
	w := newWizardComponent(cfg, nil, "/tmp", done)
	w.main.providerID = "openai"
	w.main.providerName = "OpenAI"
	w.main.modelID = "gpt-4o"
	w.main.modelName = "gpt-4o"
	w.state = stateMode
	w.selectedMode = 0 // Solo
	w.saveConfig()

	if cfg.Execution.Mode != internal.ExecutionSolo {
		t.Errorf("Execution.Mode = %q, want %q", cfg.Execution.Mode, internal.ExecutionSolo)
	}

	lines := w.renderDone(80)
	found := false
	for _, line := range lines {
		if strings.Contains(line, "Mode:") && strings.Contains(line, "Solo") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("renderDone did not show Mode: Solo; got %v", lines)
	}
}

func TestWizardComponent_executionModeSummaryMatchesSelection(t *testing.T) {
	modes := []struct {
		idx      int
		modeName string
		modeVal  internal.ExecutionMode
	}{
		{0, "Solo", internal.ExecutionSolo},
		{1, "Yolo", internal.ExecutionYolo},
		{2, "Confirm", internal.ExecutionConfirm},
		{3, "Review", internal.ExecutionReview},
	}

	for _, tc := range modes {
		t.Run(tc.modeName, func(t *testing.T) {
			cfg := &Config{}
			done := make(chan *WizardResult, 1)
			w := newWizardComponent(cfg, nil, "/tmp", done)
			w.main.providerID = "openai"
			w.main.providerName = "OpenAI"
			w.main.modelID = "gpt-4o"
			w.main.modelName = "gpt-4o"
			w.selectedMode = tc.idx
			w.saveConfig()

			if cfg.Execution.Mode != tc.modeVal {
				t.Errorf("Execution.Mode = %q, want %q", cfg.Execution.Mode, tc.modeVal)
			}

			lines := w.renderDone(80)
			found := false
			for _, line := range lines {
				if strings.Contains(line, "Mode:") && strings.Contains(line, tc.modeName) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("renderDone did not show Mode: %s; got %v", tc.modeName, lines)
			}
		})
	}
}

func TestWizardComponent_escapeFromProviderTestGoesProviderType(t *testing.T) {
	done := make(chan *WizardResult, 1)
	w := newWizardComponent(&Config{}, nil, "/tmp", done)
	w.state = stateProviderTest
	w.main.availableModels = []string{"model-a", "model-b"}

	w.HandleInput(tui.KeyEscape)
	if w.state != stateProviderType {
		t.Errorf("state = %d, want stateProviderType", w.state)
	}
	if len(w.main.availableModels) != 0 {
		t.Errorf("availableModels not cleared: %v", w.main.availableModels)
	}
}
