// SPDX-License-Identifier: GPL-3.0-or-later

package commands

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/spinner"
)

func handleConfigAdd(ctx core.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: /config:add provider <id> <endpoint> [api-key]  or  /config:add model <id> <provider-id> <model-name>")
	}
	switch args[0] {
	case "provider":
		return addProvider(ctx, args[1:])
	case "model":
		return addModel(ctx, args[1:])
	default:
		return fmt.Errorf("unknown add target: %q (use 'provider' or 'model')", args[0])
	}
}

func addProvider(ctx core.Context, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: /config:add provider <id> <endpoint> [api-key]")
	}
	return doAddProvider(ctx.Config, ctx.ConfigSaver, ctx, args[0], args[1], strings.Join(args[2:], ""))
}

func doAddProvider(cfg *config.Config, saver config.ConfigSaver, out core.OutputWriter, id, endpoint, apiKey string) error {
	for i := range cfg.Providers {
		if cfg.Providers[i].ID != id {
			continue
		}
		cfg.Providers[i].Endpoint = endpoint
		cfg.Providers[i].APIKey = apiKey
		if cfg.Providers[i].Name == "" {
			cfg.Providers[i].Name = id
		}
		return saveAndReport(out, saver, cfg, "provider", id)
	}
	cfg.Providers = append(cfg.Providers, config.ProviderConfig{
		ID:       id,
		Name:     id,
		Endpoint: endpoint,
		APIKey:   apiKey,
	})
	return saveAndReport(out, saver, cfg, "provider", id)
}

func addModel(ctx core.Context, args []string) error {
	if len(args) < 3 {
		return fmt.Errorf("usage: /config:add model <id> <provider-id> <model-name>")
	}
	return doAddModel(ctx.Config, ctx.ConfigSaver, ctx, args[0], args[1], args[2])
}

func doAddModel(cfg *config.Config, saver config.ConfigSaver, out core.OutputWriter, id, providerID, modelName string) error {
	for i := range cfg.Models {
		if cfg.Models[i].ID != id {
			continue
		}
		cfg.Models[i].ProviderID = providerID
		cfg.Models[i].Model = modelName
		if cfg.Models[i].Name == "" {
			cfg.Models[i].Name = modelName
		}
		return saveAndReport(out, saver, cfg, "model", id)
	}
	cfg.Models = append(cfg.Models, config.ModelConfig{
		ID:         id,
		Name:       modelName,
		ProviderID: providerID,
		Model:      modelName,
	})
	return saveAndReport(out, saver, cfg, "model", id)
}

func saveAndReport(out core.OutputWriter, saver config.ConfigSaver, cfg *config.Config, kind, id string) error {
	if saver == nil {
		writeFmt(out, "Added %s %s (in memory; no saver)\n", kind, id)
		return nil
	}
	if err := saver.Save(cfg); err != nil {
		writeFmt(out, "Added %s %s in memory, but failed to save: %v\n", kind, id, err)
		return nil
	}
	writeFmt(out, "Added %s %s\n", kind, id)
	return nil
}

// handleConfigRemove removes a provider or model from the configuration.
func handleConfigRemove(ctx core.Context, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: /config:remove provider <id>  or  /config:remove model <id>")
	}
	switch args[0] {
	case "provider":
		return removeProvider(ctx, args[1:])
	case "model":
		return removeModel(ctx, args[1:])
	default:
		return fmt.Errorf("unknown remove target: %q (use 'provider' or 'model')", args[0])
	}
}

func removeProvider(ctx core.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: /config:remove provider <id>")
	}
	id := args[0]
	cfg := ctx.Config
	for i, p := range cfg.Providers {
		if p.ID != id {
			continue
		}
		cfg.Providers = append(cfg.Providers[:i], cfg.Providers[i+1:]...)
		// Also remove models associated with this provider
		var remaining []config.ModelConfig
		for _, m := range cfg.Models {
			if m.ProviderID != id {
				remaining = append(remaining, m)
			}
		}
		cfg.Models = remaining
		if cfg.ActiveProvider == id {
			cfg.ActiveProvider = ""
			cfg.ActiveModel = ""
		}
		return saveAndReport(ctx, ctx.ConfigSaver, cfg, "provider", id)
	}
	return fmt.Errorf("provider %q not found", id)
}

func removeModel(ctx core.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: /config:remove model <id>")
	}
	id := args[0]
	cfg := ctx.Config
	for i, m := range cfg.Models {
		if m.ID != id {
			continue
		}
		cfg.Models = append(cfg.Models[:i], cfg.Models[i+1:]...)
		if cfg.ActiveModel == id {
			cfg.ActiveModel = ""
		}
		return saveAndReport(ctx, ctx.ConfigSaver, cfg, "model", id)
	}
	return fmt.Errorf("model %q not found", id)
}

func handleConfigSet(ctx core.Context, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: /config:set <key> <value>")
	}
	key := args[0]
	value := strings.Join(args[1:], " ")
	return applyConfigSet(ctx, key, value)
}

// configSavePaths maps user-facing dotted keys to the canonical YAML path used
// for persistence. Most keys map 1:1, but shortcuts like "thinking_level"
// expand to nested config fields.
var configSavePaths = map[string][]string{
	"thinking_level": {"thinking_levels", "main_agent"},
}

// modeDefaultsPath returns the YAML path for persisting the autonomy level of
// the current major mode. This makes /config:set execution.mode yolo survive a
// restart even when the previous session had a non-yolo default.
func modeDefaultsPath(cfg *config.Config) []string {
	major := cfg.DefaultModeState().Major
	if major == "" {
		major = internal.MajorCoder
	}
	return []string{"mode", "defaults", string(major)}
}

func applyConfigSet(ctx core.Context, key, value string) error {
	path := strings.Split(key, ".")
	prevProvider := ctx.Config.ActiveProvider
	if err := setConfigField(ctx.Config, path, value); err != nil {
		writeFmt(ctx, "Invalid value for %s: %v\n", key, err)
		return nil
	}
	// execution.mode needs the new-mode-system default updated before we sync
	// the runtime agent mode, otherwise the agent manager keeps the old autonomy.
	if key == "execution.mode" {
		updateModeDefault(ctx.Config, value)
	}
	if err := syncRuntimeConfig(ctx, key, value); err != nil {
		writeFmt(ctx, "%v\n", err)
		return nil
	}
	if ctx.ConfigSaver == nil {
		writeFmt(ctx, "Set %s = %s (in memory; no saver)\n", key, value)
		return nil
	}
	if err := persistConfigValue(ctx, key, path, value); err != nil {
		writeFmt(ctx, "%v\n", err)
		return nil
	}
	// Changing the active model may also change the provider; persist and
	// propagate that switch so the next turn uses the new provider+model.
	if key == "active_model" && ctx.Config.ActiveProvider != prevProvider {
		if err := ctx.ConfigSaver.SaveHomeField([]string{"active_provider"}, ctx.Config.ActiveProvider); err != nil {
			writeFmt(ctx, "set active_model = %s (provider switch to %s not persisted: %v)\n", value, ctx.Config.ActiveProvider, err)
		} else {
			writeFmt(ctx, "Set active_provider = %s\n", ctx.Config.ActiveProvider)
		}
		propagateModelSwitch(ctx, ctx.Config)
	}
	writeFmt(ctx, "Set %s = %s\n", key, value)
	ctx.FooterRefresh()
	return nil
}

func syncRuntimeConfig(ctx core.Context, key, value string) error {
	// Loop-detector overrides sync straight to the detector; they do not
	// require a running agent, so handle them before the AgentManager guard.
	switch key {
	case "execution.disable_thinking_loop_detection":
		if ctx.LoopDetector != nil {
			ctx.LoopDetector.SetPersistOverride("think", boolPtrValue(ctx.Config.Execution.DisableThinkingLoopDetection))
		}
		return nil
	case "execution.disable_tool_loop_detection":
		if ctx.LoopDetector != nil {
			ctx.LoopDetector.SetPersistOverride("tool", boolPtrValue(ctx.Config.Execution.DisableToolLoopDetection))
		}
		return nil
	}
	if ctx.AgentManager == nil {
		return nil
	}
	switch key {
	case "thinking_level":
		if err := ctx.AgentManager.SetThinkingLevel(value); err != nil {
			return fmt.Errorf("set %s = %s (in memory, but failed to sync runtime: %v)", key, value, err)
		}
	case "mode.default.major", "execution.mode":
		ctx.AgentManager.SetMode(ctx.Config.DefaultModeState())
	default:
		// context_compression.* changes apply to the live agent immediately
		// (thresholds, strategy, max_tokens, on_context_error).
		if strings.HasPrefix(key, "context_compression.") {
			ctx.AgentManager.RefreshContextCompression()
		}
	}
	return nil
}

func persistConfigValue(ctx core.Context, key string, path []string, value string) error {
	savePath := path
	if override, ok := configSavePaths[key]; ok {
		savePath = override
	}
	if err := ctx.ConfigSaver.SaveHomeField(savePath, scalarValue(value)); err != nil {
		return fmt.Errorf("set %s = %s (in memory, but failed to persist: %v)", key, value, err)
	}
	if key == "execution.mode" {
		if err := persistModeDefault(ctx, value); err != nil {
			return fmt.Errorf("set %s = %s (mode default not persisted: %v)", key, value, err)
		}
	}
	return nil
}

func updateModeDefault(cfg *config.Config, value string) {
	if cfg.Mode.Defaults == nil {
		cfg.Mode.Defaults = make(map[internal.MajorMode]internal.AutonomyLevel)
	}
	major := cfg.DefaultModeState().Major
	cfg.Mode.Defaults[major] = internal.AutonomyLevel(value)
}

func persistModeDefault(ctx core.Context, value string) error {
	return ctx.ConfigSaver.SaveHomeField(modeDefaultsPath(ctx.Config), scalarValue(value))
}

// handleConfigTemp handles /config:temp subcommands for session-level
// temporary overrides. These are not persisted — they only affect the
// current session and are cleared on restart/session end.
//
// Supported overrides:
//
//	/config:temp:think_loop_detection:off  — disable thinking-loop detection
//	/config:temp:think_loop_detection:on   — enable thinking-loop detection
//	/config:temp:tool_loop_detection:off   — disable tool-call loop detection
//	/config:temp:tool_loop_detection:on    — enable tool-call loop detection
func handleConfigTemp(ctx core.Context, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: /config:temp <setting> <on|off>")
	}
	setting := args[0]
	value := args[1]

	enabled := true
	switch strings.ToLower(value) {
	case "on", "true", "1", "yes":
		enabled = true
	case "off", "false", "0", "no":
		enabled = false
	default:
		return fmt.Errorf("value must be 'on' or 'off', got %q", value)
	}

	switch setting {
	case "think_loop_detection":
		return applyTempOverride(ctx, "think", enabled)
	case "tool_loop_detection":
		return applyTempOverride(ctx, "tool", enabled)
	default:
		return fmt.Errorf("unknown temp setting: %q (use 'think_loop_detection' or 'tool_loop_detection')", setting)
	}
}

// applyTempOverride applies a session-level temp override to the loop detector.
// Uses ctx.Flash (not writeFmt) so the confirmation is visible even when the
// command is internal and its output buffer is discarded by the short-circuit
// in handleSlashCommand.
func applyTempOverride(ctx core.Context, kind string, enabled bool) error {
	ld := ctx.LoopDetector
	if ld == nil {
		ctx.Flash("Loop detector not available (headless mode). Override not applied.")
		return nil
	}
	ld.SetTempOverride(kind, !enabled) // disabled=true means detection is OFF
	state := "enabled"
	if !enabled {
		state = "disabled"
	}
	label := "thinking-loop detection"
	if kind == "tool" {
		label = "tool-call loop detection"
	}
	ctx.Flash(fmt.Sprintf("Temporary: %s %s (current session only). To persist across sessions: /config set execution.disable_%s_loop_detection %v",
		label, state, tempKindToConfigInfix(kind), !enabled))
	return nil
}

// tempKindToConfigInfix maps the temp override kind to the infix used in the
// persisted config key execution.disable_<infix>_loop_detection.
func tempKindToConfigInfix(kind string) string {
	if kind == "think" {
		return "thinking"
	}
	return "tool"
}

// scalarValue converts common UI values to scalars suitable for YAML.
func scalarValue(value string) any {
	if v, err := strconv.ParseBool(value); err == nil {
		return v
	}
	if v, err := strconv.Atoi(value); err == nil {
		return v
	}
	return value
}

// configSetter updates a single config field from a string value.
type configSetter func(cfg *config.Config, value string) error

var configSetters = map[string]configSetter{
	"mode.default.major":                             setActiveMajor,
	"active_provider":                                setString(func(cfg *config.Config) *string { return &cfg.ActiveProvider }),
	"active_model":                                   setActiveModel,
	"multi_agent.companion_model":                    setStringWithValidate(func(cfg *config.Config) *string { return &cfg.MultiAgent.CompanionModel }, validateActiveModel),
	"execution.mode":                                 setExecutionMode,
	"mode.plan_file_path":                            setString(func(cfg *config.Config) *string { return &cfg.Mode.PlanFilePath }),
	"execution.max_tool_calls":                       setInt(func(cfg *config.Config) *int { return &cfg.Execution.MaxToolCalls }),
	"execution.max_tool_repeat_total":                setInt(func(cfg *config.Config) *int { return &cfg.Execution.MaxToolRepeatTotal }),
	"execution.max_tool_repeat_consecutive":          setInt(func(cfg *config.Config) *int { return &cfg.Execution.MaxToolRepeatConsecutive }),
	"execution.max_tool_repeat":                      setInt(func(cfg *config.Config) *int { return &cfg.Execution.MaxToolRepeatTotal }),
	"tui.theme":                                      setString(func(cfg *config.Config) *string { return &cfg.TUI.Theme }),
	"tui.spinner":                                    setSpinnerName,
	"tui.transparency.show_thinking":                 setBool(func(cfg *config.Config) *bool { return &cfg.TUI.Transparency.ShowThinking }),
	"tui.transparency.thinking_collapsed":            setThinkingCollapsed,
	"logging.level":                                  setString(func(cfg *config.Config) *string { return &cfg.Logging.Level }),
	"logging.file":                                   setString(func(cfg *config.Config) *string { return &cfg.Logging.File }),
	"thinking_level":                                 setThinkingLevel,
	"multi_agent.enabled":                            setBool(func(cfg *config.Config) *bool { return &cfg.MultiAgent.Enabled }),
	"multi_agent.companion_provider":                 setString(func(cfg *config.Config) *string { return &cfg.MultiAgent.CompanionProvider }),
	"context_compression.enabled":                    setBool(func(cfg *config.Config) *bool { return &cfg.ContextCompression.Enabled }),
	"context_compression.strategy":                   setCompressionStrategy,
	"context_compression.threshold_percent":          setIntRange(func(cfg *config.Config) *int { return &cfg.ContextCompression.ThresholdPercent }, 0, 100),
	"context_compression.thresholds.soft_percent":    setIntRange(func(cfg *config.Config) *int { return &cfg.ContextCompression.Thresholds.SoftPercent }, 0, 100),
	"context_compression.thresholds.trigger_percent": setIntRange(func(cfg *config.Config) *int { return &cfg.ContextCompression.Thresholds.TriggerPercent }, 0, 100),
	"context_compression.thresholds.hard_percent":    setIntRange(func(cfg *config.Config) *int { return &cfg.ContextCompression.Thresholds.HardPercent }, 0, 100),
	"context_compression.max_tokens":                 setInt(func(cfg *config.Config) *int { return &cfg.ContextCompression.MaxTokens }),
	"context_compression.on_context_error":           setBool(func(cfg *config.Config) *bool { return &cfg.ContextCompression.OnContextError }),
	"execution.loop_warning":                         setInt(func(cfg *config.Config) *int { return &cfg.Execution.LoopWarning }),
	"execution.loop_interrupt":                       setInt(func(cfg *config.Config) *int { return &cfg.Execution.LoopInterrupt }),
	"execution.disable_thinking_loop_detection":      setBoolPtr(func(cfg *config.Config) **bool { return &cfg.Execution.DisableThinkingLoopDetection }),
	"execution.disable_tool_loop_detection":          setBoolPtr(func(cfg *config.Config) **bool { return &cfg.Execution.DisableToolLoopDetection }),
	"execution.disable_tool_budget":                  setBool(func(cfg *config.Config) *bool { return &cfg.Execution.DisableToolBudget }),
	"skills.execution_mode":                          setString(func(cfg *config.Config) *string { return &cfg.Skills.ExecutionMode }),
	"tools.bash.enable_complexity_analysis":          setBool(func(cfg *config.Config) *bool { return &cfg.Tools.Bash.EnableComplexityAnalysis }),
	"tools.bash.jail":                                setBool(func(cfg *config.Config) *bool { return &cfg.Tools.Bash.Jail }),
	"tools.bash.max_complexity_score":                setInt(func(cfg *config.Config) *int { return &cfg.Tools.Bash.MaxComplexityScore }),
	"tools.terminal.sandbox.enabled":                 setBool(func(cfg *config.Config) *bool { return &cfg.Tools.Terminal.Sandbox.Enabled }),
	"tools.enabled.goal":                             setBool(func(cfg *config.Config) *bool { return &cfg.Tools.Enabled.Goal }),
}

func setActiveMajor(cfg *config.Config, value string) error {
	cfg.SetActiveMajor(value)
	return nil
}

// setString creates a configSetter that sets a string field from a string value.
// Setter names like "active_model" can pass additional validation.
func setString(getter func(*config.Config) *string) configSetter {
	return func(cfg *config.Config, value string) error {
		*getter(cfg) = value
		return nil
	}
}

// setStringWithValidate creates a configSetter that validates the value before setting.
func setStringWithValidate(getter func(*config.Config) *string, validate func(string) error) configSetter {
	return func(cfg *config.Config, value string) error {
		if err := validate(value); err != nil {
			return err
		}
		*getter(cfg) = value
		return nil
	}
}

// setActiveModel sets the active model and follows the model's configured
// provider when it differs from the current provider. The change is rejected
// if the model's provider is not configured.
func setActiveModel(cfg *config.Config, value string) error {
	if err := validateActiveModel(value); err != nil {
		return err
	}
	if np := providerIDForModel(cfg, value); np != "" && np != cfg.ActiveProvider {
		if cfg.GetProviderByID(np) == nil {
			return fmt.Errorf("provider %q for model %q is not configured", np, value)
		}
		cfg.ActiveProvider = np
	}
	cfg.ActiveModel = value
	return nil
}

// validateActiveModel rejects values that look like rendered footer display strings
// (e.g., "llama3 \u2022 high | llama3 (companion) \u2022 medium") instead of bare model IDs.
func validateActiveModel(value string) error {
	// Footer display strings contain " | " between main and companion model parts.
	if strings.Contains(value, " | ") {
		return fmt.Errorf("invalid model value: %q looks like a TUI footer display string, not a model ID", value)
	}
	// Footer display strings contain thinking level indicators (\u2022 = bullet).
	if strings.Contains(value, " \u2022 ") {
		return fmt.Errorf("invalid model value: %q contains thinking level indicator, use bare model ID instead", value)
	}
	return nil
}

func setInt(getter func(*config.Config) *int) configSetter {
	return func(cfg *config.Config, value string) error {
		v, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		*getter(cfg) = v
		return nil
	}
}

func setBool(getter func(*config.Config) *bool) configSetter {
	return func(cfg *config.Config, value string) error {
		*getter(cfg) = parseBool(value)
		return nil
	}
}

// setBoolPtr creates a configSetter for tri-state *bool config fields (nil =
// unset/default). The setter always materialises an explicit value.
func setBoolPtr(getter func(*config.Config) **bool) configSetter {
	return func(cfg *config.Config, value string) error {
		v := parseBool(value)
		*getter(cfg) = &v
		return nil
	}
}

func setThinkingCollapsed(cfg *config.Config, value string) error {
	cfg.TUI.Transparency.ThinkingCollapsed = parseToggle(value, true)
	return nil
}

var validThinkingLevels = map[string]bool{
	"off":     true,
	"minimal": true,
	"low":     true,
	"medium":  true,
	"high":    true,
	"xhigh":   true,
}

func setThinkingLevel(cfg *config.Config, value string) error {
	if !validThinkingLevels[strings.ToLower(value)] {
		return fmt.Errorf("thinking_level must be one of: off, minimal, low, medium, high, xhigh")
	}
	cfg.ThinkingLevels.MainAgent = strings.ToLower(value)
	return nil
}

func setExecutionMode(cfg *config.Config, value string) error {
	switch strings.ToLower(value) {
	case "yolo", "solo", "confirm", "review":
		cfg.Execution.Mode = internal.ExecutionMode(value)
		return nil
	}
	return fmt.Errorf("execution.mode must be yolo, solo, confirm, or review")
}

// setCompressionStrategy validates and applies a context_compression.strategy
// value. Allowed values mirror agentic.CompressionStrategy.
// setSpinnerName validates and sets tui.spinner config value.
func setSpinnerName(cfg *config.Config, value string) error {
	if value == "" || value == "none" {
		cfg.TUI.Spinner = value
		return nil
	}
	if _, ok := spinner.Get(value); ok {
		cfg.TUI.Spinner = value
		return nil
	}
	return fmt.Errorf("unknown spinner: %q (choose from: none, %s)", value, strings.Join(spinner.Names(), ", "))
}

func setCompressionStrategy(cfg *config.Config, value string) error {
	switch strings.ToLower(value) {
	case "", "tool_elision", "selective", "summarize", "hybrid", "micro":
		cfg.ContextCompression.Strategy = strings.ToLower(value)
		return nil
	}
	return fmt.Errorf("context_compression.strategy must be one of: tool_elision, selective, summarize, hybrid, micro")
}

// setIntRange returns a setter that parses an int and enforces an inclusive
// [min,max] range. Used for fields like threshold_percent where out-of-range
// values would silently disable the feature.
func setIntRange(getter func(*config.Config) *int, min, max int) configSetter {
	return func(cfg *config.Config, value string) error {
		v, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		if v < min || v > max {
			return fmt.Errorf("value must be between %d and %d (got %d)", min, max, v)
		}
		*getter(cfg) = v
		return nil
	}
}

func setConfigField(cfg *config.Config, path []string, value string) error {
	key := strings.Join(path, ".")
	setter, ok := configSetters[key]
	if !ok {
		return fmt.Errorf("unknown config key: %s", key)
	}
	return setter(cfg, value)
}

func parseBool(value string) bool {
	switch strings.ToLower(value) {
	case "true", "on", "1", "yes":
		return true
	default:
		return false
	}
}

// boolPtrValue dereferences a tri-state *bool config field; nil means the
// feature is at its default (enabled, i.e. not disabled → false).
func boolPtrValue(v *bool) bool {
	return v != nil && *v
}

// parseToggle parses a UI-friendly on/off value.
// When inverted is true, "off" means the underlying boolean is true (used for
// thinking_collapsed, where off = collapse = true).
func parseToggle(value string, inverted bool) bool {
	v := parseBool(value)
	if isOnOff(value) {
		v = strings.ToLower(value) == "on"
	}
	if inverted {
		return !v
	}
	return v
}

func isOnOff(value string) bool {
	switch strings.ToLower(value) {
	case "on", "off":
		return true
	default:
		return false
	}
}

func handleConfigReload(ctx core.Context) error {
	if ctx.ConfigSaver == nil {
		writeStr(ctx, "Config saver not available. Cannot reload.\n")
		return nil
	}
	fresh, err := ctx.ConfigSaver.Reload()
	if err != nil {
		writeFmt(ctx, "Error reloading config: %v\n", err)
		return nil
	}
	*ctx.Config = *fresh
	writeStr(ctx, "Config reloaded from all cascade layers.\n")
	return nil
}
