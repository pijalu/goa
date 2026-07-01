// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"sync"
)

// PaneFactory creates a TUI pane instance.
type PaneFactory func() interface{}

// SegmentFactory creates a mode line segment instance.
type SegmentFactory func() interface{}

// ModalFactory creates a modal dialog instance.
type ModalFactory func() interface{}

// KeyBindingDef defines a keybinding for the TUI keymap.
type KeyBindingDef struct {
	Keys        string
	Description string
	Context     string // "normal", "input", or "" for all
}

// UICommandDef defines a TUI command (for command palette / M-x).
type UICommandDef struct {
	Name        string
	Description string
	Category    string
	Handler     func() error
}

// ThemeTokenDef defines a theme token override.
type ThemeTokenDef struct {
	Token string
	Color string
}

// EventHandler processes an event payload.
type EventHandler func(event interface{})

// ExtensionRegistry is the central plugin callback registry.
// All methods are thread-safe.
type ExtensionRegistry struct {
	mu            sync.RWMutex
	panes         map[string]PaneFactory
	segments      map[string]SegmentFactory
	modals        map[string]ModalFactory
	keyBindings   []KeyBindingDef
	uiCommands    []UICommandDef
	themeTokens   []ThemeTokenDef
	eventHandlers map[string][]EventHandler
}

// NewExtensionRegistry creates an empty ExtensionRegistry.
func NewExtensionRegistry() *ExtensionRegistry {
	return &ExtensionRegistry{
		panes:         make(map[string]PaneFactory),
		segments:      make(map[string]SegmentFactory),
		modals:        make(map[string]ModalFactory),
		keyBindings:   make([]KeyBindingDef, 0),
		uiCommands:    make([]UICommandDef, 0),
		themeTokens:   make([]ThemeTokenDef, 0),
		eventHandlers: make(map[string][]EventHandler),
	}
}

// RegisterPane adds a pane factory.
func (er *ExtensionRegistry) RegisterPane(id string, factory PaneFactory) {
	er.mu.Lock()
	defer er.mu.Unlock()
	er.panes[id] = factory
}

// RegisterSegment adds a mode line segment factory.
func (er *ExtensionRegistry) RegisterSegment(id string, factory SegmentFactory) {
	er.mu.Lock()
	defer er.mu.Unlock()
	er.segments[id] = factory
}

// RegisterModal adds a modal factory.
func (er *ExtensionRegistry) RegisterModal(id string, factory ModalFactory) {
	er.mu.Lock()
	defer er.mu.Unlock()
	er.modals[id] = factory
}

// RegisterKeyBinding adds a keybinding definition.
func (er *ExtensionRegistry) RegisterKeyBinding(binding KeyBindingDef) {
	er.mu.Lock()
	defer er.mu.Unlock()
	er.keyBindings = append(er.keyBindings, binding)
}

// RegisterEventHandler adds an event handler for the given event type.
// Multiple handlers can be registered for the same event type.
func (er *ExtensionRegistry) RegisterEventHandler(eventType string, handler EventHandler) {
	er.mu.Lock()
	defer er.mu.Unlock()
	er.eventHandlers[eventType] = append(er.eventHandlers[eventType], handler)
}

// RegisterCommand adds a UI command definition.
func (er *ExtensionRegistry) RegisterCommand(cmd UICommandDef) {
	er.mu.Lock()
	defer er.mu.Unlock()
	er.uiCommands = append(er.uiCommands, cmd)
}

// RegisterThemeToken adds a theme token override.
func (er *ExtensionRegistry) RegisterThemeToken(token ThemeTokenDef) {
	er.mu.Lock()
	defer er.mu.Unlock()
	er.themeTokens = append(er.themeTokens, token)
}

// --- Read methods ---

// PaneFactories returns a copy of all registered pane factories.
func (er *ExtensionRegistry) PaneFactories() map[string]PaneFactory {
	er.mu.RLock()
	defer er.mu.RUnlock()
	out := make(map[string]PaneFactory, len(er.panes))
	for k, v := range er.panes {
		out[k] = v
	}
	return out
}

// SegmentFactories returns a copy of all registered segment factories.
func (er *ExtensionRegistry) SegmentFactories() map[string]SegmentFactory {
	er.mu.RLock()
	defer er.mu.RUnlock()
	out := make(map[string]SegmentFactory, len(er.segments))
	for k, v := range er.segments {
		out[k] = v
	}
	return out
}

// ModalFactories returns a copy of all registered modal factories.
func (er *ExtensionRegistry) ModalFactories() map[string]ModalFactory {
	er.mu.RLock()
	defer er.mu.RUnlock()
	out := make(map[string]ModalFactory, len(er.modals))
	for k, v := range er.modals {
		out[k] = v
	}
	return out
}

// KeyBindings returns a copy of all registered keybindings.
func (er *ExtensionRegistry) KeyBindings() []KeyBindingDef {
	er.mu.RLock()
	defer er.mu.RUnlock()
	out := make([]KeyBindingDef, len(er.keyBindings))
	copy(out, er.keyBindings)
	return out
}

// UICommands returns a copy of all registered UI commands.
func (er *ExtensionRegistry) UICommands() []UICommandDef {
	er.mu.RLock()
	defer er.mu.RUnlock()
	out := make([]UICommandDef, len(er.uiCommands))
	copy(out, er.uiCommands)
	return out
}

// ThemeTokens returns a copy of all registered theme tokens.
func (er *ExtensionRegistry) ThemeTokens() []ThemeTokenDef {
	er.mu.RLock()
	defer er.mu.RUnlock()
	out := make([]ThemeTokenDef, len(er.themeTokens))
	copy(out, er.themeTokens)
	return out
}

// FireEvent dispatches an event to all registered handlers for the event type.
func (er *ExtensionRegistry) FireEvent(eventType string, payload interface{}) {
	er.mu.RLock()
	handlers, ok := er.eventHandlers[eventType]
	er.mu.RUnlock()
	if !ok {
		return
	}
	for _, h := range handlers {
		h(payload)
	}
}
