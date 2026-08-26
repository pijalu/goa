// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
)

// saveProjectConfig persists the mode configuration to the project
// .goa/config.yaml (field-scoped: only the mode section is written;
// see CascadeLoader.SaveProjectConfig). Used for project-local settings
// such as autonomy level.
func saveProjectConfig(cfg *config.Config, saver config.ConfigSaver) error {
	if saver == nil {
		return nil
	}
	if err := saver.SaveProjectConfig(cfg); err != nil {
		return fmt.Errorf("failed to save project config: %w", err)
	}
	return nil
}

// persistModelSwitch saves a provider/model SELECTION change. The PROJECT
// layer is the PRIMARY store (Bug6 + bugs.md model-scope design): it is the
// highest-precedence cascade layer, so each project keeps its own last-used
// provider/model pair, and a switch in one project no longer leaks into the
// global default.
//
// ~/.goa is written ONLY as a fallback:
//   - execution.auto_save_model true (default) → project pin first; when the
//     project layer cannot be changed (no project directory configured, or
//     the write fails e.g. read-only tree) the home layer keeps the choice
//     alive so it still survives restart;
//   - execution.auto_save_model false (explicit opt-out) → legacy home-only
//     persistence, the user declined project writes.
func persistModelSwitch(cfg *config.Config, saver config.ConfigSaver) error {
	if saver == nil {
		return nil
	}
	if cfg.Execution.AutoSaveModelEnabled() {
		if err := saver.SaveProjectActiveModel(cfg); err == nil {
			// Per-project pin written: home stays untouched (bugs.md — a
			// model change in one project must not become every other
			// project's default).
			return nil
		}
		// Project layer unchangeable → fall back to home below.
	}
	return saver.SaveHomeProvidersAndModels(cfg)
}

// persistModelCatalogChange saves a change to the providers/models CATALOG
// (added/removed model or provider) — a different responsibility than a
// selection switch: the catalog is GLOBAL state, so ~/.goa (its canonical
// store) is updated FIRST and unconditionally. Afterwards, when project
// pinning is enabled, the pin is mirrored so a cleared/deleted ACTIVE entry
// cannot be resurrected from a stale highest-precedence pin
// (TestRemoveActiveModel_ClearsProjectPin). Mirroring respects the RC-5
// rule through the same suppression gate as switches: while a team governs
// the session model, cfg.ActiveProvider/ActiveModel hold the TEAM's couple,
// which must never become the user's saved pin. Catalog call-sites used to
// work only because the merge bug (bugs.md) forced every install onto the
// legacy home path; splitting the two operations makes the default-on world
// lossless.
func persistModelCatalogChange(host core.UIHost, cfg *config.Config, saver config.ConfigSaver) error {
	if saver == nil {
		return nil
	}
	homeErr := saver.SaveHomeProvidersAndModels(cfg)
	// teamModelPersistenceSuppressed reports false for non-Context hosts (no
	// team manager is reachable), so unknown hosts keep legacy behavior.
	if cfg.Execution.AutoSaveModelEnabled() {
		if _, suppressed := teamModelPersistenceSuppressed(host); !suppressed {
			// Best-effort pin refresh: the catalog write above already gives
			// durability, a pin failure must not mask it.
			_ = saver.SaveProjectActiveModel(cfg)
		}
	}
	if homeErr != nil {
		return fmt.Errorf("failed to save config: %w", homeErr)
	}
	return nil
}
