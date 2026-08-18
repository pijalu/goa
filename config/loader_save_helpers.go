// SPDX-License-Identifier: GPL-3.0-or-later
package config

import (
	"fmt"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
)

func (cl *CascadeLoader) SaveProjectConfig(cfg *Config) error {
	cl.writeMu.Lock()
	defer cl.writeMu.Unlock()
	dir := filepath.Join(cl.projectDir, ".goa")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create project config dir: %w", err)
	}
	path := filepath.Join(dir, "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read project config: %w", err)
	}
	if os.IsNotExist(err) {
		return writeModeOnlyProjectConfig(path, cfg.Mode)
	}
	onDisk := &Config{}
	if yaml.Unmarshal(data, onDisk) != nil {
		return writeModeOnlyProjectConfig(path, cfg.Mode)
	}
	onDisk.Mode = cfg.Mode
	save := onDisk.DeepCopy()
	save.FirstRun = false
	save.ConfigDir = ""
	save.Models = persistableModels(save.Models)
	out, err := yaml.Marshal(save)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err = os.WriteFile(path, out, 0644); err != nil {
		return fmt.Errorf("write project config: %w", err)
	}
	return nil
}
func writeModeOnlyProjectConfig(path string, mode ModeConfig) error {
	out, err := yaml.Marshal(map[string]ModeConfig{"mode": mode})
	if err != nil {
		return fmt.Errorf("marshal mode config: %w", err)
	}
	if err = os.WriteFile(path, out, 0644); err != nil {
		return fmt.Errorf("write mode config: %w", err)
	}
	return nil
}
func persistableModels(models []ModelConfig) []ModelConfig {
	out := make([]ModelConfig, 0, len(models))
	for _, m := range models {
		if !m.Ephemeral {
			out = append(out, m)
		}
	}
	return out
}
func (cl *CascadeLoader) HomeConfigPath() string {
	return filepath.Join(cl.homeDir, ".goa", "config.yaml")
}
func (cl *CascadeLoader) WritableConfigPaths() []string {
	paths := []string{cl.HomeConfigPath()}
	if cl.configPath != "" {
		return append(paths, cl.configPath)
	}
	return append(paths, filepath.Join(cl.projectDir, ".goa", "config.yaml"), filepath.Join(cl.projectDir, ".goa", "config.local.yaml"))
}
func skillListsOnDisk(path string) (enabled, disabled, sticky, stickyOff []string, ok bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, nil, nil, false
	}
	var raw struct {
		Skills struct {
			Enabled   []string `yaml:"enabled"`
			Disabled  []string `yaml:"disabled"`
			Sticky    []string `yaml:"sticky"`
			StickyOff []string `yaml:"sticky_off"`
		} `yaml:"skills"`
	}
	if yaml.Unmarshal(data, &raw) != nil {
		return nil, nil, nil, nil, false
	}
	return raw.Skills.Enabled, raw.Skills.Disabled, raw.Skills.Sticky, raw.Skills.StickyOff, true
}
