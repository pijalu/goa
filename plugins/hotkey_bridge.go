// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import "sync"

// HotkeyDef describes a plugin-registered keyboard shortcut.
type HotkeyDef struct {
	Key         string // base key name, e.g. "q", "f5"
	Ctrl        bool
	Alt         bool
	Shift       bool
	Description string
	Handler     func() // runs on the plugin runner
}

// HotkeyBridge collects hotkeys plugins register via goa.registerHotkey.
// The TUI polls Registered() during its key routing; handlers dispatch onto
// the plugin runner so a hotkey never executes on the TUI goroutine.
type HotkeyBridge struct {
	mu      sync.Mutex
	hotkeys []HotkeyDef
}

// NewHotkeyBridge creates an empty bridge.
func NewHotkeyBridge() *HotkeyBridge {
	return &HotkeyBridge{}
}

// Register adds a hotkey. The Key is normalized to the TUI key-name form
// (e.g. ctrl+shift+q) by the caller before storing.
func (b *HotkeyBridge) Register(def HotkeyDef) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.hotkeys = append(b.hotkeys, def)
}

// Registered returns all hotkeys (copy, safe for the TUI to iterate).
func (b *HotkeyBridge) Registered() []HotkeyDef {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]HotkeyDef, len(b.hotkeys))
	copy(out, b.hotkeys)
	return out
}

// KeyName builds the canonical TUI key name for a def (e.g. "ctrl+shift+q").
func (d HotkeyDef) KeyName() string {
	name := ""
	if d.Ctrl {
		name += "ctrl+"
	}
	if d.Alt {
		name += "alt+"
	}
	if d.Shift {
		name += "shift+"
	}
	return name + d.Key
}
