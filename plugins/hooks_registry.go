// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"fmt"
	"sort"
	"sync"
)

// HookMode selects how a registered hook participates at its point.
type HookMode string

const (
	// HookNotify marks a read-only observer: it runs asynchronously against a
	// payload snapshot and can neither modify nor veto anything. Notify
	// handlers can never stall the agent loop.
	HookNotify HookMode = "notify"
	// HookIntercept marks a synchronous interceptor: it may rewrite the
	// payload flowing through the point or deny the action outright via
	// {deny:true, reason:"..."}.
	HookIntercept HookMode = "intercept"
)

// HookSpec describes one registered hook. Priority orders the chain
// ascending; ties break by registration order (the order handlers were
// registered, which for equal priorities means plugin load order then
// in-plugin call order).
type HookSpec struct {
	PluginID string   // manifest id of the registering plugin
	Name     string   // unique per plugin
	Point    string   // wire id ("message:pre-send", …) — see ValidHookPoints
	Mode     HookMode // notify | intercept
	Priority int      // ascending; lower runs first
}

// HookHandler executes one hook invocation. Implementations created by the
// bridge run UNDER the global VM lock (goja single-goroutine rule) and receive
// a private JSON copy of the payload — mutating it never affects the caller.
// Returning nil means "no change" (pass-through).
type HookHandler func(payload map[string]any) map[string]any

// HookEntry pairs a spec with its executable handler.
type HookEntry struct {
	Spec    HookSpec
	Handler HookHandler
}

// HookValidator is the enforcement seam consulted by Register. The manager
// injects it (nil validator = allow everything; M6 installs the
// manifest-declaration + install-grant validator).
type HookValidator func(spec HookSpec) error

// HookRegistry holds ordered hook chains per point. All methods are safe for
// concurrent use; chains are copied on read so a Snapshot never aliases live
// registration state.
type HookRegistry struct {
	mu     sync.RWMutex
	chains map[string][]HookEntry
	allow  HookValidator
}

// NewHookRegistry creates an empty registry. allow may be nil (= allow all);
// when set, every Register consults it after local validation and a returned
// error rejects the registration.
func NewHookRegistry(allow HookValidator) *HookRegistry {
	return &HookRegistry{
		chains: make(map[string][]HookEntry),
		allow:  allow,
	}
}

// SetAllow installs or replaces the validator after construction (M6 wires
// the grant-backed enforcer right before plugin scripts run on every
// load/reload). Safe for concurrent use with Register.
func (r *HookRegistry) SetAllow(v HookValidator) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.allow = v
}

// Register adds one hook entry. Validation order: shape (ids, mode, handler),
// duplicate name per plugin (across ALL points — a plugin's hook names form
// one namespace), then the injected allow validator. The entry is appended to
// its point's chain, which preserves registration order inside a priority
// tie.
func (r *HookRegistry) Register(spec HookSpec, handler HookHandler) error {
	if err := validateHookSpec(spec, handler); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, chain := range r.chains {
		for _, e := range chain {
			if e.Spec.PluginID == spec.PluginID && e.Spec.Name == spec.Name {
				return fmt.Errorf("plugin %s already registered a hook named %q", spec.PluginID, spec.Name)
			}
		}
	}
	if r.allow != nil {
		if err := r.allow(spec); err != nil {
			return fmt.Errorf("hook %s@%s rejected: %w", spec.Name, spec.Point, err)
		}
	}
	r.chains[spec.Point] = append(r.chains[spec.Point], HookEntry{Spec: spec, Handler: handler})
	return nil
}

// validateHookSpec enforces the shape contract shared by the registry and the
// JS bridge (which pre-validates points against the constant list; the
// registry only rejects structurally invalid specs so Go-side tests cannot
// register garbage either).
func validateHookSpec(spec HookSpec, handler HookHandler) error {
	switch {
	case spec.PluginID == "":
		return fmt.Errorf("hook PluginID is required")
	case spec.Name == "":
		return fmt.Errorf("hook Name is required")
	case spec.Point == "":
		return fmt.Errorf("hook Point is required")
	case spec.Mode != HookNotify && spec.Mode != HookIntercept:
		return fmt.Errorf("hook Mode must be %q or %q, got %q", HookNotify, HookIntercept, spec.Mode)
	case handler == nil:
		return fmt.Errorf("hook %s: Handler is required", spec.Name)
	}
	return nil
}

// UnregisterPlugin removes every hook registered by pluginID across all
// chains (plugin unload / disable). Unknown ids are a no-op.
func (r *HookRegistry) UnregisterPlugin(pluginID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for point, chain := range r.chains {
		kept := chain[:0]
		for _, e := range chain {
			if e.Spec.PluginID != pluginID {
				kept = append(kept, e)
			}
		}
		if len(kept) == 0 {
			delete(r.chains, point)
			continue
		}
		r.chains[point] = kept
	}
}

// Snapshot returns a copy of the point's chain sorted by Priority ascending,
// stable by registration order. A missing/empty chain returns nil so hot
// paths (reply deltas with no hooks) pay no allocation beyond the lookup.
func (r *HookRegistry) Snapshot(point string) []HookEntry {
	r.mu.RLock()
	chain := r.chains[point]
	if len(chain) == 0 {
		r.mu.RUnlock()
		return nil
	}
	out := make([]HookEntry, len(chain))
	copy(out, chain)
	r.mu.RUnlock()
	if len(out) > 1 {
		sort.SliceStable(out, func(i, j int) bool { return out[i].Spec.Priority < out[j].Spec.Priority })
	}
	return out
}

// HasInterceptors reports whether any intercept-mode hook is registered at
// the point. Cheaper than Snapshot for callers that only gate on existence.
func (r *HookRegistry) HasInterceptors(point string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, e := range r.chains[point] {
		if e.Spec.Mode == HookIntercept {
			return true
		}
	}
	return false
}

// Count returns the total number of registered hooks (diagnostics/tests).
func (r *HookRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n := 0
	for _, chain := range r.chains {
		n += len(chain)
	}
	return n
}
