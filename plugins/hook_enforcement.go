// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// HookEscapeEnv is the headless escape hatch (§7 step 5): when no TUI exists
// the grant flow cannot run, so external plugin hooks are disabled entirely —
// unless this environment variable is set to "1", which is documented and
// logged loudly.
const HookEscapeEnv = "GOA_PLUGIN_HOOKS_APPROVED"

// HookEnforcer is the M6 grant-backed implementation of the registry's
// HookValidator slot (§7 step 4). It consults, in order:
//
//  1. the manifest declaration gate — registrations not matching a declared
//     (mode, point) are rejected outright regardless of grants;
//  2. the bundled pre-approval — bundled plugins (provider-quota) ship
//     approved and skip the grant flow;
//  3. the headless fail-closed gate — without a TUI the approval card cannot
//     run, so external hooks are refused unless HookEscapeEnv=1;
//  4. the stored grant — approved (mode, point) pairs only, with staleness
//     (version bump / manifest fingerprint change ⇒ re-prompt).
//
// Declarations are recorded at discovery via Declare/ObserveDir, which is
// what makes enforcement apply at EVERY load/reload: a fresh enforcer is
// built per plugin scan and handed to the registry before any plugin script
// runs. All methods are safe for concurrent use.
type HookEnforcer struct {
	store   *GrantStore
	headless bool
	escape  bool // HookEscapeEnv=1 escape hatch

	mu      sync.Mutex
	decls   map[string]*PluginDef // discovered manifests by plugin id
	bundled map[string]bool       // pre-approved bundled plugin ids
}

// NewHookEnforcer builds an enforcer backed by store. headless reflects the
// runtime mode (no TUI); the escape hatch is read from HookEscapeEnv at
// construction so tests can inject it via t.Setenv.
func NewHookEnforcer(store *GrantStore, headless bool) *HookEnforcer {
	return &HookEnforcer{
		store:    store,
		headless: headless,
		escape:   os.Getenv(HookEscapeEnv) == "1",
		decls:    make(map[string]*PluginDef),
		bundled:  make(map[string]bool),
	}
}

// Declare records one discovered plugin's manifest (and whether it ships
// bundled). Called for every scanned plugin.yaml at discovery, regardless of
// enabled state — cheap, and it keeps enforcement independent of load order.
func (e *HookEnforcer) Declare(def *PluginDef, bundled bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.decls[def.ID] = def
	if bundled {
		e.bundled[def.ID] = true
	} else {
		delete(e.bundled, def.ID)
	}
}

// ObserveDir declares every immediate subdirectory of dir that carries a
// plugin.yaml, mirroring PluginLoader's scan shape. Invalid manifests are
// skipped here (they refuse loading independently); unreadable dirs are a
// no-op so a missing bundled dir never blocks startup.
func (e *HookEnforcer) ObserveDir(dir string, bundled bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		def, err := loadManifest(filepath.Join(dir, entry.Name(), "plugin.yaml"))
		if err != nil || def == nil {
			continue
		}
		e.Declare(def, bundled)
	}
}

// Allow implements HookValidator. See the type doc for the gate order.
func (e *HookEnforcer) Allow(spec HookSpec) error {
	e.mu.Lock()
	def := e.decls[spec.PluginID]
	bundled := e.bundled[spec.PluginID]
	e.mu.Unlock()

	switch {
	case def == nil:
		return fmt.Errorf("plugin %q has no discovered manifest", spec.PluginID)
	default:
	}
	// Gate 1: undeclared-in-manifest registrations are rejected outright,
	// regardless of grants or bundling (§7 step 4).
	if err := checkHookDeclared(spec, def); err != nil {
		return err
	}
	// Gate 2: bundled plugins ship pre-approved — skip prompts/grants.
	if bundled {
		return nil
	}
	// Gate 3: headless fail-closed (§7 step 5). The grant flow cannot run
	// without a TUI; only the documented escape hatch overrides it.
	if e.headless && !e.escape {
		return fmt.Errorf("external plugin hooks are disabled in headless mode (set %s=1 to override)", HookEscapeEnv)
	}
	// Gate 4: grant check with staleness handling.
	g, ok, err := e.store.Get(spec.PluginID)
	if err != nil {
		return fmt.Errorf("grant store unreadable — refusing hook (%v)", err)
	}
	if GrantStale(g, ok, def) {
		return fmt.Errorf("no current grant covers %s@%s — install-time (re-)approval required", spec.Mode, spec.Point)
	}
	for _, ah := range g.ApprovedHooks {
		if ah.Point == spec.Point && ah.Mode == string(spec.Mode) {
			return nil
		}
	}
	return fmt.Errorf("hook %s@%s was not selected in the install review", spec.Mode, spec.Point)
}

// checkHookDeclared verifies spec matches one declared (mode, point) pair in
// the plugin's manifest.
func checkHookDeclared(spec HookSpec, def *PluginDef) error {
	for _, h := range def.Hooks {
		if h.Point == spec.Point && h.Mode == string(spec.Mode) {
			return nil
		}
	}
	return fmt.Errorf("registration of %s@%s is not declared in the manifest — refused", spec.Mode, spec.Point)
}

// NeedsApproval reports whether pluginID currently requires the review card:
// its discovered manifest declares hooks/permissions AND its stored grant is
// missing or stale. Bundled plugins never need approval (§7 step 4).
func (e *HookEnforcer) NeedsApproval(pluginID string) bool {
	e.mu.Lock()
	def := e.decls[pluginID]
	bundled := e.bundled[pluginID]
	e.mu.Unlock()
	if def == nil || bundled || !RequiresReview(def) {
		return false
	}
	g, ok, _ := e.store.Get(pluginID)
	return GrantStale(g, ok, def)
}

// PendingApprovals returns sorted plugin ids needing (re-)approval among the
// discovered set — the startup/reload re-prompt queue.
func (e *HookEnforcer) PendingApprovals() []string {
	e.mu.Lock()
	ids := make([]string, 0, len(e.decls))
	for id := range e.decls {
		ids = append(ids, id)
	}
	e.mu.Unlock()
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if e.NeedsApproval(id) {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// DeclaredFor returns a defensive copy of the manifest declarations recorded
// for pluginID (nil when unknown) — callers may freely mutate the result
// without corrupting enforcement state. Used by the review-card builder.
func (e *HookEnforcer) DeclaredFor(pluginID string) *PluginDef {
	e.mu.Lock()
	defer e.mu.Unlock()
	def := e.decls[pluginID]
	if def == nil {
		return nil
	}
	cp := *def // shallow struct copy; deep-copy the mutable slices too
	cp.Hooks = append([]PluginHookDecl(nil), def.Hooks...)
	cp.Permissions = append([]string(nil), def.Permissions...)
	return &cp
}
