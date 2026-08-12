// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/pijalu/goa/internal"
	"gopkg.in/yaml.v3"
)

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

// envPattern matches ${VAR} and ${VAR:-default}.
var envPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

// resolveEnvVar resolves a single ${VAR} or ${VAR:-default} expression.
func resolveEnvVar(match string) string {
	inner := match[2 : len(match)-1] // strip ${ and }
	parts := strings.SplitN(inner, ":-", 2)
	varName := parts[0]
	defaultVal := ""
	if len(parts) == 2 {
		defaultVal = parts[1]
	}
	if envVal := os.Getenv(varName); envVal != "" {
		return envVal
	}
	return defaultVal
}

// interpolateEnv replaces ${VAR} and ${VAR:-default} in all string fields.
func (cl *CascadeLoader) interpolateEnv(cfg *Config) error {
	return cl.interpolateVal(reflect.ValueOf(cfg).Elem())
}

func (cl *CascadeLoader) interpolateVal(val reflect.Value) error {
	if val.Kind() == reflect.Ptr {
		if val.IsNil() {
			return nil
		}
		val = val.Elem()
	}

	switch val.Kind() {
	case reflect.String:
		return cl.interpolateString(val)
	case reflect.Struct:
		return cl.interpolateStructFields(val)
	case reflect.Map:
		return cl.interpolateMapValues(val)
	case reflect.Slice:
		return cl.interpolateSliceElements(val)
	}
	return nil
}

func (cl *CascadeLoader) interpolateString(val reflect.Value) error {
	s := val.String()
	if strings.Contains(s, "${") {
		resolved := envPattern.ReplaceAllStringFunc(s, resolveEnvVar)
		val.SetString(resolved)
	}
	return nil
}

func (cl *CascadeLoader) interpolateStructFields(val reflect.Value) error {
	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		if field.CanSet() {
			if err := cl.interpolateVal(field); err != nil {
				return err
			}
		}
	}
	return nil
}

func (cl *CascadeLoader) interpolateMapValues(val reflect.Value) error {
	for _, key := range val.MapKeys() {
		mv := val.MapIndex(key)
		newVal := reflect.New(mv.Type()).Elem()
		newVal.Set(mv)
		if err := cl.interpolateVal(newVal); err != nil {
			return err
		}
		val.SetMapIndex(key, newVal)
	}
	return nil
}

func (cl *CascadeLoader) interpolateSliceElements(val reflect.Value) error {
	for i := 0; i < val.Len(); i++ {
		elem := val.Index(i)
		if elem.CanSet() {
			if err := cl.interpolateVal(elem); err != nil {
				return err
			}
		}
	}
	return nil
}

// applyEnvOverrides applies GOA_* environment variable overrides.
func (cl *CascadeLoader) applyEnvOverrides(cfg *Config) {
	cl.walkStructForEnv(reflect.ValueOf(cfg).Elem(), "")
}

func (cl *CascadeLoader) walkStructForEnv(val reflect.Value, prefix string) {
	typ := val.Type()
	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		fieldType := typ.Field(i)

		yamlTag := fieldType.Tag.Get("yaml")
		if yamlTag == "" || yamlTag == "-" {
			continue
		}
		name := strings.Split(yamlTag, ",")[0]

		envKey := "GOA_" + prefix + strings.ToUpper(name)
		if prefix == "" {
			envKey = "GOA_" + strings.ToUpper(name)
		}

		envVal := os.Getenv(envKey)
		if envVal == "" {
			// Recurse into struct fields even without env override
			if field.Kind() == reflect.Struct {
				cl.walkStructForEnv(field, prefix+strings.ToUpper(name)+"_")
			}
			continue
		}

		cl.setFieldFromEnv(field, envVal)
	}
}

func (cl *CascadeLoader) setFieldFromEnv(field reflect.Value, envVal string) {
	switch field.Kind() {
	case reflect.String:
		field.SetString(envVal)
	case reflect.Bool:
		field.SetBool(envVal == "true" || envVal == "1" || envVal == "yes")
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		intVal := 0
		fmt.Sscanf(envVal, "%d", &intVal)
		field.SetInt(int64(intVal))
	case reflect.Float64:
		floatVal := 0.0
		fmt.Sscanf(envVal, "%f", &floatVal)
		field.SetFloat(floatVal)
	}
}

// applyCLIOverrides applies the values from CLI flags.
func (cl *CascadeLoader) applyCLIOverrides(cfg *Config) {
	if cl.cliOverrides == nil {
		return
	}
	cl.applyScalarCLIOverrides(cfg)
	cfg.repairActiveProviderModel()
	cl.applyProviderCLIOverrides(cfg)
	cl.applyModelCLIOverrides(cfg)
	cl.applyExecutionCLIOverrides(cfg)
	cl.applyTUICLIOverrides(cfg)
	cl.applySkillCLIOverrides(cfg)
	cl.applyModeCLIOverrides(cfg)
}

func (cl *CascadeLoader) applyScalarCLIOverrides(cfg *Config) {
	for flag, applier := range scalarCLIAppliers {
		if value, ok := cl.cliOverrides[flag]; ok {
			applier(cfg, value)
		}
	}
}

var scalarCLIAppliers = map[string]func(*Config, string){
	"model": func(cfg *Config, value string) {
		if value != "" {
			cfg.ActiveModel = value
		}
	},
	"profile": func(cfg *Config, value string) {
		if value != "" {
			cfg.Mode.Default.Major = internal.MajorMode(value)
		}
	},
	"provider": func(cfg *Config, value string) {
		if value != "" {
			cfg.ActiveProvider = value
		}
	},
	"debug": func(cfg *Config, value string) {
		if value == "true" {
			cfg.Logging.Level = "debug"
		}
	},
	"logfile": func(cfg *Config, value string) {
		if value != "" {
			cfg.Logging.File = value
		}
	},
	"debug_keys": func(cfg *Config, value string) {
		if value == "true" {
			cfg.Logging.TraceKeys = true
		}
	},
	"terminal_log": func(cfg *Config, value string) {
		if value != "" {
			cfg.Logging.TerminalLog = value
		}
	},
	"render_trace": func(cfg *Config, value string) {
		if value != "" {
			cfg.Logging.RenderTrace = value
		}
	},
	"capture_stream": func(cfg *Config, value string) {
		if value != "" {
			cfg.Logging.CaptureStream = value
		}
	},
}

func (cl *CascadeLoader) applyProviderCLIOverrides(cfg *Config) {
	p := cfg.GetActiveProviderConfig()
	if p == nil {
		return
	}
	if endpoint, ok := cl.cliOverrides["endpoint"]; ok && endpoint != "" {
		p.Endpoint = endpoint
	}
	if apiKey, ok := cl.cliOverrides["api_key"]; ok && apiKey != "" {
		p.APIKey = apiKey
	}
}

// cliOverrideModelID is the ID of the ephemeral scratch model created to
// carry model-scalar CLI overrides when no configured model resolves.
const cliOverrideModelID = "cli-override"

// modelScalarCLIFlags are the CLI override keys applied onto the active
// model. When none of them is set there is nothing to apply and the model
// list must be left untouched.
var modelScalarCLIFlags = []string{"temperature", "max_tokens", "reasoning", "thinking_level"}

func (cl *CascadeLoader) applyModelCLIOverrides(cfg *Config) {
	if !cl.hasModelScalarOverrides() {
		return
	}
	m, err := cfg.GetActiveModelConfig()
	if err != nil {
		// No configured model resolves: carry the CLI overrides on a
		// memory-only scratch model bound to the active provider so they
		// still apply for this session. The entry is ephemeral — never
		// persisted and hidden from pickers — so it cannot leak a bogus
		// model into config files (previously this upserted a persistent,
		// provider-less "cli-override" entry on every unresolvable launch).
		p := cfg.GetActiveProviderConfig()
		if p == nil {
			return
		}
		m = ModelConfig{ID: cliOverrideModelID, ProviderID: p.ID, Model: p.DefaultModel, Ephemeral: true}
	}
	if m.ID == "" {
		m.ID = cliOverrideModelID
		m.Ephemeral = true
	}
	cl.applyModelScalars(&m)
	upsertModelConfig(cfg, m)
}

// hasModelScalarOverrides reports whether any model-scalar CLI override
// (temperature, max_tokens, reasoning, thinking_level) is set.
func (cl *CascadeLoader) hasModelScalarOverrides() bool {
	for _, key := range modelScalarCLIFlags {
		if v, ok := cl.cliOverrides[key]; ok && v != "" {
			return true
		}
	}
	return false
}

func (cl *CascadeLoader) applyModelScalars(m *ModelConfig) {
	if temp, ok := cl.cliOverrides["temperature"]; ok && temp != "" {
		if v, err := strconv.ParseFloat(temp, 64); err == nil {
			m.Temperature = v
		}
	}
	if tokens, ok := cl.cliOverrides["max_tokens"]; ok && tokens != "" {
		if v, err := strconv.Atoi(tokens); err == nil {
			m.MaxTokens = v
		}
	}
	if reasoning, ok := cl.cliOverrides["reasoning"]; ok && reasoning != "" {
		v := reasoning == "true"
		m.Reasoning = &v
	}
	if level, ok := cl.cliOverrides["thinking_level"]; ok && level != "" {
		m.ThinkingLevel = level
	}
}

func upsertModelConfig(cfg *Config, m ModelConfig) {
	for i := range cfg.Models {
		if cfg.Models[i].ID == m.ID {
			cfg.Models[i] = m
			return
		}
	}
	cfg.Models = append(cfg.Models, m)
}

// applyIntCLIOverride reads an integer from a CLI override key and, if
// present and parseable, assigns it to the target pointer.
func (cl *CascadeLoader) applyIntCLIOverride(key string, target *int) {
	if v, ok := cl.cliOverrides[key]; ok && v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			*target = parsed
		}
	}
}

func (cl *CascadeLoader) applyExecutionCLIOverrides(cfg *Config) {
	cl.applyIntCLIOverride("max_tool_repeat_total", &cfg.Execution.MaxToolRepeatTotal)
	cl.applyIntCLIOverride("max_tool_repeat", &cfg.Execution.MaxToolRepeatTotal)
	cl.applyIntCLIOverride("max_tool_repeat_consecutive", &cfg.Execution.MaxToolRepeatConsecutive)
	cl.applyIntCLIOverride("max_tool_calls", &cfg.Execution.MaxToolCalls)
	cl.applyIntCLIOverride("max_stream_rounds", &cfg.Execution.MaxStreamRounds)
	cl.applyIntCLIOverride("max_consecutive_tool_rounds", &cfg.Execution.MaxConsecutiveToolRounds)
	cl.applyIntCLIOverride("tool_call_limit_reset_window", &cfg.Execution.ToolCallLimitResetWindow)
	cl.applyCompressionCLIOverride(cfg)
}

func (cl *CascadeLoader) applyCompressionCLIOverride(cfg *Config) {
	compression, ok := cl.cliOverrides["compression"]
	if !ok {
		return
	}
	switch compression {
	case "true":
		on := true
		cfg.ContextCompression.Enabled = &on
		if cfg.ContextCompression.MaxTokens == 0 {
			cfg.ContextCompression.MaxTokens = 8192
		}
		if cfg.ContextCompression.Strategy == "" {
			cfg.ContextCompression.Strategy = AgenticCompressionToolElision
		}
	case "false":
		off := false
		cfg.ContextCompression.Enabled = &off
	}
}

func (cl *CascadeLoader) applyTUICLIOverrides(cfg *Config) {
	if theme, ok := cl.cliOverrides["theme"]; ok && theme != "" {
		cfg.TUI.Theme = theme
	}
	if blocks, ok := cl.cliOverrides["thinking_blocks"]; ok && blocks != "" {
		cfg.TUI.Transparency.ThinkingCollapsed = blocks == "off" || blocks == "false"
	}
	if show, ok := cl.cliOverrides["show_thinking"]; ok && show == "true" {
		cfg.TUI.Transparency.ShowThinking = true
	}
}

func (cl *CascadeLoader) applySkillCLIOverrides(cfg *Config) {
	if mode, ok := cl.cliOverrides["skill_mode"]; ok && mode != "" {
		cfg.Skills.ExecutionMode = mode
	}
}

func (cl *CascadeLoader) applyModeCLIOverrides(cfg *Config) {
	if mode, ok := cl.cliOverrides["execution_mode"]; ok && mode != "" {
		cfg.Execution.Mode = internal.ExecutionMode(mode)
	}
}

// repairActiveProviderModel ensures ActiveProvider and ActiveModel reference
// existing configs when CLI overrides create references without entries.
// loadRegistries fetches remote provider registries and merges their
// provider/model definitions into the config.
func (cl *CascadeLoader) loadRegistries(cfg *Config) error {
	sources := cfg.RegistryLoaders.Sources
	if len(sources) == 0 {
		return nil
	}

	loader := NewRegistryLoader(sources)
	providers, models, err := loader.Load()
	if err != nil {
		return err
	}

	// Merge registry providers (append, since YAML cascade handles dedup).
	cfg.Providers = append(cfg.Providers, providers...)
	cfg.Models = append(cfg.Models, models...)

	return nil
}

// migrateProviderDefaultModels converts deprecated ProviderConfig.DefaultModel
// into explicit ModelConfig entries so the rest of the codebase can rely on
// Models exclusively.
func (c *Config) migrateProviderDefaultModels() {
	for i := range c.Providers {
		p := &c.Providers[i]
		if p.DefaultModel == "" {
			continue
		}
		hasModel := false
		for _, m := range c.Models {
			if m.ProviderID == p.ID {
				hasModel = true
				break
			}
		}
		if !hasModel {
			c.Models = append(c.Models, ModelConfig{
				ID:         p.ID + "/" + p.DefaultModel,
				ProviderID: p.ID,
				Model:      p.DefaultModel,
			})
		}
	}
}

func (c *Config) repairActiveProviderModel() {
	if c.ActiveProvider != "" && c.GetProviderByID(c.ActiveProvider) == nil {
		c.Providers = append(c.Providers, ProviderConfig{ID: c.ActiveProvider})
	}
	if c.ActiveModel != "" && c.GetModelByID(c.ActiveModel) == nil {
		providerID := c.ActiveProvider
		if providerID == "" {
			if p := c.PreferredProvider(); p != nil {
				providerID = p.ID
			}
		}
		c.Models = append(c.Models, ModelConfig{ID: c.ActiveModel, ProviderID: providerID, Model: c.ActiveModel})
	}
}

// sanitizeSelectorSentinels drops selector sentinel values ("__add__",
// "__delete__*", etc.) that older versions could persist as provider or
// model IDs when a picker's '+'/'-' hotkey leaked the sentinel into config.
// It also clears active_provider/active_model when they hold a sentinel so
// repairActiveProviderModel cannot resurrect them. Runs before validation.
//
// Two sentinel shapes are recognized:
//   - bracketed: "__add__", "__custom__" (both prefix and suffix "__")
//   - delete-prefixed: "__delete__<id>" — emitted by the picker's '-' hotkey
// and previously persisted verbatim as a model/provider ID
//     "Model delete"). This shape has no trailing "__" after the real ID, so
//     it needs its own prefix check.
func (c *Config) sanitizeSelectorSentinels() {
	isSentinel := func(id string) bool {
		if strings.HasPrefix(id, "__delete__") {
			return true
		}
		return strings.HasPrefix(id, "__") && strings.HasSuffix(id, "__") && len(id) > 4
	}

	if isSentinel(c.ActiveProvider) {
		c.ActiveProvider = ""
	}
	if isSentinel(c.ActiveModel) {
		c.ActiveModel = ""
	}

	providers := c.Providers[:0]
	for _, p := range c.Providers {
		if !isSentinel(p.ID) {
			providers = append(providers, p)
		}
	}
	c.Providers = providers

	models := c.Models[:0]
	for _, m := range c.Models {
		if !isSentinel(m.ID) && !isSentinel(m.ProviderID) {
			models = append(models, m)
		}
	}
	c.Models = models
}

// migrateLegacyMode converts old config fields to the new mode system.
// Specifically: execution.mode → mode.defaults.<active_profile>
// Called after all cascade layers are loaded but before validation.
// Only migrates if mode.defaults is empty (user hasn't opted into new system)
// AND execution.mode differs from the current built-in default for the active
// major mode. This ensures the legacy value is preserved without overriding
// the new built-in defaults.
func migrateLegacyMode(cfg *Config) {
	if len(cfg.Mode.Defaults) > 0 {
		return
	}
	if cfg.Execution.Mode == "" {
		return
	}

	major := cfg.Mode.Default.Major
	if major == "" {
		major = internal.MajorCoder
	}
	if string(cfg.Execution.Mode) == string(DefaultAutonomyForMajor(major)) {
		return
	}

	if cfg.Mode.Defaults == nil {
		cfg.Mode.Defaults = make(map[internal.MajorMode]internal.AutonomyLevel)
	}
	cfg.Mode.Defaults[major] = internal.AutonomyLevel(cfg.Execution.Mode)
}

// Save writes the given config to ~/.goa/config.yaml.
// Reload re-reads config from all cascade layers and returns the result.
func (cl *CascadeLoader) Reload() (*Config, error) {
	return cl.Load()
}

// SaveProjectConfig writes the given config to .goa/config.yaml in the project directory.
func (cl *CascadeLoader) SaveProjectConfig(cfg *Config) error {
	cl.writeMu.Lock()
	defer cl.writeMu.Unlock()
	projectConfigDir := filepath.Join(cl.projectDir, ".goa")
	if err := os.MkdirAll(projectConfigDir, 0755); err != nil {
		return fmt.Errorf("create project config dir: %w", err)
	}
	path := filepath.Join(projectConfigDir, "config.yaml")

	// Field-scoped save: this entry point is used by /mode and /autonomy to
	// persist the active major mode and per-mode autonomy. Start from the
	// on-disk config (when present) and overlay ONLY those mode fields, so
	// unrelated settings keep their persisted values. Previously this marshaled
	// the entire in-memory config, so a stale in-memory Tools.Enabled (e.g.
	// goal toggled earlier in the session) was written back over the on-disk
	// value — silently reverting the user's tool toggles on the next session.
	data, err := os.ReadFile(path)
	switch {
	case err != nil && !os.IsNotExist(err):
		return fmt.Errorf("read project config: %w", err)
	case err != nil:
		// No project file yet: persist ONLY the mode section this entry
		// point owns. Writing the full merged config would bake embedded
		// defaults and home-layer values into the project layer, where they
		// silently shadow every later home-config edit (startup
		// /config showed the embedded default compression strategy "micro"
		// instead of the home-config value on every launch).
		return writeModeOnlyProjectConfig(path, cfg.Mode)
	}

	onDisk := &Config{}
	if uerr := yaml.Unmarshal(data, onDisk); uerr != nil {
		// Unreadable project file: same mode-only scope — never dump the
		// full merged config into the project layer.
		return writeModeOnlyProjectConfig(path, cfg.Mode)
	}
	merged := onDisk
	merged.Mode = cfg.Mode

	saveCfg := merged.DeepCopy()
	saveCfg.FirstRun = false
	saveCfg.ConfigDir = ""
	saveCfg.Models = persistableModels(saveCfg.Models)

	out, err := yaml.Marshal(saveCfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(path, out, 0644); err != nil {
		return fmt.Errorf("write project config: %w", err)
	}
	return nil
}

// writeModeOnlyProjectConfig creates a project config file containing only
// the mode section — the single field scope SaveProjectConfig is documented
// to own. It marshals a map rather than a sparse Config because most Config
// fields lack omitempty: a sparse Config would emit zero-value sections
// (e.g. tools.terminal.sandbox.enabled: false) that would shadow home-layer
// values on the next cascade load.
func writeModeOnlyProjectConfig(path string, mode ModeConfig) error {
	out, err := yaml.Marshal(map[string]ModeConfig{"mode": mode})
	if err != nil {
		return fmt.Errorf("marshal mode config: %w", err)
	}
	if err := os.WriteFile(path, out, 0644); err != nil {
		return fmt.Errorf("write project config: %w", err)
	}
	return nil
}

// persistableModels strips ephemeral (memory-only) model entries so they
// are never written to config files.
func persistableModels(models []ModelConfig) []ModelConfig {
	out := make([]ModelConfig, 0, len(models))
	for _, m := range models {
		if !m.Ephemeral {
			out = append(out, m)
		}
	}
	return out
}

// HomeConfigPath returns the absolute path of the home config file
// (<goa home>/.goa/config.yaml) this loader reads from and Save writes to.
// The goa home honors --home / GOA_HOME overrides via internal.GoaHome.
func (cl *CascadeLoader) HomeConfigPath() string {
	return filepath.Join(cl.homeDir, ".goa", "config.yaml")
}

// skillListsOnDisk reads only the skills.enabled / skills.disabled lists from
// the config file at path. ok=false when the file is missing or unreadable —
// callers then leave the in-memory lists as-is rather than forcing a value.
//
// Snapshot writers (Save, SaveHomeProvidersAndModels, SaveProjectConfig) use
// this to PRESERVE the on-disk skill lists instead of re-emitting the lists
// from the in-memory config they were handed. Skill enable/disable is owned by
// the field-scoped toggle write (persistSkillToggle), so the on-disk value is
// authoritative and a stale in-memory snapshot must never overwrite it — that
// lost update is what made disabled skills spontaneously re-enable.
func skillListsOnDisk(path string) (enabled, disabled []string, ok bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, false
	}
	var raw struct {
		Skills struct {
			Enabled  []string `yaml:"enabled"`
			Disabled []string `yaml:"disabled"`
		} `yaml:"skills"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, nil, false
	}
	return raw.Skills.Enabled, raw.Skills.Disabled, true
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
	// resurrect a skill the user disabled (skills re-enable bug).
	if en, dis, ok := skillListsOnDisk(cl.HomeConfigPath()); ok {
		saveCfg.Skills.Enabled = en
		saveCfg.Skills.Disabled = dis
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

// SaveHomeField updates a single scalar field in ~/.goa/config.yaml without
// overwriting other settings. It reads the existing file (or creates a minimal
// one), walks the nested key path, and sets the value. Missing intermediate
// maps are created automatically.
func (cl *CascadeLoader) SaveHomeField(path []string, value any) error {
	if len(path) == 0 {
		return fmt.Errorf("empty field path")
	}

	cl.writeMu.Lock()
	defer cl.writeMu.Unlock()
	configDir := filepath.Join(cl.homeDir, ".goa")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	pathYaml := filepath.Join(configDir, "config.yaml")

	var root yaml.Node
	data, err := os.ReadFile(pathYaml)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("read home config: %w", err)
		}
		root.Kind = yaml.DocumentNode
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.MappingNode})
	} else {
		if err := yaml.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("unmarshal home config: %w", err)
		}
		if len(root.Content) == 0 {
			root.Content = append(root.Content, &yaml.Node{Kind: yaml.MappingNode})
		}
	}

	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		doc.Kind = yaml.MappingNode
	}

	if err := setYamlNode(doc, path, value); err != nil {
		return err
	}

	out, err := yaml.Marshal(&root)
	if err != nil {
		return fmt.Errorf("marshal home config: %w", err)
	}
	if err := os.WriteFile(pathYaml, out, 0644); err != nil {
		return fmt.Errorf("write home config: %w", err)
	}
	return nil
}

// SaveProjectField updates a single scalar field in .goa/config.yaml in the
// project directory without overwriting other settings. It reads the existing
// file (or creates a minimal one), walks the nested key path, and sets the
// value. Missing intermediate maps are created automatically.
func (cl *CascadeLoader) SaveProjectField(path []string, value any) error {
	if len(path) == 0 {
		return fmt.Errorf("empty field path")
	}

	cl.writeMu.Lock()
	defer cl.writeMu.Unlock()
	configDir := filepath.Join(cl.projectDir, ".goa")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("create project config dir: %w", err)
	}
	pathYaml := filepath.Join(configDir, "config.yaml")

	var root yaml.Node
	data, err := os.ReadFile(pathYaml)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("read project config: %w", err)
		}
		// Create a minimal document so the field is written (matches SaveHomeField).
		root.Kind = yaml.DocumentNode
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.MappingNode})
	} else if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("unmarshal project config: %w", err)
	}
	if len(root.Content) == 0 {
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.MappingNode})
	}

	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		doc.Kind = yaml.MappingNode
	}

	if err := setYamlNode(doc, path, value); err != nil {
		return err
	}

	out, err := yaml.Marshal(&root)
	if err != nil {
		return fmt.Errorf("marshal project config: %w", err)
	}
	if err := os.WriteFile(pathYaml, out, 0644); err != nil {
		return fmt.Errorf("write project config: %w", err)
	}
	return nil
}

// SaveProjectFieldValue writes an arbitrary value (including maps and slices)
// at the given path in the project .goa/config.yaml, preserving other
// settings. Use for whole sub-configs such as an MCP server definition.
func (cl *CascadeLoader) SaveProjectFieldValue(path []string, value any) error {
	return cl.editProjectConfig(func(doc *yaml.Node) error {
		return setYamlNodeValue(doc, path, value)
	})
}

// DeleteProjectField removes the key at the given path from the project
// .goa/config.yaml. It is a no-op when the key (or file) does not exist.
func (cl *CascadeLoader) DeleteProjectField(path []string) error {
	if len(path) == 0 {
		return fmt.Errorf("empty field path")
	}
	return cl.editProjectConfig(func(doc *yaml.Node) error {
		deleteYamlNode(doc, path)
		return nil
	})
}

// SaveHomeFieldValue writes an arbitrary value (including maps and slices) at
// the given path in ~/.goa/config.yaml, preserving other settings.
func (cl *CascadeLoader) SaveHomeFieldValue(path []string, value any) error {
	return cl.editHomeConfig(func(doc *yaml.Node) error {
		return setYamlNodeValue(doc, path, value)
	})
}

// SaveLocalFieldValue writes an arbitrary value (including maps and slices) at
// the given path in the project local layer .goa/config.local.yaml,
// preserving other local settings.
func (cl *CascadeLoader) SaveLocalFieldValue(path []string, value any) error {
	return cl.editLocalConfig(func(doc *yaml.Node) error {
		return setYamlNodeValue(doc, path, value)
	})
}

// DeleteHomeField removes the key at the given path from ~/.goa/config.yaml.
// It is a no-op when the key (or file) does not exist.
func (cl *CascadeLoader) DeleteHomeField(path []string) error {
	if len(path) == 0 {
		return fmt.Errorf("empty field path")
	}
	return cl.editHomeConfig(func(doc *yaml.Node) error {
		deleteYamlNode(doc, path)
		return nil
	})
}

// editHomeConfig applies edit to ~/.goa/config.yaml (see editConfigFile).
func (cl *CascadeLoader) editHomeConfig(edit func(doc *yaml.Node) error) error {
	return cl.editConfigFile(filepath.Join(cl.homeDir, ".goa"), "config.yaml", "home", edit)
}

// editProjectConfig applies edit to the project .goa/config.yaml (see
// editConfigFile).
func (cl *CascadeLoader) editProjectConfig(edit func(doc *yaml.Node) error) error {
	return cl.editConfigFile(filepath.Join(cl.projectDir, ".goa"), "config.yaml", "project", edit)
}

// editLocalConfig applies edit to the project local layer
// .goa/config.local.yaml (see editConfigFile).
func (cl *CascadeLoader) editLocalConfig(edit func(doc *yaml.Node) error) error {
	return cl.editConfigFile(filepath.Join(cl.projectDir, ".goa"), "config.local.yaml", "local", edit)
}

// editConfigFile loads the named config file under configDir (creating a
// minimal document when missing), applies edit to the root mapping, and
// writes it back. The label ("home"/"project"/"local") scopes error
// messages.
func (cl *CascadeLoader) editConfigFile(configDir, fileName, label string, edit func(doc *yaml.Node) error) error {
	cl.writeMu.Lock()
	defer cl.writeMu.Unlock()
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("create %s config dir: %w", label, err)
	}
	pathYaml := filepath.Join(configDir, fileName)

	var root yaml.Node
	data, err := os.ReadFile(pathYaml)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("read %s config: %w", label, err)
		}
		root.Kind = yaml.DocumentNode
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.MappingNode})
	} else if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("unmarshal %s config: %w", label, err)
	}
	if len(root.Content) == 0 {
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.MappingNode})
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		doc.Kind = yaml.MappingNode
	}
	if err := edit(doc); err != nil {
		return err
	}
	out, err := yaml.Marshal(&root)
	if err != nil {
		return fmt.Errorf("marshal %s config: %w", label, err)
	}
	if err := os.WriteFile(pathYaml, out, 0644); err != nil {
		return fmt.Errorf("write %s config: %w", label, err)
	}
	return nil
}

// deleteYamlNode removes the key at path from a mapping tree, pruning empty
// parent maps. Missing keys are ignored.
func deleteYamlNode(node *yaml.Node, path []string) {
	if node.Kind != yaml.MappingNode {
		return
	}
	key := path[0]
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value != key {
			continue
		}
		if len(path) == 1 {
			node.Content = append(node.Content[:i], node.Content[i+2:]...)
			return
		}
		deleteYamlNode(node.Content[i+1], path[1:])
		if len(node.Content[i+1].Content) == 0 {
			node.Content = append(node.Content[:i], node.Content[i+2:]...)
		}
		return
	}
}

// setYamlNode walks a YAML mapping tree and sets the scalar at the given path.
func setYamlNode(node *yaml.Node, path []string, value interface{}) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("expected mapping node at %q", strings.Join(path, "."))
	}
	key := path[0]
	var child *yaml.Node
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			child = node.Content[i+1]
			break
		}
	}
	if child == nil {
		child = &yaml.Node{Kind: yaml.MappingNode}
		node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, child)
	}
	if len(path) == 1 {
		child.Kind = yaml.ScalarNode
		child.Tag = ""
		child.Value = fmt.Sprintf("%v", value)
		return nil
	}
	if child.Kind != yaml.MappingNode {
		child.Kind = yaml.MappingNode
	}
	return setYamlNode(child, path[1:], value)
}

// setYamlNodeValue walks a YAML mapping tree and replaces the node at the
// given path with an encoding of value (which may be a map, slice, or scalar).
// Unlike setYamlNode (scalar-only), it marshals value into a YAML node, so
// whole sub-configs (e.g. an MCP server definition) can be written. Missing
// intermediate maps are created automatically.
func setYamlNodeValue(node *yaml.Node, path []string, value any) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("expected mapping node at %q", strings.Join(path, "."))
	}
	key := path[0]
	var childIdx = -1
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			childIdx = i + 1
			break
		}
	}
	if len(path) == 1 {
		encoded, err := valueToYAMLNode(value)
		if err != nil {
			return err
		}
		if childIdx >= 0 {
			node.Content[childIdx] = encoded
		} else {
			node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, encoded)
		}
		return nil
	}
	var child *yaml.Node
	if childIdx >= 0 {
		child = node.Content[childIdx]
		if child.Kind != yaml.MappingNode {
			child.Kind = yaml.MappingNode
			child.Content = nil
		}
	} else {
		child = &yaml.Node{Kind: yaml.MappingNode}
		node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, child)
	}
	return setYamlNodeValue(child, path[1:], value)
}

// valueToYAMLNode marshals an arbitrary value into a YAML node.
func valueToYAMLNode(value any) (*yaml.Node, error) {
	data, err := yaml.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal value: %w", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("reparse value: %w", err)
	}
	if len(doc.Content) == 0 {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: ""}, nil
	}
	return doc.Content[0], nil
}
