// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/pijalu/goa/internal"
)

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
//
// and previously persisted verbatim as a model/provider ID
//
//	"Model delete"). This shape has no trailing "__" after the real ID, so
//	it needs its own prefix check.
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

// sanitizeDanglingActiveTeam clears teams.active when it names a team that is
// not defined in teams.definitions. The selection persists in the project
// LOCAL layer (.goa/config.local.yaml) while definitions live in the home
// layer, so the two can desync (team deleted via an older build, edited by
// hand, or the local file copied across projects). Dropping the selection is
// equivalent to /team:off — safe and self-healing — whereas validation would
// otherwise hard-fail startup. Runs before validation.
func (c *Config) sanitizeDanglingActiveTeam() {
	if c.Teams.Active == "" {
		return
	}
	if _, ok := c.Teams.Definitions[c.Teams.Active]; ok {
		return
	}
	fmt.Fprintf(os.Stderr, "Warning: teams.active %q is not defined in teams.definitions — clearing (team deleted?)\n", c.Teams.Active)
	c.Teams.Active = ""
}

// sanitizeDanglingCompressionModels drops context_compression.per_model
// entries whose model ID is no longer in the model catalog. Overrides persist
// in whichever layer set them while models can be deleted from another layer,
// via the model picker, or by hand; the stale entry then hard-failed startup
// validation ("no model with id %q is configured"). A dangling override is
// inert at runtime — nothing resolves it — so dropping it with a warning is
// equivalent to never having configured it, under the same
// heal-before-validate contract as sanitizeDanglingActiveTeam. Validate()
// deliberately keeps rejecting unknown model IDs so interactive edits
// (/config set on a per_model.<id>.* key) still catch typos. Runs before
// validation, and after model repair so repaired-in models are kept.
func (c *Config) sanitizeDanglingCompressionModels() {
	for id := range c.ContextCompression.PerModel {
		if c.GetModelByID(id) != nil {
			continue
		}
		fmt.Fprintf(os.Stderr, "Warning: context_compression.per_model %q references an unconfigured model — dropping stale override (model deleted?)\n", id)
		delete(c.ContextCompression.PerModel, id)
	}
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
