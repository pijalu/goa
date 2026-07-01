// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"

	"github.com/pijalu/goa/config"
)

// saveProjectConfig persists the full config to the project .goa/config.yaml.
// Used for project-local settings such as autonomy level.
func saveProjectConfig(cfg *config.Config, saver config.ConfigSaver) error {
	if saver == nil {
		return nil
	}
	if err := saver.SaveProjectConfig(cfg); err != nil {
		return fmt.Errorf("failed to save project config: %w", err)
	}
	return nil
}

// saveHomeProvidersAndModels persists provider/model configuration to the home
// directory without overwriting other home-config settings. When
// cfg.Execution.AutoSaveModel is true, it also updates the project config so
// that model/provider changes survive a reload in the presence of project-level
// overrides (e.g. models defined in .goa/config.yaml).
func saveHomeProvidersAndModels(cfg *config.Config, saver config.ConfigSaver) error {
	if saver == nil {
		return nil
	}
	if err := saver.SaveHomeProvidersAndModels(cfg); err != nil {
		return fmt.Errorf("failed to save provider/model config: %w", err)
	}

	// When auto_save_model is true, also update the project config so that
	// model/provider changes (including deletions) survive restarts even
	// when the project config defines its own providers/models.
	if cfg.Execution.AutoSaveModel {
		if err := saver.SaveProjectProvidersAndModels(cfg); err != nil {
			return fmt.Errorf("failed to save provider/model config to project: %w", err)
		}
	}

	return nil
}
