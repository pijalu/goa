// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
)

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
	stateDreamModel:             stateWebFetchSummary,
	stateCompanionModel:         stateDreamModel,
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
	if w.companionUseMainModel == 1 {
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
	case stateMode:
		w.handleNumberMode(key)
	case stateAdvancedOptions:
		w.handleNumberAdvancedOptions(key)
	case stateModelAdvanced, stateCompanionModelAdvanced:
		w.handleNumberModelAdvanced(key)
	default:
		// All two-option screens share one quick-pick path.
		w.pickYesNo(key)
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

func (w *wizardComponent) handleUp()   { w.handleVertical(-1) }
func (w *wizardComponent) handleDown() { w.handleVertical(1) }

// handleVertical moves the current screen's selection by dir (-1 up, +1 down)
// with wraparound. All wizard option lists are cyclic.
func (w *wizardComponent) handleVertical(dir int) {
	// Two-option (Yes/No) screens all cycle a single 0/1 field.
	if s := w.yesNoScreen(); s != nil {
		cycleYesNo(s.field, dir)
		return
	}
	switch w.state {
	case stateProviderType, stateCompanionProviderType:
		presets := PresetProviders()
		s := w.currentSlot()
		cur := s.selectedPresetIndex
		if cur < 0 {
			cur = len(presets)
		}
		w.focusProvider(cur + dir)
	case stateModelSelect, stateCompanionModelSelect:
		s := w.currentSlot()
		if n := len(s.availableModels); n > 0 {
			s.selectedModelIdx = (s.selectedModelIdx + dir + n) % n
		}
	case stateMode:
		w.selectedMode = (w.selectedMode + dir + 4) % 4
	case stateAdvancedOptions:
		w.cycleAdvancedField(dir)
	case stateModelSetup, stateCompanionModelSetup:
		w.commitEditorToField()
		s := w.currentSlot()
		s.modelFieldIdx = (s.modelFieldIdx + dir + modelFieldCount) % modelFieldCount
		w.loadFieldIntoEditor()
	}
}

// yesNoScreen binds a two-option wizard screen to its selection field and the
// field value that highlights the first listed option (e.g. "Yes"). Field
// values are always 0/1; which of the two means "first option" differs per
// screen (enabled-style fields use 1, index-style fields use 0).
type yesNoScreen struct {
	field *int
	first int
}

// yesNoScreen returns the descriptor for the current two-option screen, or
// nil when the current state is not a two-option choice.
func (w *wizardComponent) yesNoScreen() *yesNoScreen {
	switch w.state {
	case stateSkillMode:
		return &yesNoScreen{&w.skillMode, 0} // 1st = sub-agent
	case statePromptPreview, stateWorkflowPreview:
		return &yesNoScreen{&w.previewYesNo, 0} // 1st = Yes
	case stateWebFetchSummary:
		return &yesNoScreen{&w.webfetchSummaryEnabled, 1} // 1st = Yes
	case stateDreamModel:
		return &yesNoScreen{&w.dreamEnabled, 1} // 1st = Yes
	case stateCompanionModel:
		return &yesNoScreen{&w.companionUseMainModel, 1} // 1st = Yes
	}
	return nil
}

// pickYesNo handles a quick-pick key on a two-option screen: "1" selects the
// first listed option, "2" the second, matching the rendered order.
func (w *wizardComponent) pickYesNo(key string) {
	s := w.yesNoScreen()
	if s == nil {
		return
	}
	switch key {
	case "1":
		*s.field = s.first
	case "2":
		*s.field = 1 - s.first
	}
}

// cycleYesNo moves a two-option selection (0/1) by dir positions with wrap.
func cycleYesNo(v *int, dir int) {
	*v = (*v + dir + 2) % 2
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
