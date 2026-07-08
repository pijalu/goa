// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

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
