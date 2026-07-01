// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed configs/default.yaml configs/themes/dark.yaml configs/themes/light.yaml
var embeddedFS embed.FS

// DefaultConfigYAML returns the embedded default config YAML.
// Returns an error if the embedded asset is missing (a build/packaging bug).
func DefaultConfigYAML() (string, error) {
	data, err := embeddedFS.ReadFile("configs/default.yaml")
	if err != nil {
		return "", fmt.Errorf("missing embedded default config: %w", err)
	}
	return string(data), nil
}

// DefaultThemeYAML returns the embedded theme YAML for the given theme name.
func DefaultThemeYAML(name string) string {
	path := filepath.Join("configs/themes", name+".yaml")
	data, err := embeddedFS.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

// DefaultSkillDirs returns the default skill directories that are always searched.
// These are the cross-agent standard paths: ~/.agents/skills/ (user-global)
// and $PWD/.agents/skills/ (project-local). Later dirs override earlier ones.
func DefaultSkillDirs(projectDir string) []string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return []string{filepath.Join(projectDir, ".agents", "skills")}
	}
	return []string{
		filepath.Join(homeDir, ".agents", "skills"),
		filepath.Join(projectDir, ".agents", "skills"),
	}
}
