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

// persistModelSwitch saves a provider/model switch across the writable
// config layers. The PROJECT layer is written FIRST (Bug6): it is the
// highest-precedence cascade layer, so each project keeps its own last-used
// provider/model pair; the home layer is updated afterwards as the global
// fallback for projects that have no pin yet.
//
// The project pin is gated on execution.auto_save_model (default true): an
// explicit opt-out keeps the legacy home-only behavior.
func persistModelSwitch(cfg *config.Config, saver config.ConfigSaver) error {
	if saver == nil {
		return nil
	}
	if cfg.Execution.AutoSaveModel {
		if err := saver.SaveProjectActiveModel(cfg); err != nil {
			return fmt.Errorf("failed to save active model to project: %w", err)
		}
	}
	if err := saver.SaveHomeProvidersAndModels(cfg); err != nil {
		return fmt.Errorf("failed to save provider/model config: %w", err)
	}
	return nil
}
