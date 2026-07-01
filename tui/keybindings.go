// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

// KeybindingDef defines a keybinding with its default keys and description.
type KeybindingDef struct {
	DefaultKeys []string
	Description string
}

// Keybinding name constants.
const (
	// Editor navigation
	KbCursorUp        = "editor.cursorUp"
	KbCursorDown      = "editor.cursorDown"
	KbCursorLeft      = "editor.cursorLeft"
	KbCursorRight     = "editor.cursorRight"
	KbCursorWordLeft  = "editor.cursorWordLeft"
	KbCursorWordRight = "editor.cursorWordRight"
	KbCursorLineStart = "editor.cursorLineStart"
	KbCursorLineEnd   = "editor.cursorLineEnd"
	KbPageUp          = "editor.pageUp"
	KbPageDown        = "editor.pageDown"
	// Editor editing
	KbDeleteBackward  = "editor.deleteCharBackward"
	KbDeleteForward   = "editor.deleteCharForward"
	KbDeleteWordBack  = "editor.deleteWordBackward"
	KbDeleteWordFwd   = "editor.deleteWordForward"
	KbDeleteLineStart = "editor.deleteToLineStart"
	KbDeleteLineEnd   = "editor.deleteToLineEnd"
	KbYank            = "editor.yank"
	KbYankPop         = "editor.yankPop"
	KbUndo            = "editor.undo"
	KbRedo            = "editor.redo"
	// Input actions
	KbNewLine = "input.newLine"
	KbSubmit  = "input.submit"
	KbTab     = "input.tab"
	// Delete last message
	KbDeleteLastMsg = "app.messages.deleteLast"
	// Selection
	KbSelectUp       = "select.up"
	KbSelectDown     = "select.down"
	KbSelectPageUp   = "select.pageUp"
	KbSelectPageDown = "select.pageDown"
	KbSelectConfirm  = "select.confirm"
	KbSelectCancel   = "select.cancel"
	// Tool display
	KbToggleExpand = "app.tools.expand"
	// App-level quick actions
	KbCycleThinkingLevel   = "app.thinking.cycle"
	KbChangeMode           = "app.mode.change"
	KbOpenModeSelector     = "app.mode.select"
	KbCycleAutonomy        = "app.autonomy.cycle"
	KbChangeModel          = "app.model.select"
	KbToggleThinkingBlocks = "app.thinking.toggle"
)

// DefaultKeybindings returns the default keybinding definitions.
func DefaultKeybindings() map[string]KeybindingDef {
	return map[string]KeybindingDef{
		KbCursorUp:             {[]string{KeyUp, "ctrl+p"}, "Move cursor up"},
		KbCursorDown:           {[]string{KeyDown, "ctrl+n"}, "Move cursor down"},
		KbCursorLeft:           {[]string{KeyLeft, "ctrl+b"}, "Move cursor left"},
		KbCursorRight:          {[]string{KeyRight, "ctrl+f"}, "Move cursor right"},
		KbCursorWordLeft:       {[]string{"alt+left", "ctrl+left", "alt+b"}, "Move cursor word left"},
		KbCursorWordRight:      {[]string{"alt+right", "ctrl+right", "alt+f"}, "Move cursor word right"},
		KbCursorLineStart:      {[]string{KeyHome, KeyCtrlA}, "Move to line start"},
		KbCursorLineEnd:        {[]string{KeyEnd, KeyCtrlE}, "Move to line end"},
		KbPageUp:               {[]string{KeyPageUp}, "Page up"},
		KbPageDown:             {[]string{KeyPageDown}, "Page down"},
		KbDeleteBackward:       {[]string{KeyBackspace}, "Delete character backward"},
		KbDeleteForward:        {[]string{KeyDelete, KeyCtrlD}, "Delete character forward"},
		KbDeleteWordBack:       {[]string{KeyCtrlW, "alt+backspace", "ctrl+backspace"}, "Delete word backward"},
		KbDeleteWordFwd:        {[]string{"alt+d", "alt+delete"}, "Delete word forward"},
		KbDeleteLineStart:      {[]string{KeyCtrlU}, "Delete to line start"},
		KbDeleteLineEnd:        {[]string{KeyCtrlK}, "Delete to line end"},
		KbYank:                 {[]string{KeyCtrlY}, "Yank"},
		KbYankPop:              {[]string{"alt+y"}, "Yank pop"},
		KbUndo:                 {[]string{"ctrl+-"}, "Undo"},
		KbNewLine:              {[]string{"shift+enter", "ctrl+enter", "alt+enter"}, "Insert newline"},
		KbSubmit:               {[]string{KeyEnter}, "Submit input"},
		KbTab:                  {[]string{KeyTab}, "Tab / autocomplete"},
		KbSelectUp:             {[]string{KeyUp}, "Move selection up"},
		KbSelectDown:           {[]string{KeyDown}, "Move selection down"},
		KbSelectPageUp:         {[]string{KeyPageUp}, "Selection page up"},
		KbSelectPageDown:       {[]string{KeyPageDown}, "Selection page down"},
		KbSelectConfirm:        {[]string{KeyEnter}, "Confirm selection"},
		KbSelectCancel:         {[]string{KeyEscape, KeyCtrlC}, "Cancel selection"},
		KbDeleteLastMsg:        {[]string{"ctrl+shift+backspace"}, "Delete last chat message"},
		KbToggleExpand:         {[]string{"ctrl+o"}, "Toggle tool/output expand/collapse"},
		KbCycleThinkingLevel:   {[]string{KeyShiftTab}, "Cycle thinking level"},
		KbChangeMode:           {[]string{"alt+m"}, "Cycle major mode"},
		KbOpenModeSelector:     {[]string{"alt+o"}, "Open the mode selector"},
		KbCycleAutonomy:        {[]string{"ctrl+shift+m"}, "Cycle autonomy level"},
		KbChangeModel:          {[]string{KeyCtrlL}, "Open model selector"},
		KbToggleThinkingBlocks: {[]string{KeyCtrlT}, "Toggle thinking blocks"},
	}
}

// KeybindingsManager manages keybinding definitions and matching.
type KeybindingsManager struct {
	bindings map[string]KeybindingDef
}

// NewKeybindingsManager creates a manager with the given definitions.
func NewKeybindingsManager(defs map[string]KeybindingDef) *KeybindingsManager {
	return &KeybindingsManager{bindings: defs}
}

// DefaultKeybindingsManager returns a manager with default bindings.
func DefaultKeybindingsManager() *KeybindingsManager {
	return NewKeybindingsManager(DefaultKeybindings())
}

// Matches checks if the given key string matches a named keybinding.
func (m *KeybindingsManager) Matches(data string, keybinding string) bool {
	def, ok := m.bindings[keybinding]
	if !ok {
		return false
	}
	for _, k := range def.DefaultKeys {
		if data == k {
			return true
		}
	}
	return false
}

// Keys returns the default keys for a named keybinding.
func (m *KeybindingsManager) Keys(keybinding string) []string {
	def, ok := m.bindings[keybinding]
	if !ok {
		return nil
	}
	return def.DefaultKeys
}
