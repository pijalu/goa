// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package hooks

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// LoadConfig loads hook configurations from user and project directories.
// Project-level hooks are appended after user-level hooks, so project hooks
// can override or extend user hooks by event. The returned configuration is
// the merged result.
func LoadConfig(homeDir, projectDir string) (*Config, error) {
	var merged Config

	userPath := filepath.Join(homeDir, ".goa", "hooks.yaml")
	if _, err := os.Stat(userPath); err == nil {
		cfg, err := loadFile(userPath)
		if err != nil {
			return nil, fmt.Errorf("load user hooks: %w", err)
		}
		merged.Hooks = append(merged.Hooks, cfg.Hooks...)
	}

	projectPath := filepath.Join(projectDir, ".goa", "hooks.yaml")
	if _, err := os.Stat(projectPath); err == nil {
		cfg, err := loadFile(projectPath)
		if err != nil {
			return nil, fmt.Errorf("load project hooks: %w", err)
		}
		merged.Hooks = append(merged.Hooks, cfg.Hooks...)
	}

	if err := merged.Validate(); err != nil {
		return nil, err
	}
	return &merged, nil
}

func loadFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
