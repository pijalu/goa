// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"github.com/pijalu/goa/tui"
)

var wizardRenderers = map[wizardState]func(*wizardComponent, int) []string{
	stateWelcome:                   (*wizardComponent).renderWelcome,
	stateProviderType:              (*wizardComponent).renderProviderType,
	stateProviderEndpoint:          (*wizardComponent).renderProviderEndpoint,
	stateProviderKey:               (*wizardComponent).renderProviderKey,
	stateProviderTest:              (*wizardComponent).renderProviderTest,
	stateModelSelect:               (*wizardComponent).renderModelSelect,
	stateModelSetup:                (*wizardComponent).renderModelSetup,
	stateModelAdvanced:             (*wizardComponent).renderModelAdvanced,
	stateWebFetchSummary:           (*wizardComponent).renderWebFetchSummary,
	stateCompanionModel:            (*wizardComponent).renderCompanionModel,
	stateDreamModel:                (*wizardComponent).renderDreamModel,
	stateCompanionModelSetup:       (*wizardComponent).renderCompanionModelSetup,
	stateCompanionProviderType:     (*wizardComponent).renderProviderType,
	stateCompanionProviderEndpoint: (*wizardComponent).renderProviderEndpoint,
	stateCompanionProviderKey:      (*wizardComponent).renderProviderKey,
	stateCompanionProviderTest:     (*wizardComponent).renderProviderTest,
	stateCompanionModelSelect:      (*wizardComponent).renderModelSelect,
	stateCompanionModelAdvanced:    (*wizardComponent).renderModelAdvanced,
	stateMode:                      (*wizardComponent).renderMode,
	stateSkillMode:                 (*wizardComponent).renderSkillMode,
	stateAdvancedOptions:           (*wizardComponent).renderAdvancedOptions,
	statePromptPreview:             (*wizardComponent).renderPromptPreview,
	stateWorkflowPreview:           (*wizardComponent).renderWorkflowPreview,
	stateDone:                      (*wizardComponent).renderDone,
}

func (w *wizardComponent) Render(width int) []string {
	if fn, ok := wizardRenderers[w.state]; ok {
		return fn(w, width)
	}
	return nil
}

func (w *wizardComponent) HandleInput(data string) {
	if w.handleNavKey(data) {
		return
	}
	if w.handleActionKey(data) {
		return
	}
	w.handleTextInput(data)
}

func (w *wizardComponent) handleNavKey(data string) bool {
	switch {
	case matchesKey(data, tui.KeyUp):
		w.handleUp()
	case matchesKey(data, tui.KeyDown):
		w.handleDown()
	default:
		// Don't pass action keys to the editor — let handleActionKey process them.
		if matchesKey(data, tui.KeyEnter) || matchesKey(data, tui.KeyEscape) ||
			matchesKey(data, tui.KeyBackspace) || (len(data) == 1 && data[0] >= '1' && data[0] <= '9') {
			return false
		}
		if w.inputMode != "" && w.editor.HandleKey(data) {
			return true
		}
		return false
	}
	return true
}

func (w *wizardComponent) handleActionKey(data string) bool {
	switch {
	case matchesKey(data, tui.KeyEnter):
		if w.advance() {
			w.finish()
		}
	case matchesKey(data, tui.KeyEscape):
		w.handleEscape()
	case matchesKey(data, tui.KeyBackspace):
		if w.inputMode != "" {
			w.editor.HandleKey(data)
		} else {
			w.goBack()
		}
	case len(data) == 1 && data[0] >= '1' && data[0] <= '9':
		w.handleNumber(data)
	default:
		return false
	}
	return true
}

func (w *wizardComponent) handleEscape() {
	if w.state == stateWelcome {
		w.cancelled = true
		w.finish()
		return
	}
	w.goBack()
}

func (w *wizardComponent) handleTextInput(data string) {
	if w.inputMode == "" {
		return
	}
	w.editor.HandleKey(data)
}

func (w *wizardComponent) TrapInput(data string) bool {
	// Only trap Ctrl+C here. Escape is intentionally handled by the normal
	// HandleInput path so Enter (and other action keys) work reliably in the
	// wizard; trapping Escape in TrapInput causes key dispatch ordering issues
	// with some terminals / Kitty protocol states.
	if matchesKey(data, tui.KeyCtrlC) {
		w.cancelled = true
		w.finish()
		return true
	}
	return false
}

func (w *wizardComponent) Invalidate()             {}
func (w *wizardComponent) SetFocused(focused bool) { w.focused = focused }
func (w *wizardComponent) Focused() bool           { return w.focused }

func matchesKey(data, key string) bool { return data == key }

// -- State transitions --------------------------------------------

func (w *wizardComponent) advance() bool {
	if w.state == stateDone {
		w.saved = true
		return true
	}
	w.advanceByPhase()
	return false
}

func (w *wizardComponent) advanceByPhase() {
	switch {
	case w.advanceProvider():
	case w.advanceModel():
	case w.advancePostModel():
	}
}

func (w *wizardComponent) advanceProvider() bool {
	switch w.state {
	case stateWelcome:
		w.state = stateProviderType
	case stateProviderType:
		w.advanceFromProviderType()
	case stateProviderEndpoint:
		w.advanceFromEndpoint()
	case stateProviderKey:
		w.advanceFromKey()
	case stateProviderTest:
		w.advanceFromTest()
	case stateCompanionProviderType:
		w.advanceCompanionFromProviderType()
	case stateCompanionProviderEndpoint:
		w.advanceFromEndpoint()
	case stateCompanionProviderKey:
		w.advanceFromKey()
	case stateCompanionProviderTest:
		w.advanceFromTest()
	default:
		return false
	}
	return true
}

func (w *wizardComponent) advanceModel() bool {
	switch w.state {
	case stateModelSelect:
		w.advanceFromModelSelect()
	case stateModelSetup:
		w.advanceFromModelSetup()
	case stateCompanionModelSelect:
		w.advanceFromModelSelect()
	case stateCompanionModelSetup:
		w.advanceCompanionFromModelSetup()
	default:
		return false
	}
	return true
}

func (w *wizardComponent) advancePostModel() bool {
	switch w.state {
	case stateModelAdvanced:
		w.state = stateWebFetchSummary
	case stateWebFetchSummary:
		w.state = stateDreamModel
	case stateDreamModel:
		w.advanceFromDreamModel()
	case stateCompanionModelAdvanced:
		w.state = stateMode
	case stateCompanionModel:
		w.advanceFromCompanionModel()
	case stateCompanionModelSetup:
		w.advanceFromCompanionModelSetup()
	case stateMode:
		w.state = stateSkillMode
	case stateSkillMode:
		w.state = stateAdvancedOptions
		w.prepareAdvancedOptions()
	case stateAdvancedOptions:
		w.state = statePromptPreview
		w.previewYesNo = 1
	case statePromptPreview:
		w.advanceFromPromptPreview()
	case stateWorkflowPreview:
		w.advanceFromWorkflowPreview()
	default:
		return false
	}
	return true
}

func (w *wizardComponent) advanceFromCompanionModel() {
	if w.companionUseMainModel == 1 {
		w.state = stateMode
	} else {
		w.state = stateCompanionProviderType
	}
}

func (w *wizardComponent) advanceFromDreamModel() {
	w.state = stateCompanionModel
}

func (w *wizardComponent) advanceFromCompanionModelSetup() {
	w.companion.modelName = w.editor.Text()
	if w.companion.modelName == "" {
		w.companion.modelName = w.main.modelName
	}
	w.inputMode = ""
	w.editor.Clear()
	w.state = stateMode
}

// currentSlot returns the active modelSlot based on the current wizard state.
func (w *wizardComponent) currentSlot() *modelSlot {
	switch w.state {
	case stateCompanionProviderType, stateCompanionProviderEndpoint,
		stateCompanionProviderKey, stateCompanionProviderTest,
		stateCompanionModelSelect, stateCompanionModelSetup,
		stateCompanionModelAdvanced:
		return &w.companion
	default:
		return &w.main
	}
}

func (w *wizardComponent) advanceFromEndpoint() {
	w.commitTextInput()
	s := w.currentSlot()
	s.endpoint = w.editor.Text()
	if s.selectedPresetIndex < 0 {
		s.providerID = DeriveProviderID(s.endpoint)
		s.providerName = deriveProviderName(s.endpoint)
	}
	w.editor.Clear()
	if w.state == stateCompanionProviderEndpoint {
		w.state = stateCompanionProviderKey
	} else {
		w.state = stateProviderKey
	}
	w.startKeyInput(s)
}

func (w *wizardComponent) advanceFromKey() {
	w.commitTextInput()
	s := w.currentSlot()
	s.apiKey = w.editor.Text()
	if w.state == stateCompanionProviderKey {
		w.state = stateCompanionProviderTest
	} else {
		w.state = stateProviderTest
	}
	w.inputMode = ""
	w.editor.Clear()
}

func (w *wizardComponent) startKeyInput(s *modelSlot) {
	w.inputMode = "apikey"
	w.editor.SetText(s.apiKey)
}

func (w *wizardComponent) advanceFromTest() {
	w.inputMode = ""
	w.editor.Clear()
	s := w.currentSlot()
	w.fetchAvailableModels(s)
	if len(s.availableModels) > 0 {
		s.selectedModelIdx = 0
		if w.state == stateCompanionProviderTest {
			w.state = stateCompanionModelSelect
		} else {
			w.state = stateModelSelect
		}
	} else {
		w.initModelSetup("")
	}
}

func (w *wizardComponent) startEndpointInput(s *modelSlot) {
	w.inputMode = "endpoint"
	w.editor.SetText(s.endpoint)
}

func (w *wizardComponent) advanceFromModelSelect() {
	s := w.currentSlot()
	selected := ""
	if s.selectedModelIdx >= 0 && s.selectedModelIdx < len(s.availableModels) {
		selected = s.availableModels[s.selectedModelIdx]
	}
	if selected != "" {
		// Use the selected model directly; skip the manual ID/name form.
		s.modelID = selected
		s.modelName = selected
		if w.state == stateCompanionModelSelect {
			w.state = stateCompanionModelAdvanced
		} else {
			w.state = stateModelAdvanced
		}
		return
	}
	w.initModelSetup("")
	if w.state == stateCompanionModelSelect || w.state == stateCompanionProviderTest {
		w.state = stateCompanionModelSetup
	} else {
		w.state = stateModelSetup
	}
}

func (w *wizardComponent) advanceFromModelSetup() {
	w.commitEditorToField()
	w.inputMode = ""
	w.editor.Clear()
	if w.state == stateCompanionModelSetup {
		w.state = stateCompanionModelAdvanced
	} else {
		w.state = stateModelAdvanced
	}
}

func (w *wizardComponent) advanceFromPromptPreview() {
	w.copyPrompts = w.previewYesNo == 0
	w.state = stateWorkflowPreview
	w.previewYesNo = 1
}

func (w *wizardComponent) advanceFromWorkflowPreview() {
	w.copyWorkflows = w.previewYesNo == 0
	w.state = stateDone
	w.saveConfig()
}

func (w *wizardComponent) advanceCompanionFromProviderType() {
	presets := PresetProviders()
	s := &w.companion
	switch {
	case s.selectedPresetIndex == -1:
		w.state = stateCompanionProviderEndpoint
		w.inputMode = ""
		w.editor.Clear()
		w.startEndpointInput(s)
	case s.selectedPresetIndex >= 0 && s.selectedPresetIndex < len(presets) && presets[s.selectedPresetIndex].NeedsAPIKey:
		w.state = stateCompanionProviderKey
		w.inputMode = ""
		w.editor.Clear()
		w.startKeyInput(s)
	default:
		w.state = stateCompanionProviderTest
		w.inputMode = ""
		w.editor.Clear()
	}
}

func (w *wizardComponent) advanceCompanionFromModelSetup() {
	w.companion.modelName = w.editor.Text()
	if w.companion.modelName == "" {
		w.companion.modelName = w.main.modelName
	}
	w.companion.modelID = w.companion.modelName
	w.inputMode = ""
	w.editor.Clear()
	w.state = stateCompanionModelAdvanced
}

func (w *wizardComponent) advanceFromProviderType() {
	presets := PresetProviders()
	switch {
	case w.main.selectedPresetIndex == -1:
		w.state = stateProviderEndpoint
		w.inputMode = ""
		w.editor.Clear()
		w.startEndpointInput(&w.main)
	case w.main.selectedPresetIndex >= 0 && w.main.selectedPresetIndex < len(presets) && presets[w.main.selectedPresetIndex].NeedsAPIKey:
		w.state = stateProviderKey
		w.inputMode = ""
		w.editor.Clear()
		w.startKeyInput(&w.main)
	default:
		w.state = stateProviderTest
		w.inputMode = ""
		w.editor.Clear()
	}
}

func (w *wizardComponent) initModelSetup(defaultModel string) {
	if w.state == stateCompanionProviderTest {
		w.state = stateCompanionModelSetup
	} else {
		w.state = stateModelSetup
	}
	w.inputMode = "model"
	s := w.currentSlot()
	s.modelFieldIdx = modelFieldModelID
	s.modelID = "default"
	if defaultModel != "" {
		s.modelName = defaultModel
	} else {
		s.modelName = w.deriveDefaultModelName()
	}
	s.modelTemp = "0.2"
	w.editor.SetText(s.modelID)
}
