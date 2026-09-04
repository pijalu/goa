// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/pijalu/goa/internal"
	"gopkg.in/yaml.v3"
)

// ErrNoProjectDir reports that no project directory is configured, so the
// project config layer cannot be changed. Callers use it (via errors.Is) to
// decide on a home-layer fallback instead of treating the save as failed —
// a missing project is an expected state, not an error to surface
// (bugs.md: model changes must fall back to ~/.goa only when the project
// .goa cannot be changed).
var ErrNoProjectDir = errors.New("no project directory configured")

// Loader is the interface for configuration loading.
type Loader interface {
	// Load returns the merged configuration.
	Load() (*Config, error)
}

// ConfigProvider provides access to the current config.
type ConfigProvider interface {
	Config() *Config
}

// ConfigSaver persists configuration changes back to disk.
type ConfigSaver interface {
	// Save writes the given config to ~/.goa/config.yaml.
	Save(cfg *Config) error

	// SaveProjectConfig persists the mode configuration (active major mode and
	// per-mode autonomy) to .goa/config.yaml in the project directory. The save
	// is field-scoped: existing on-disk settings are preserved, and a newly
	// created file contains only the mode section — never a dump of the merged
	// in-memory config (that would bake embedded defaults and home-layer values
	// into the project layer, where they shadow later home-config edits).
	SaveProjectConfig(cfg *Config) error

	// SaveHomeProvidersAndModels updates providers, models, active_provider, and
	// active_model in ~/.goa/config.yaml without overwriting other home settings.
	SaveHomeProvidersAndModels(cfg *Config) error

	// SaveProjectProvidersAndModels updates providers, models, active_provider, and
	// active_model in .goa/config.yaml without overwriting other project settings.
	SaveProjectProvidersAndModels(cfg *Config) error

	// SaveProjectActiveModel persists ONLY active_provider and active_model into
	// .goa/config.yaml in the project directory, creating the file when missing.
	// This is the per-project "last used model" pin (Bug6): project is the
	// highest-precedence cascade layer, so each project keeps its own last-used
	// pair while home retains the global fallback. Providers/models catalogs are
	// deliberately untouched — the pin references them, it does not duplicate
	// them.
	//
	// Returns ErrNoProjectDir when no project directory is configured (the
	// project layer cannot exist), and a wrapped error when the project file
	// cannot be written. Implementations must never create a relative ".goa"
	// in an arbitrary CWD as a side effect. Callers that need the switch to
	// survive restart regardless of layer fall back to the home layer on any
	// error from this method (see commands.persistModelSwitch).
	SaveProjectActiveModel(cfg *Config) error

	// SaveHomeField updates a single scalar field in ~/.goa/config.yaml without
	// overwriting other settings. The path is a sequence of nested YAML keys.
	SaveHomeField(path []string, value any) error

	// SaveProjectField updates a single scalar field in .goa/config.yaml in the
	// project directory without overwriting other settings. The path is a sequence
	// of nested YAML keys. Missing intermediate maps are created automatically.
	SaveProjectField(path []string, value any) error

	// SaveProjectFieldValue writes an arbitrary value (including maps and slices)
	// at the given path in .goa/config.yaml, preserving other settings.
	SaveProjectFieldValue(path []string, value any) error

	// SaveHomeFieldValue writes an arbitrary value (including maps and slices)
	// at the given path in ~/.goa/config.yaml, preserving other settings.
	SaveHomeFieldValue(path []string, value any) error

	// SaveLocalFieldValue writes an arbitrary value (including maps and slices)
	// at the given path in .goa/config.local.yaml — the project LOCAL layer
	// (gitignored, per-developer). Use for project-scoped, per-developer
	// settings such as teams.active: they must neither leak across projects
	// (home layer) nor dirty the committed project config.
	SaveLocalFieldValue(path []string, value any) error

	// DeleteProjectField removes the key at the given path from .goa/config.yaml.
	DeleteProjectField(path []string) error

	// DeleteHomeField removes the key at the given path from ~/.goa/config.yaml.
	DeleteHomeField(path []string) error

	// Reload re-reads config from all cascade layers and returns the result.
	Reload() (*Config, error)
}

// CascadeLoader implements a multi-source configuration cascade:
// embedded defaults → ~/.goa/config.yaml → .goa/config.yaml → .goa/config.local.yaml
// → env vars (GOA_*) → CLI flags
type CascadeLoader struct {
	homeDir      string
	projectDir   string
	configPath   string // explicit --config path (overrides cascade for file)
	cliOverrides map[string]string
	// writeMu serializes every config-file read-modify-write cycle (Save*,
	// editConfigFile). All writers mutate the same home/project config.yaml
	// on disk; without exclusion, a concurrent field-scoped write (skill
	// toggle) and a snapshot write (model switch, /goal:settings) interleave
	// and silently lose entries (skills re-enable spontaneously).
	writeMu sync.Mutex
}

// NewCascadeLoader creates a new cascade loader.
// cliFlags is a map of flag names to values from cobra.
func NewCascadeLoader(projectDir, explicitConfigPath string, cliFlags map[string]string) *CascadeLoader {
	homeDir, _ := internal.GoaHome()
	return &CascadeLoader{
		homeDir:      homeDir,
		projectDir:   projectDir,
		configPath:   explicitConfigPath,
		cliOverrides: cliFlags,
	}
}

// Load implements the full configuration cascade.
func (cl *CascadeLoader) Load() (*Config, error) {
	cfg, err := cl.loadDefaults()
	if err != nil {
		return nil, err
	}

	homeCfg, err := cl.loadHomeConfig(cfg)
	if err != nil {
		return nil, err
	}
	cfg = homeCfg

	cfg, err = cl.loadProjectConfig(cfg)
	if err != nil {
		return nil, err
	}

	// Fetch remote provider registries before env interpolation so that
	// registry-provided values can still use env references.
	if err := cl.loadRegistries(cfg); err != nil {
		return nil, fmt.Errorf("registry: %w", err)
	}

	if err := cl.interpolateEnv(cfg); err != nil {
		return nil, fmt.Errorf("env interpolation: %w", err)
	}
	cl.applyEnvOverrides(cfg)
	cl.applyCLIOverrides(cfg)

	// Migrate deprecated active_profile to mode.default.major. This runs after
	// CLI/env overrides so that legacy configs are upgraded without losing the
	// user's explicit --profile flag.
	if cfg.ActiveProfile != "" {
		if cfg.Mode.Default.Major == "" || cfg.Mode.Default.Major == internal.MajorCoder {
			cfg.Mode.Default.Major = internal.MajorMode(cfg.ActiveProfile)
		}
		cfg.ActiveProfile = ""
	}

	// Migrate old execution.mode to mode.defaults (M13)
	migrateLegacyMode(cfg)

	// Convert deprecated ProviderConfig.DefaultModel into explicit ModelConfig entries.
	cfg.migrateProviderDefaultModels()

	// Drop selector sentinel values ("__add__" etc.) persisted by older versions.
	cfg.sanitizeSelectorSentinels()

	// Drop a dangling teams.active (team definition removed after the
	// selection was persisted — e.g. deleted in another layer or by hand).
	// Starting with no active team is identical to /team:off, so heal
	// instead of failing validation and refusing to start.
	cfg.sanitizeDanglingActiveTeam()

	// Drop stale context_compression.per_model overrides (model deleted after
	// the override was persisted). A dangling override is inert at runtime,
	// so heal instead of failing validation and refusing to start.
	cfg.sanitizeDanglingCompressionModels()

	// Clear dangling model references in team member definitions and
	// orchestrator roles/pool (model deleted after the reference was
	// persisted). An empty model falls back to the active model at activation
	// time, so heal instead of failing validation and refusing to start.
	cfg.sanitizeDanglingModelRefs()

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (cl *CascadeLoader) loadDefaults() (*Config, error) {
	cfg := &Config{}
	defaults, err := DefaultConfigYAML()
	if err != nil {
		return nil, &internal.ConfigError{Key: "embedded", Err: err}
	}
	if err := yaml.Unmarshal([]byte(defaults), cfg); err != nil {
		return nil, &internal.ConfigError{Key: "embedded", Err: fmt.Errorf("unmarshal embedded defaults: %w", err)}
	}
	homeConfigPath := filepath.Join(cl.homeDir, ".goa", "config.yaml")
	_, err = os.Stat(homeConfigPath)
	cfg.FirstRun = os.IsNotExist(err)
	cfg.ConfigDir = filepath.Join(cl.homeDir, ".goa")
	return cfg, nil
}

func (cl *CascadeLoader) loadHomeConfig(cfg *Config) (*Config, error) {
	homeConfigPath := filepath.Join(cl.homeDir, ".goa", "config.yaml")
	if _, err := os.Stat(homeConfigPath); os.IsNotExist(err) {
		return cfg, nil // no home config
	}
	if err := cl.mergeFile(cfg, homeConfigPath); err != nil {
		return nil, fmt.Errorf("loading home config: %w", err)
	}
	return cfg, nil
}

func (cl *CascadeLoader) loadProjectConfig(cfg *Config) (*Config, error) {
	if cl.configPath != "" {
		if err := cl.mergeFile(cfg, cl.configPath); err != nil {
			return nil, fmt.Errorf("loading --config: %w", err)
		}
		return cfg, nil
	}

	projectPath := filepath.Join(cl.projectDir, ".goa", "config.yaml")
	if err := cl.mergeProjectFile(cfg, projectPath); err != nil {
		return nil, err
	}

	localPath := filepath.Join(cl.projectDir, ".goa", "config.local.yaml")
	if err := cl.mergeProjectFile(cfg, localPath); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (cl *CascadeLoader) mergeProjectFile(cfg *Config, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // file is optional
		}
		return fmt.Errorf("read %s: %w", path, err)
	}
	layer := &Config{}
	if err := yaml.Unmarshal(data, layer); err != nil {
		return &internal.ConfigError{Key: path, Err: fmt.Errorf("unmarshal: %w", err)}
	}
	cfg.DeepMerge(layer)
	return nil
}

// Config returns the current config (used by ConfigProvider interface).
// For CascadeLoader, this calls Load() each time to get fresh state.
// In production, this is replaced by a cached reference.
func (cl *CascadeLoader) Config() *Config {
	cfg, err := cl.Load()
	if err != nil {
		// Return an empty config on error rather than nil
		return &Config{}
	}
	return cfg
}

// mergeFile reads a YAML file and deep-merges it into the config.
func (cl *CascadeLoader) mergeFile(cfg *Config, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	layer := &Config{}
	if err := yaml.Unmarshal(data, layer); err != nil {
		return &internal.ConfigError{Key: path, Err: fmt.Errorf("unmarshal: %w", err)}
	}
	cfg.DeepMerge(layer)
	return nil
}

// Save writes the given config to ~/.goa/config.yaml.
// Reload re-reads config from all cascade layers and returns the result.
func (cl *CascadeLoader) Reload() (*Config, error) {
	return cl.Load()
}

func (cl *CascadeLoader) Save(cfg *Config) error {
	cl.writeMu.Lock()
	defer cl.writeMu.Unlock()
	configDir := filepath.Join(cl.homeDir, ".goa")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	// Create a copy without ephemeral fields
	saveCfg := cfg.DeepCopy()
	saveCfg.FirstRun = false
	saveCfg.ConfigDir = ""
	saveCfg.Models = persistableModels(saveCfg.Models)
	// Preserve on-disk skill lists: a stale in-memory snapshot must not
	// resurrect a skill the user disabled (skills re-enable bug) nor copy a
	// project layer's sticky state into the home file.
	if en, dis, st, stOff, ok := skillListsOnDisk(cl.HomeConfigPath()); ok {
		saveCfg.Skills.Enabled = en
		saveCfg.Skills.Disabled = dis
		saveCfg.Skills.Sticky = st
		saveCfg.Skills.StickyOff = stOff
	}

	data, err := yaml.Marshal(saveCfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(cl.HomeConfigPath(), data, 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	return nil
}

// SaveProjectActiveModel persists ONLY active_provider and active_model into
// the project's .goa/config.yaml (created when missing). See the interface
// doc: this is the per-project last-used-model pin. Returns ErrNoProjectDir
// when no project directory is configured — a relative ".goa" in an arbitrary
// CWD must never be created as a side effect, and callers use the sentinel to
// fall back to the home layer (bugs.md: home only when project unchangeable).
//
// An empty active value CLEARS the pin instead of being skipped: a pin whose
// model was removed from the configuration must not be resurrected by the
// next load (project is the highest-precedence layer, so a stale pin always
// wins over home). Deleting the key drops the project back to the home pin
// or the usage-based boot default.
func (cl *CascadeLoader) SaveProjectActiveModel(cfg *Config) error {
	if cl.projectDir == "" {
		return ErrNoProjectDir
	}
	if err := cl.pinProjectField("active_provider", cfg.ActiveProvider); err != nil {
		return err
	}
	return cl.pinProjectField("active_model", cfg.ActiveModel)
}

// pinProjectField writes value into the project config under key, or removes
// the key when value is empty (deleteYamlNode no-ops when the key is absent,
// so clearing is idempotent).
func (cl *CascadeLoader) pinProjectField(key, value string) error {
	if value != "" {
		return cl.SaveProjectField([]string{key}, value)
	}
	return cl.DeleteProjectField([]string{key})
}

// SaveHomeProvidersAndModels updates providers, models, active_provider, and
// active_model in ~/.goa/config.yaml without overwriting other home settings.
// It reads the existing home file (if any), applies the provider/model fields
// from cfg, and writes the merged result back.
func (cl *CascadeLoader) SaveHomeProvidersAndModels(cfg *Config) error {
	cl.writeMu.Lock()
	defer cl.writeMu.Unlock()
	configDir := filepath.Join(cl.homeDir, ".goa")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	pathYaml := filepath.Join(configDir, "config.yaml")

	homeCfg := &Config{}
	data, err := os.ReadFile(pathYaml)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("read home config: %w", err)
		}
	} else {
		if err := yaml.Unmarshal(data, homeCfg); err != nil {
			return fmt.Errorf("unmarshal home config: %w", err)
		}
	}

	homeCfg.ActiveProvider = cfg.ActiveProvider
	homeCfg.ActiveModel = cfg.ActiveModel
	homeCfg.Providers = cfg.Providers
	homeCfg.Models = persistableModels(cfg.Models)

	saveCfg := homeCfg.DeepCopy()
	saveCfg.FirstRun = false
	saveCfg.ConfigDir = ""

	out, err := yaml.Marshal(saveCfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(pathYaml, out, 0644); err != nil {
		return fmt.Errorf("write home config: %w", err)
	}
	return nil
}

// SaveProjectProvidersAndModels updates providers, models, active_provider, and
// active_model in .goa/config.yaml without overwriting other project settings.
// It reads the existing project file (if any), applies the provider/model fields
// from cfg, and writes the merged result back.
func (cl *CascadeLoader) SaveProjectProvidersAndModels(cfg *Config) error {
	cl.writeMu.Lock()
	defer cl.writeMu.Unlock()
	configDir := filepath.Join(cl.projectDir, ".goa")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("create project config dir: %w", err)
	}
	pathYaml := filepath.Join(configDir, "config.yaml")

	projectCfg := &Config{}
	data, err := os.ReadFile(pathYaml)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("read project config: %w", err)
		}
		// If the project config doesn't exist, nothing to update
		return nil
	}
	if err := yaml.Unmarshal(data, projectCfg); err != nil {
		return fmt.Errorf("unmarshal project config: %w", err)
	}

	projectCfg.ActiveProvider = cfg.ActiveProvider
	projectCfg.ActiveModel = cfg.ActiveModel
	projectCfg.Providers = cfg.Providers
	projectCfg.Models = persistableModels(cfg.Models)

	saveCfg := projectCfg.DeepCopy()
	saveCfg.FirstRun = false
	saveCfg.ConfigDir = ""

	out, err := yaml.Marshal(saveCfg)
	if err != nil {
		return fmt.Errorf("marshal project config: %w", err)
	}
	if err := os.WriteFile(pathYaml, out, 0644); err != nil {
		return fmt.Errorf("write project config: %w", err)
	}
	return nil
}
