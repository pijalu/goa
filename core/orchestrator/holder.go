// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"sync"

	"github.com/pijalu/goa/config"
)

// Builder constructs a fully-wired Runtime for one orchestration run. The
// production implementation lives in internal/app (OrchestratorAdapter) and
// bridges to a real multiagent.AgentPool; defining the interface here lets
// core/commands depend on it without importing internal/app (no cycle).
type Builder interface {
	NewRuntime(cfg config.OrchestratorConfig, rootDir string) (*Runtime, error)
}

// ActiveRuntime is a concurrency-safe holder for the currently-running
// orchestration. It is shared (by pointer) between the slash command that
// launches a run, the TUI that subscribes to its events, and steering
// commands that inject messages. At most one run is "active" at a time;
// launching a new run replaces (and stops listening to) the previous.
type ActiveRuntime struct {
	mu sync.Mutex
	rt *Runtime
}

// NewActiveRuntime returns an empty holder.
func NewActiveRuntime() *ActiveRuntime { return &ActiveRuntime{} }

// Set installs rt as the active runtime and returns the previously-active
// runtime (nil if none). The caller decides whether to stop the previous run.
func (a *ActiveRuntime) Set(rt *Runtime) *Runtime {
	a.mu.Lock()
	defer a.mu.Unlock()
	prev := a.rt
	a.rt = rt
	return prev
}

// Get returns the active runtime, or nil if no run is active.
func (a *ActiveRuntime) Get() *Runtime {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.rt
}

// Clear drops the active runtime if (and only if) it is still the one passed.
// This avoids a late clearer wiping a freshly-started newer run.
func (a *ActiveRuntime) Clear(rt *Runtime) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.rt == rt {
		a.rt = nil
	}
}
