// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pijalu/goa/internal/ansi"
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
		w.state = stateCompanionModel
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
	if w.companionModelSelected {
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
	if w.tui == nil {
		return
	}
	ch := w.tui.ShowInput(fmt.Sprintf("API key for %s:", s.providerName), s.apiKey)
	go func() {
		result := <-ch
		if result == "" {
			return
		}
		w.tui.Apply(func() {
			// Find the current slot again (the pointer might be stale if the wizard
			// was restarted or companion was added during the wait).
			cs := w.currentSlot()
			cs.apiKey = result
			w.editor.Clear()
			w.inputMode = ""
			// Advance to test state
			if w.state == stateCompanionProviderKey {
				w.state = stateCompanionProviderTest
			} else {
				w.state = stateProviderTest
			}
			w.fetchAvailableModels(cs)
		})
	}()
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
	if w.tui == nil {
		return
	}
	ch := w.tui.ShowInput("API endpoint URL:", s.endpoint)
	go func() {
		result := <-ch
		if result == "" {
			return
		}
		w.tui.Apply(func() {
			cs := w.currentSlot()
			cs.endpoint = result
			if cs.selectedPresetIndex < 0 {
				cs.providerID = DeriveProviderID(result)
				cs.providerName = deriveProviderName(result)
			}
			w.editor.Clear()
			w.inputMode = ""
			// Determine if API key is needed
			presets := PresetProviders()
			needsKey := cs.selectedPresetIndex >= 0 && cs.selectedPresetIndex < len(presets) && presets[cs.selectedPresetIndex].NeedsAPIKey
			if needsKey {
				if w.state == stateCompanionProviderEndpoint {
					w.state = stateCompanionProviderKey
				} else {
					w.state = stateProviderKey
				}
				w.startKeyInput(cs)
			} else {
				if w.state == stateCompanionProviderEndpoint {
					w.state = stateCompanionProviderTest
				} else {
					w.state = stateProviderTest
				}
				w.fetchAvailableModels(cs)
			}
		})
	}()
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

func (w *wizardComponent) goBack() {
	w.state = w.previousState()
}

func (w *wizardComponent) previousState() wizardState {
	st := w.state
	if st == stateProviderType {
		return stateWelcome
	}
	if st == stateProviderEndpoint || st == stateProviderKey {
		w.clearInput()
		return stateProviderType
	}
	if st == stateProviderTest {
		w.clearInput()
		w.main.availableModels = nil
		return stateProviderType
	}
	if st == stateCompanionProviderTest {
		w.clearInput()
		w.companion.availableModels = nil
		return stateCompanionProviderType
	}
	return w.previousStateAfterProvider(st)
}

func (w *wizardComponent) previousStateAfterProvider(st wizardState) wizardState {
	if target, ok := w.previousStateMap(st); ok {
		return target
	}
	return st
}

func (w *wizardComponent) previousStateMap(st wizardState) (wizardState, bool) {
	if target, ok := staticPreviousStates[st]; ok {
		return target, true
	}
	return w.dynamicPreviousState(st)
}

var staticPreviousStates = map[wizardState]wizardState{
	stateModelSelect:            stateProviderTest,
	stateModelAdvanced:          stateModelSetup,
	stateWebFetchSummary:        stateModelAdvanced,
	stateCompanionModel:         stateWebFetchSummary,
	stateCompanionProviderType:  stateCompanionModel,
	stateCompanionModelSelect:   stateCompanionProviderTest,
	stateCompanionModelAdvanced: stateCompanionModelSetup,
	stateSkillMode:              stateMode,
	stateAdvancedOptions:        stateSkillMode,
	statePromptPreview:          stateAdvancedOptions,
	stateWorkflowPreview:        statePromptPreview,
	stateDone:                   stateWorkflowPreview,
}

func (w *wizardComponent) dynamicPreviousState(st wizardState) (wizardState, bool) {
	switch st {
	case stateModelSetup:
		w.commitEditorToField()
		w.clearInput()
		return w.modelSetupBackTarget(), true
	case stateCompanionProviderEndpoint:
		w.clearInput()
		return stateCompanionProviderType, true
	case stateCompanionProviderKey:
		w.clearInput()
		return stateCompanionProviderType, true
	case stateCompanionProviderTest:
		w.clearInput()
		w.companion.availableModels = nil
		return stateCompanionProviderType, true
	case stateCompanionModelSetup:
		w.commitEditorToField()
		w.clearInput()
		return w.companionModelSetupBackTarget(), true
	case stateMode:
		return w.modeBackTarget(), true
	case stateWorkflowPreview:
		w.previewYesNo = 1
		return statePromptPreview, true
	}
	return st, false
}

func (w *wizardComponent) companionModelSetupBackTarget() wizardState {
	if len(w.companion.availableModels) > 0 {
		return stateCompanionModelSelect
	}
	return stateCompanionProviderTest
}

func (w *wizardComponent) modeBackTarget() wizardState {
	if w.companionModelSelected {
		return stateCompanionModel
	}
	// If companion went through full provider/model setup, go back to the advanced screen
	if w.companion.providerID != "" || w.companion.modelID != "" {
		return stateCompanionModelAdvanced
	}
	return stateCompanionModelSetup
}

func (w *wizardComponent) clearInput() {
	w.inputMode = ""
	w.editor.Clear()
}

func (w *wizardComponent) modelSetupBackTarget() wizardState {
	s := w.currentSlot()
	if len(s.availableModels) > 0 {
		if w.state == stateCompanionModelSetup {
			return stateCompanionModelSelect
		}
		return stateModelSelect
	}
	if w.state == stateCompanionModelSetup {
		return stateCompanionProviderTest
	}
	return stateProviderTest
}

func (w *wizardComponent) commitEditorToField() {
	w.commitEditorToFieldFor(w.currentSlot())
}

func (w *wizardComponent) commitEditorToFieldFor(s *modelSlot) {
	switch s.modelFieldIdx {
	case 0:
		s.modelID = w.editor.Text()
	case 1:
		s.modelName = w.editor.Text()
	case 2:
		s.modelTemp = w.editor.Text()
	case 3:
		s.modelMaxTokens = w.editor.Text()
	}
}

func (w *wizardComponent) loadFieldIntoEditor() {
	s := w.currentSlot()
	switch s.modelFieldIdx {
	case 0:
		w.editor.SetText(s.modelID)
	case 1:
		w.editor.SetText(s.modelName)
	case 2:
		w.editor.SetText(s.modelTemp)
	case 3:
		w.editor.SetText(s.modelMaxTokens)
	}
}

func (w *wizardComponent) cycleAdvancedField(dir int) {
	if w.advancedMode == 0 {
		return
	}
	w.advancedFieldIdx += dir
	if w.advancedFieldIdx < 0 {
		w.advancedFieldIdx = 0
	}
	if w.advancedFieldIdx > 3 {
		w.advancedFieldIdx = 3
	}
}

func (w *wizardComponent) prepareAdvancedOptions() {
	if w.advancedMode == 0 {
		return
	}
	switch w.advancedFieldIdx {
	case 0:
		w.editor.SetText(w.compressMaxTokens)
	case 1:
		w.editor.SetText(w.compressThreshold)
	case 2:
		w.editor.SetText(w.maxToolRepeat)
	case 3:
		w.editor.SetText("")
	}
}

func (w *wizardComponent) commitTextInput() {
	s := w.currentSlot()
	switch w.inputMode {
	case "endpoint":
		s.endpoint = w.editor.Text()
		if s.selectedPresetIndex < 0 {
			s.providerID = DeriveProviderID(s.endpoint)
			s.providerName = deriveProviderName(s.endpoint)
		}
	case "apikey":
		s.apiKey = w.editor.Text()
	}
	w.editor.Clear()
}

func (w *wizardComponent) handleNumber(key string) {
	switch w.state {
	case stateProviderType:
		w.handleNumberProviderType(key)
	case stateModelSelect:
		w.handleNumberModelSelect(key)
	case stateWebFetchSummary:
		w.handleNumberWebFetchSummary(key)
	case stateMode:
		w.handleNumberMode(key)
	case stateSkillMode:
		w.handleNumberSkillMode(key)
	case stateAdvancedOptions:
		w.handleNumberAdvancedOptions(key)
	case stateModelAdvanced, stateCompanionModelAdvanced:
		w.handleNumberModelAdvanced(key)
	case statePromptPreview, stateWorkflowPreview:
		w.handleNumberPreview(key)
	}
}

func (w *wizardComponent) handleNumberModelAdvanced(key string) {
	s := w.currentSlot()
	switch key {
	case "1":
		s.modelReasoning = !s.modelReasoning
	case "2":
		levels := []string{"off", "minimal", "low", "medium", "high", "xhigh"}
		for i, l := range levels {
			if l == s.modelThinkingLevel {
				s.modelThinkingLevel = levels[(i+1)%len(levels)]
				return
			}
		}
		s.modelThinkingLevel = levels[0]
	}
}

func (w *wizardComponent) handleNumberWebFetchSummary(key string) {
	switch key {
	case "1":
		w.webfetchSummaryEnabled = 0
	case "2":
		w.webfetchSummaryEnabled = 1
	}
}

func (w *wizardComponent) handleNumberSkillMode(key string) {
	switch key {
	case "1":
		w.skillMode = 0
	case "2":
		w.skillMode = 1
	}
}

func (w *wizardComponent) handleNumberAdvancedOptions(key string) {
	switch key {
	case "1":
		w.advancedMode = (w.advancedMode + 1) % 2
	case "2":
		if w.advancedMode == 1 {
			w.compressEnabled = (w.compressEnabled + 1) % 2
		}
	case "3":
		if w.advancedMode == 1 {
			w.allowFuzzEdits = (w.allowFuzzEdits + 1) % 2
		}
	}
}

func (w *wizardComponent) handleNumberProviderType(key string) {
	presets := PresetProviders()
	idx := int(key[0]-'0') - 1
	if idx < 0 || idx > len(presets) {
		return
	}
	w.focusProvider(idx)
}

func (w *wizardComponent) handleNumberModelSelect(key string) {
	s := w.currentSlot()
	idx := int(key[0]-'0') - 1
	if idx >= 0 && idx < len(s.availableModels) {
		s.selectedModelIdx = idx
	}
}

func (w *wizardComponent) handleNumberMode(key string) {
	switch key {
	case "1":
		w.selectedMode = 0
	case "2":
		w.selectedMode = 1
	case "3":
		w.selectedMode = 2
	case "4":
		w.selectedMode = 3
	}
}

func (w *wizardComponent) handleNumberPreview(key string) {
	switch key {
	case "1":
		w.previewYesNo = 0
	case "2":
		w.previewYesNo = 1
	}
}

func (w *wizardComponent) handleUp() {
	switch w.state {
	case stateProviderType, stateCompanionProviderType:
		presets := PresetProviders()
		s := w.currentSlot()
		cur := s.selectedPresetIndex
		if cur < 0 {
			cur = len(presets)
		}
		w.focusProvider(cur - 1)
	case stateModelSelect, stateCompanionModelSelect:
		s := w.currentSlot()
		if len(s.availableModels) > 0 {
			s.selectedModelIdx = (s.selectedModelIdx - 1 + len(s.availableModels)) % len(s.availableModels)
		}
	case stateMode:
		w.selectedMode = (w.selectedMode - 1 + 4) % 4
	case stateSkillMode:
		w.skillMode = (w.skillMode - 1 + 2) % 2
	case stateAdvancedOptions:
		w.cycleAdvancedField(-1)
	case stateModelSetup, stateCompanionModelSetup:
		w.commitEditorToField()
		s := w.currentSlot()
		s.modelFieldIdx = (s.modelFieldIdx - 1 + modelFieldCount) % modelFieldCount
		w.loadFieldIntoEditor()
	case stateCompanionModel:
		w.companionModelSelected = !w.companionModelSelected
	case statePromptPreview, stateWorkflowPreview:
		w.previewYesNo = (w.previewYesNo - 1 + 2) % 2
	case stateWebFetchSummary:
		w.webfetchSummaryEnabled = (w.webfetchSummaryEnabled - 1 + 2) % 2
	}
}

func (w *wizardComponent) handleDown() {
	switch w.state {
	case stateProviderType, stateCompanionProviderType:
		presets := PresetProviders()
		s := w.currentSlot()
		cur := s.selectedPresetIndex
		if cur < 0 {
			cur = len(presets)
		}
		w.focusProvider(cur + 1)
	case stateModelSelect, stateCompanionModelSelect:
		s := w.currentSlot()
		if len(s.availableModels) > 0 {
			s.selectedModelIdx = (s.selectedModelIdx + 1) % len(s.availableModels)
		}
	case stateMode:
		w.selectedMode = (w.selectedMode + 1) % 4
	case stateSkillMode:
		w.skillMode = (w.skillMode + 1) % 2
	case stateAdvancedOptions:
		w.cycleAdvancedField(1)
	case stateModelSetup, stateCompanionModelSetup:
		w.commitEditorToField()
		s := w.currentSlot()
		s.modelFieldIdx = (s.modelFieldIdx + 1) % modelFieldCount
		w.loadFieldIntoEditor()
	case stateCompanionModel:
		w.companionModelSelected = !w.companionModelSelected
	case statePromptPreview, stateWorkflowPreview:
		w.previewYesNo = (w.previewYesNo + 1) % 2
	case stateWebFetchSummary:
		w.webfetchSummaryEnabled = (w.webfetchSummaryEnabled + 1) % 2
	}
}

func (w *wizardComponent) deriveDefaultModelName() string {
	presets := PresetProviders()
	if w.main.selectedPresetIndex >= 0 && w.main.selectedPresetIndex < len(presets) {
		if presets[w.main.selectedPresetIndex].DefaultModel != "" {
			return presets[w.main.selectedPresetIndex].DefaultModel
		}
	}
	return "gpt-4o"
}

func (w *wizardComponent) focusProvider(idx int) {
	presets := PresetProviders()
	total := len(presets) + 1
	idx = ((idx % total) + total) % total
	s := w.currentSlot()
	if idx == len(presets) {
		s.selectedPresetIndex = -1
		s.providerID = ""
		s.providerName = ""
		s.endpoint = ""
		return
	}
	p := presets[idx]
	s.selectedPresetIndex = idx
	s.providerID = p.ID
	s.providerName = p.Name
	s.endpoint = p.Endpoint
}

// -- Model fetching -----------------------------------------------

func (w *wizardComponent) fetchAvailableModels(s *modelSlot) {
	s.availableModels = nil
	if s.endpoint == "" {
		return
	}
	endpoint := modelsEndpoint(s.endpoint)
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return
	}
	if s.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return
	}
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return
	}
	for _, m := range result.Data {
		if m.ID != "" {
			s.availableModels = append(s.availableModels, m.ID)
		}
	}
}

func modelsEndpoint(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return endpoint + "/models"
	}
	u.Path = strings.TrimSuffix(u.Path, "/chat/completions")
	u.Path = strings.TrimRight(u.Path, "/") + "/models"
	return u.String()
}

// -- Rendering ----------------------------------------------------

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
