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
	if target, ok := staticPreviousStates[st]; ok {
		return target
	}
	if target, ok := w.dynamicPreviousState(st); ok {
		return target
	}
	return st
}

var staticPreviousStates = map[wizardState]wizardState{
	stateModelSelect:    stateProviderTest,
	stateModelAdvanced:  stateModelSetup,
	stateDone:           stateModelAdvanced,
}

func (w *wizardComponent) dynamicPreviousState(st wizardState) (wizardState, bool) {
	switch st {
	case stateModelSetup:
		w.commitEditorToField()
		w.clearInput()
		return w.modelSetupBackTarget(), true
	}
	return st, false
}

func (w *wizardComponent) clearInput() {
	w.inputMode = ""
	w.editor.Clear()
}

func (w *wizardComponent) modelSetupBackTarget() wizardState {
	s := w.currentSlot()
	if len(s.availableModels) > 0 {
		return stateModelSelect
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
	case stateModelAdvanced:
		w.handleNumberModelAdvanced(key)
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

func (w *wizardComponent) handleUp()   { w.handleVertical(-1) }
func (w *wizardComponent) handleDown() { w.handleVertical(1) }

// handleVertical moves the current screen's selection by dir (-1 up, +1 down)
// with wraparound. All wizard option lists are cyclic.
func (w *wizardComponent) handleVertical(dir int) {
	switch w.state {
	case stateProviderType:
		presets := PresetProviders()
		s := w.currentSlot()
		cur := s.selectedPresetIndex
		if cur < 0 {
			cur = len(presets)
		}
		w.focusProvider(cur + dir)
	case stateModelSelect:
		s := w.currentSlot()
		if n := len(s.availableModels); n > 0 {
			s.selectedModelIdx = (s.selectedModelIdx + dir + n) % n
		}
	case stateModelSetup:
		w.commitEditorToField()
		s := w.currentSlot()
		s.modelFieldIdx = (s.modelFieldIdx + dir + modelFieldCount) % modelFieldCount
		w.loadFieldIntoEditor()
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
