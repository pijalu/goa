// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"github.com/pijalu/goa/tui"
)

var wizardRenderers = map[wizardState]func(*wizardComponent, int) []string{
	stateWelcome:          (*wizardComponent).renderWelcome,
	stateProviderType:     (*wizardComponent).renderProviderType,
	stateProviderEndpoint: (*wizardComponent).renderProviderEndpoint,
	stateProviderKey:      (*wizardComponent).renderProviderKey,
	stateProviderTest:     (*wizardComponent).renderProviderTest,
	stateModelSelect:      (*wizardComponent).renderModelSelect,
	stateModelSetup:       (*wizardComponent).renderModelSetup,
	stateModelAdvanced:    (*wizardComponent).renderModelAdvanced,
	stateDone:             (*wizardComponent).renderDone,
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
	case len(data) == 1 && data[0] >= '1' && data[0] <= '9' && w.inputMode == "":
		// Quick-pick digits only apply on selection screens. While a text
		// editor is open (endpoint / API key / model ID) digits are content:
		// intercepting them here silently dropped every 1-9 from typed keys
		// and endpoints (bugs.md B42).
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
//
// Quick-start flow (bugs.md B42): welcome → provider → (endpoint) → (key) →
// test → model select/setup → model advanced (thinking level, default high)
// → done. Everything else — companion, execution mode, skill mode, webfetch
// summary, dreams, advanced options, prompt/workflow copying — applies its
// quick-start default without a question (see saveConfig).

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
	default:
		return false
	}
	return true
}

func (w *wizardComponent) advancePostModel() bool {
	switch w.state {
	case stateModelAdvanced:
		w.state = stateDone
		w.saveConfig()
	default:
		return false
	}
	return true
}

// currentSlot returns the active modelSlot. The quick-start wizard configures
// only the main slot; kept as a seam so a future flow can re-introduce slots
// without touching every transition.
func (w *wizardComponent) currentSlot() *modelSlot {
	return &w.main
}

func (w *wizardComponent) advanceFromEndpoint() {
	// commitTextInput is the single commit point: it copies the editor into
	// the slot (deriving the provider identity for custom endpoints) and
	// clears the editor. Re-reading the editor afterwards returned the
	// already-cleared text and lost the endpoint (bugs.md B42).
	w.commitTextInput()
	w.editor.Clear()
	if w.state == stateProviderEndpoint {
		w.state = stateProviderKey
		w.startKeyInput(w.currentSlot())
		return
	}
	w.state = stateProviderKey
	w.startKeyInput(w.currentSlot())
}

func (w *wizardComponent) advanceFromKey() {
	// Single commit point, as in advanceFromEndpoint: commitTextInput already
	// stores the typed key and clears the editor; re-reading the cleared
	// editor emptied the saved API key (bugs.md B42).
	w.commitTextInput()
	w.editor.Clear()
	w.state = stateProviderTest
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
		w.state = stateModelSelect
		return
	}
	w.initModelSetup("")
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
		w.state = stateModelAdvanced
		return
	}
	w.initModelSetup("")
}

func (w *wizardComponent) advanceFromModelSetup() {
	w.commitEditorToField()
	w.inputMode = ""
	w.editor.Clear()
	w.state = stateModelAdvanced
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
	w.state = stateModelSetup
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
