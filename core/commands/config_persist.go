// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"

	"github.com/pijalu/goa/config"
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

// persistModelSwitch saves a provider/model switch. The PROJECT layer is the
// PRIMARY store (Bug6 + bugs.md model-scope design): it is the
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
	if cfg.Execution.AutoSaveModel {
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
