// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import "strings"

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
// Alt+M mode change, Tab/Shift+Tab multi-agent tab cycling, Alt+T thinking-level
// cycle, Ctrl+L model selector, Ctrl+T thinking-blocks toggle.
func (t *TUI) handleAppShortcuts(key string) bool {
	if t.handleToggleExpand(key) {
		return true
	}
	// Tab is shared between agent-tab cycling and editor completion (the
	// opencode convention): a visible completion popup owns Tab, so the
	// shortcut layer must yield it to the focused editor.
	if matchesKey(key, KeyTab) && t.editorCompletionOwnsTab() {
		return false
	}
	fn, ok := t.resolveAppShortcut(key)
	if !ok {
		return false
	}
	t.invokeCallback(fn)
	return true
}

// editorCompletionOwnsTab reports whether the focused component is an editor
// with an active completion popup (Tab then accepts the selected candidate).
func (t *TUI) editorCompletionOwnsTab() bool {
	ed, ok := t.Focused().(*Editor)
	return ok && ed.AutoCompActive()
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
	// Multi-agent tab cycling (Tab/Shift+Tab + Alt+]/[ aliases): resolved
	// conditionally so an unbound host callback never swallows the key. The
	// macOS Option-char alias is passed so Option+[ / Option+] (which deliver
	// the literal '“'/'‘' under the default keyboard layout) resolve to the
	// alt+[/alt+] aliases exactly like the ESC-prefixed form.
	if fn, ok := t.resolveAgentTabCycle(key, altKey); ok {
		return fn, true
	}
	// Alt+<digit> jumps: also conditional, after the flat table so an
	// explicit built-in binding always wins.
	if fn, ok := t.resolveAgentTabJump(key, altKey); ok {
		return fn, true
	}
	// Plugin-registered hotkeys take lowest precedence (built-ins win ties).
	if fn, ok := t.resolvePluginHotkey(key); ok {
		return fn, true
	}
	return nil, false
}

// resolveAgentTabCycle maps Tab/Shift+Tab (and the Alt+]/Alt+[ aliases) to
// the multi-agent tab strip callbacks. Kept out of the flat appShortcuts
// table because these keys are only consumed when the host actually bound a
// handler — an unbound entry must fall through (e.g. plain Tab reaching the
// focused editor).
func (t *TUI) resolveAgentTabCycle(key, altKey string) (func(), bool) {
	switch {
	case matchesKey(key, KeyTab) && t.OnAgentTabNext != nil:
		return t.OnAgentTabNext, true
	case matchesKey(key, KeyShiftTab) && t.OnAgentTabPrev != nil:
		return t.OnAgentTabPrev, true
	case (matchesKey(key, "alt+]") || matchesKey(altKey, "alt+]")) && t.OnAgentTabNext != nil:
		return t.OnAgentTabNext, true
	case (matchesKey(key, "alt+[") || matchesKey(altKey, "alt+[")) && t.OnAgentTabPrev != nil:
		return t.OnAgentTabPrev, true
	}
	return nil, false
}

// resolveAgentTabJump maps "alt+1".."alt+9" to a zero-based OnAgentTabDigit
// jump. Kept out of the flat appShortcuts table so the jump does not need
// nine near-identical rows; returns false when the key is not a digit jump
// or no handler is bound.
func (t *TUI) resolveAgentTabJump(key, altKey string) (func(), bool) {
	// Prefer the canonical ESC-prefixed form; fall back to the macOS
	// Option-char alias (Option+1..9 deliver '¡™£¢∞§¶•ª' under the default
	// keyboard layout) so the digit jump works on Mac too.
	name := key
	if !(len(name) == 5 && strings.HasPrefix(name, "alt+")) {
		name = altKey
	}
	if t.OnAgentTabDigit == nil || len(name) != 5 || !strings.HasPrefix(name, "alt+") {
		return nil, false
	}
	d := name[4]
	if d < '1' || d > '9' {
		return nil, false
	}
	idx := int(d - '1')
	return func() { t.OnAgentTabDigit(idx) }, true
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
	// Multi-agent tab strip navigation (T5, opencode convention): Tab next /
	// Shift+Tab previous — works on every keyboard, no Option needed on
	// macOS — with Alt+]/Alt+[ as aliases. These four are resolved
	// conditionally in resolveAgentTabCycle (Tab yields to a visible editor
	// completion popup; unbound handlers must not swallow the key), and
	// Alt+<digit> jumps in resolveAgentTabJump.
	{keys: []string{"alt+t"}, altAlias: "alt+t", callback: func(t *TUI) func() { return t.OnCycleThinkingLevel }},
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
