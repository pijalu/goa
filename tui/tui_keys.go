// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

func (t *TUI) SetKeyLog(path string) error {
	kl, err := newKeyLogger(path)
	if err != nil {
		return err
	}
	t.keyLog = kl
	return nil
}

// logKey enqueues a formatted trace line when keystroke tracing is enabled.
func (t *TUI) logKey(format string, args ...any) {
	if t.keyLog == nil {
		return
	}
	t.keyLog.logf(format, args...)
}

func (t *TUI) handleKey(data string) {
	key := decodeKeyForRouting(data)
	focused := t.Focused()

	t.logKey("raw=%q key=%q focused=%T\n", data, key, focused)

	if t.handleTrappedInput(key, focused) {
		t.logKey("  → trapped\n")
		return
	}
	// Key release events (Kitty protocol) must be dropped before any routing.
	if t.ignoreKeyRelease(data, focused) {
		t.logKey("  → keyRelease\n")
		return
	}
	if t.routeToCapturingOverlay(data, key) {
		t.logKey("  → overlay\n")
		return
	}
	if t.handleDeleteLastKeys(key, focused) {
		t.logKey("  → deleteLastKeys\n")
		return
	}
	if t.handleCtrlC(key, focused) {
		t.logKey("  → ctrlc\n")
		return
	}
	if t.handleAppShortcuts(key) {
		t.logKey("  → appShortcut\n")
		return
	}

	if focused != nil {
		t.logKey("  → %T.HandleInput\n", focused)
		focused.HandleInput(key)
		t.RequestRender()
	}
}

// handleTrappedInput gives the focused component a chance to consume global
// keys such as Ctrl+C or Escape before any other routing.
func (t *TUI) handleTrappedInput(key string, focused Component) bool {
	if trap, ok := focused.(InputTrap); ok && trap.TrapInput(key) {
		t.RequestRender()
		return true
	}
	return false
}

// ignoreKeyRelease filters Kitty key-release events unless the focused
// component explicitly asks for them.
func (t *TUI) ignoreKeyRelease(data string, focused Component) bool {
	if !isKeyRelease(data) {
		return false
	}
	if f, ok := focused.(KeyReleaseAware); ok && f.WantsKeyRelease() {
		return false
	}
	return true
}

// handleDeleteLastKeys routes Ctrl+Backspace / Ctrl+Shift+Backspace to either
// the focused editor or the application-level "delete last message" callback.
func (t *TUI) handleDeleteLastKeys(key string, focused Component) bool {
	if matchesKey(key, "ctrl+shift+backspace") || matchesKey(key, "\x1b[3;6~") {
		if t.OnDeleteLast != nil {
			t.OnDeleteLast()
			t.RequestRender()
		}
		return true
	}
	if matchesKey(key, "ctrl+backspace") || matchesKey(key, "\x1b[3;5~") {
		if ed, ok := focused.(*Editor); ok && ed.Text() != "" {
			ed.HandleInput(key)
			t.RequestRender()
			return true
		}
		if t.OnDeleteLast != nil {
			t.OnDeleteLast()
			t.RequestRender()
			return true
		}
	}
	return false
}

// handleCtrlC clears the focused input when it has content; otherwise it stops
// the TUI.
func (t *TUI) handleCtrlC(key string, focused Component) bool {
	if !matchesKey(key, KeyCtrlC) {
		return false
	}
	if ed, ok := focused.(*Editor); ok && ed.Text() != "" {
		ed.Clear()
		t.RequestRender()
		return true
	}
	if ed, ok := focused.(*Input); ok && ed.Text() != "" {
		ed.Clear()
		t.RequestRender()
		return true
	}
	// Editor is empty: give the host a chance to cancel a pending main-input
	// request (e.g. /goal prompt) instead of quitting the application.
	if t.OnCancelInputRequest != nil && t.OnCancelInputRequest() {
		t.RequestRender()
		return true
	}
	t.Stop()
	return true
}

// handleAppShortcuts handles Ctrl+O expand/collapse, Ctrl+G goal-bubble toggle,
// Alt+M mode change, Shift+Tab thinking-level cycle, Ctrl+L model selector,
// Ctrl+T thinking-blocks toggle.
func (t *TUI) handleAppShortcuts(key string) bool {
	if t.handleToggleExpand(key) {
		return true
	}
	fn, ok := t.resolveAppShortcut(key)
	if !ok {
		return false
	}
	t.invokeCallback(fn)
	return true
}

// resolveAppShortcut maps a decoded key to its application-level callback.
// It accounts for terminals that emit an alt+printable character instead of
// the ESC+<base> sequence for Option-key combinations on macOS. A flat table
// keeps the dispatch cyclomatic-complexity low (one loop, no big switch).
func (t *TUI) resolveAppShortcut(key string) (func(), bool) {
	altKey := altKeyName(key)
	for _, sc := range appShortcuts {
		if sc.matches(key, altKey) {
			return sc.callback(t), true
		}
	}
	// Plugin-registered hotkeys take lowest precedence (built-ins win ties).
	if fn, ok := t.resolvePluginHotkey(key); ok {
		return fn, true
	}
	return nil, false
}

// resolvePluginHotkey matches a decoded key against plugin-registered
// hotkeys. Returns the handler (invoked on the TUI loop, but the handler
// itself dispatches onto the plugin VM) and true on match.
func (t *TUI) resolvePluginHotkey(key string) (func(), bool) {
	t.pluginHotkeysMu.RLock()
	defer t.pluginHotkeysMu.RUnlock()
	for _, hk := range t.pluginHotkeys {
		if matchesKey(key, hk.keyName) {
			return hk.handler, hk.handler != nil
		}
	}
	return nil, false
}

// pluginHotkey is one plugin-registered keyboard shortcut.
type pluginHotkey struct {
	keyName string // canonical TUI key name, e.g. "ctrl+shift+q"
	handler func()
}

// RegisterPluginHotkey adds a plugin hotkey by canonical key name
// (e.g. "ctrl+shift+q"). Built-in shortcuts take precedence; a plugin key
// that collides with a built-in simply never fires. Re-registering the same
// key replaces the handler. Safe to call from the plugin runner.
func (t *TUI) RegisterPluginHotkey(keyName string, handler func()) {
	t.pluginHotkeysMu.Lock()
	defer t.pluginHotkeysMu.Unlock()
	for i, hk := range t.pluginHotkeys {
		if hk.keyName == keyName {
			t.pluginHotkeys[i].handler = handler
			return
		}
	}
	t.pluginHotkeys = append(t.pluginHotkeys, pluginHotkey{keyName: keyName, handler: handler})
}

// appShortcut is one application-level keybinding: a set of accepted key names
// (plus the macOS Option-alias form) and the callback it resolves to.
type appShortcut struct {
	keys     []string // exact key names (and alt+uppercase variants)
	altAlias string   // optional macOS Option-key alias (e.g. "alt+m")
	callback func(t *TUI) func()
}

func (s appShortcut) matches(key, altKey string) bool {
	for _, k := range s.keys {
		if matchesKey(key, k) {
			return true
		}
	}
	return s.altAlias != "" && matchesKey(altKey, s.altAlias)
}

// appShortcuts is the ordered table consumed by resolveAppShortcut.
var appShortcuts = []appShortcut{
	{keys: []string{"ctrl+g"}, callback: func(t *TUI) func() { return t.OnToggleGoalBubble }},
	{keys: []string{"alt+e", "alt+E"}, altAlias: "alt+e", callback: func(t *TUI) func() { return t.OnEditSteering }},
	{keys: []string{"alt+m", "alt+M"}, altAlias: "alt+m", callback: func(t *TUI) func() { return t.OnChangeMode }},
	{keys: []string{"alt+o", "alt+O"}, altAlias: "alt+o", callback: func(t *TUI) func() { return t.OnOpenModeSelector }},
	{keys: []string{"ctrl+shift+m"}, callback: func(t *TUI) func() { return t.OnCycleAutonomy }},
	{keys: []string{KeyShiftTab}, callback: func(t *TUI) func() { return t.OnCycleThinkingLevel }},
	{keys: []string{KeyCtrlL}, callback: func(t *TUI) func() { return t.OnChangeModel }},
	{keys: []string{KeyCtrlT}, callback: func(t *TUI) func() { return t.OnToggleThinkingBlocks }},
	{keys: []string{"ctrl+x"}, callback: func(t *TUI) func() { return t.OnOpenAgentTabs }},
}

func (t *TUI) invokeCallback(fn func()) {
	if fn != nil {
		fn()
		t.RequestRender()
	}
}

// decodeKeyForRouting converts raw terminal bytes into a key name for
// matching, but preserves raw text/paste events so multi-character input is
// not split into individual key presses.
func decodeKeyForRouting(data string) string {
	// Multi-character data that does not begin with an escape sequence is raw
	// text from a bracketed paste (or similar bulk input). Pass it through
	// unchanged so components can detect and handle pastes.
	if len(data) > 1 && data[0] != '\x1b' {
		return data
	}
	decoded := decodeKeys([]byte(data))
	if len(decoded) > 0 && decoded[0] != "" {
		return decoded[0]
	}
	return data
}

// routeToCapturingOverlay sends input to the topmost capturing overlay, if
// any. Returns true when the input was consumed by the overlay.
func (t *TUI) routeToCapturingOverlay(data, key string) bool {
	t.overlayMu.RLock()
	var top *overlayEntry
	if n := len(t.overlayStack); n > 0 {
		top = t.overlayStack[n-1]
	}
	t.overlayMu.RUnlock()
	if top == nil || !top.opts.CaptureInput {
		return false
	}
	// Overlays receive the decoded key name for control keys, but raw data for
	// pasted text so their own paste handling can run.
	if len(data) > 1 && data[0] != '\x1b' {
		top.comp.HandleInput(data)
	} else {
		top.comp.HandleInput(key)
	}
	t.RequestRender()
	return true
}

// ShowSelector displays a channel-based interactive selector as an overlay.
// The caller blocks on the returned channel until the user selects or cancels.
// title is shown at the top; currentValue is marked with a ✓ indicator.
