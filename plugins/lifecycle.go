// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

// HookType identifies lifecycle events that plugins can observe.
type HookType string

const (
	HookStart     HookType = "start"
	HookShutdown  HookType = "shutdown"
	HookToolCall  HookType = "tool_call"
	HookToolDone  HookType = "tool_done"
	HookModeEnter HookType = "mode_enter"
)

// LifecycleHandler is invoked for lifecycle events.
type LifecycleHandler func(hook HookType, payload map[string]any)

// LifecycleRegistry tracks lifecycle handlers registered by plugins.
type LifecycleRegistry struct {
	handlers map[HookType][]LifecycleHandler
}

// NewLifecycleRegistry creates an empty registry.
func NewLifecycleRegistry() *LifecycleRegistry {
	return &LifecycleRegistry{handlers: make(map[HookType][]LifecycleHandler)}
}

// Register adds a handler for a hook type.
func (r *LifecycleRegistry) Register(hook HookType, h LifecycleHandler) {
	r.handlers[hook] = append(r.handlers[hook], h)
}

// Dispatch calls all handlers for the given hook.
func (r *LifecycleRegistry) Dispatch(hook string, payload map[string]any) {
	for _, h := range r.handlers[HookType(hook)] {
		h(HookType(hook), payload)
	}
}

// Count returns the number of registered handlers for a hook.
func (r *LifecycleRegistry) Count(hook HookType) int {
	return len(r.handlers[hook])
}
