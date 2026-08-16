// SPDX-License-Identifier: GPL-3.0-or-later

package commands

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

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
	// Apply the change to a deep copy first: per-key setters only check their
	// own range, but cross-field invariants (e.g. compression thresholds
	// soft ≤ trigger ≤ hard) are enforced by Config.Validate. Without this
	// gate /config could apply and persist a configuration that fails
	// validation on the next start.
	candidate := ctx.Config.DeepCopy()
	if err := setConfigField(candidate, path, value); err != nil {
		writeFmt(ctx, "Invalid value for %s: %v\n", key, err)
		return nil
	}
	// execution.mode needs the new-mode-system default updated before we sync
	// the runtime agent mode, otherwise the agent manager keeps the old autonomy.
	if key == "execution.mode" {
		updateModeDefault(candidate, value)
	}
	if err := candidate.Validate(); err != nil {
		writeFmt(ctx, "Refusing to set %s = %s: the resulting configuration is invalid (not applied, not saved):\n%v\n", key, value, err)
		// /config:set is an internal command: its router output is not echoed
		// to the chat viewport (echoCommandResult drops internal output), so
		// the rejection must also go through the flash channel — otherwise it
		// would be silent in the TUI.
		flash := err.Error()
		var ve *internal.ValidationError
		if errors.As(err, &ve) {
			flash = strings.Join(ve.ErrList, "; ")
		}
		ctx.Flash(fmt.Sprintf("Rejected %s = %s: %s (not applied, not saved)", key, value, flash))
		return nil
	}
	// Commit the validated change to the live config. The setters are
	// deterministic functions of (cfg, value), so this cannot fail after the
	// candidate applied cleanly.
	_ = setConfigField(ctx.Config, path, value)
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

// syncLoopDetectorConfig applies loop-detector config keys straight to the
// live detector; they do not require a running agent. It reports whether key
// was a loop-detector key (handled, even when the detector is absent).
func syncLoopDetectorConfig(ctx core.Context, key string) bool {
	exec := ctx.Config.Execution
	switch key {
	case "execution.disable_thinking_loop_detection":
		if ctx.LoopDetector != nil {
			ctx.LoopDetector.SetPersistOverride("think", boolPtrValue(exec.DisableThinkingLoopDetection))
		}
	case "execution.disable_tool_loop_detection":
		if ctx.LoopDetector != nil {
			ctx.LoopDetector.SetPersistOverride("tool", boolPtrValue(exec.DisableToolLoopDetection))
		}
	case "execution.disable_stream_loop_detection":
		if ctx.LoopDetector != nil {
			ctx.LoopDetector.SetPersistOverride("stream", boolPtrValue(exec.DisableStreamLoopDetection))
		}
	case "execution.disable_thinking_stall_detection":
		if ctx.LoopDetector != nil {
			ctx.LoopDetector.SetPersistOverride("stall", boolPtrValue(exec.DisableThinkingStallDetection))
		}
	case "execution.loop_warning", "execution.loop_interrupt":
		if ctx.LoopDetector != nil {
			ctx.LoopDetector.SetLoopThresholds(exec.LoopWarning, exec.LoopInterrupt)
		}
	case "execution.stream_loop_max_repeats", "execution.stream_loop_min_period":
		syncStreamLoopThresholds(ctx.LoopDetector, exec)
	default:
		return false
	}
	return true
}

// syncStreamLoopThresholds pushes the stream-loop numeric knobs to the live
// detector (nil-safe; invalid values restore detector defaults).
func syncStreamLoopThresholds(ld *core.LoopDetector, exec config.ExecutionConfig) {
	if ld == nil {
		return
	}
	ld.SetStreamMaxRepeats(exec.StreamLoopMaxRepeats)
	ld.SetStreamMinPeriod(exec.StreamLoopMinPeriod)
}

func syncRuntimeConfig(ctx core.Context, key, value string) error {
	// Loop-detector overrides sync straight to the detector; they do not
	// require a running agent, so handle them before the AgentManager guard.
	if syncLoopDetectorConfig(ctx, key) {
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
	// Per-model compression overrides: an empty value means "clear this field so
	// the model inherits the global section". Persist that as a key removal
	// rather than writing an empty scalar, so the override entry stays clean
	// (and disappears entirely once its last field is cleared).
	if _, _, isPerModel := parsePerModelCompressionKey(path); isPerModel && value == "" {
		if err := ctx.ConfigSaver.DeleteHomeField(savePath); err != nil {
			return fmt.Errorf("cleared %s in memory, but failed to persist the clear: %v", key, err)
		}
		return nil
	}
	if err := ctx.ConfigSaver.SaveHomeField(savePath, scalarValue(value)); err != nil {
		return fmt.Errorf("set %s = %s (in memory, but failed to persist: %v)", key, value, err)
	}
	// Setting the tiered trigger_percent clears the deprecated legacy
	// threshold_percent alias (see setTriggerPercentClearLegacy); remove the
	// legacy key from the home config too so it cannot re-shadow the tiered
	// value after a reload (Issue 2).
	if key == "context_compression.thresholds.trigger_percent" {
		if err := ctx.ConfigSaver.DeleteHomeField([]string{"context_compression", "threshold_percent"}); err != nil {
			return fmt.Errorf("set %s = %s (in memory, but failed to clear legacy threshold_percent: %v)", key, value, err)
		}
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
//	/config:temp:think_loop_detection:off   — disable thinking-loop detection
//	/config:temp:think_loop_detection:on    — enable thinking-loop detection
//	/config:temp:tool_loop_detection:off    — disable tool-call loop detection
//	/config:temp:tool_loop_detection:on     — enable tool-call loop detection
//	/config:temp:stream_loop_detection:off  — disable stream-text loop detection
//	/config:temp:stream_loop_detection:on   — enable stream-text loop detection
//	/config:temp:thinking_stall_detection:off — disable the thinking-stall watchdog
//	/config:temp:thinking_stall_detection:on  — enable the thinking-stall watchdog
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
	case "stream_loop_detection":
		return applyTempOverride(ctx, "stream", enabled)
	case "thinking_stall_detection":
		return applyTempOverride(ctx, "stall", enabled)
	default:
		return fmt.Errorf("unknown temp setting: %q (use 'think_loop_detection', 'tool_loop_detection', 'stream_loop_detection' or 'thinking_stall_detection')", setting)
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
	if kind == "stream" {
		label = "stream-text loop detection"
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
	if kind == "stream" {
		return "stream"
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
	"execution.auto_save_model":                      setBool(func(cfg *config.Config) *bool { return &cfg.Execution.AutoSaveModel }),
	"mode.plan_file_path":                            setString(func(cfg *config.Config) *string { return &cfg.Mode.PlanFilePath }),
	"execution.max_tool_calls":                       setInt(func(cfg *config.Config) *int { return &cfg.Execution.MaxToolCalls }),
	"execution.max_tool_repeat_total":                setInt(func(cfg *config.Config) *int { return &cfg.Execution.MaxToolRepeatTotal }),
	"execution.max_tool_repeat_consecutive":          setInt(func(cfg *config.Config) *int { return &cfg.Execution.MaxToolRepeatConsecutive }),
	"execution.max_tool_repeat":                      setInt(func(cfg *config.Config) *int { return &cfg.Execution.MaxToolRepeatTotal }),
	"execution.max_consecutive_tool_rounds":          setInt(func(cfg *config.Config) *int { return &cfg.Execution.MaxConsecutiveToolRounds }),
	"tui.theme":                                      setString(func(cfg *config.Config) *string { return &cfg.TUI.Theme }),
	"tui.spinner":                                    setSpinnerName,
	"tui.spinner_location":                           setSpinnerLocation,
	"tui.transparency.show_thinking":                 setBool(func(cfg *config.Config) *bool { return &cfg.TUI.Transparency.ShowThinking }),
	"tui.transparency.thinking_collapsed":            setThinkingCollapsed,
	"logging.level":                                  setString(func(cfg *config.Config) *string { return &cfg.Logging.Level }),
	"logging.file":                                   setString(func(cfg *config.Config) *string { return &cfg.Logging.File }),
	"thinking_level":                                 setThinkingLevel,
	"multi_agent.enabled":                            setBool(func(cfg *config.Config) *bool { return &cfg.MultiAgent.Enabled }),
	"multi_agent.companion_provider":                 setString(func(cfg *config.Config) *string { return &cfg.MultiAgent.CompanionProvider }),
	"context_compression.enabled":                    setBoolPtr(func(cfg *config.Config) **bool { return &cfg.ContextCompression.Enabled }),
	"context_compression.strategy":                   setCompressionStrategy,
	"context_compression.threshold_percent":          setIntRange(func(cfg *config.Config) *int { return &cfg.ContextCompression.ThresholdPercent }, 0, 100),
	"context_compression.thresholds.soft_percent":    setIntRange(func(cfg *config.Config) *int { return &cfg.ContextCompression.Thresholds.SoftPercent }, 0, 100),
	"context_compression.thresholds.trigger_percent": setTriggerPercentClearLegacy,
	"context_compression.thresholds.hard_percent":    setIntRange(func(cfg *config.Config) *int { return &cfg.ContextCompression.Thresholds.HardPercent }, 0, 100),
	"context_compression.strategies.soft":            setLayerStrategy(func(cfg *config.Config) *string { return &cfg.ContextCompression.Strategies.Soft }),
	"context_compression.strategies.trigger":         setLayerStrategy(func(cfg *config.Config) *string { return &cfg.ContextCompression.Strategies.Trigger }),
	"context_compression.strategies.hard":            setLayerStrategy(func(cfg *config.Config) *string { return &cfg.ContextCompression.Strategies.Hard }),
	"context_compression.cache_gate":                 setCacheGate,
	"context_compression.max_tokens":                 setInt(func(cfg *config.Config) *int { return &cfg.ContextCompression.MaxTokens }),
	"context_compression.on_context_error":           setBool(func(cfg *config.Config) *bool { return &cfg.ContextCompression.OnContextError }),
	"context_compression.on_error_strategy":          setOnErrorStrategy,
	"context_compression.preserve_recent_turns":      setIntRange(func(cfg *config.Config) *int { return &cfg.ContextCompression.PreserveRecentTurns }, 0, 100),
	// Micro compaction knobs: no hidden configuration keys — the micro own
	// gates (usage ratio, cold-cache threshold) change runtime behavior and
	// must be visible/settable like every other compression knob.
	"context_compression.micro_compaction.enabled":              setBoolPtr(func(cfg *config.Config) **bool { return &cfg.ContextCompression.MicroCompaction.Enabled }),
	"context_compression.micro_compaction.keep_recent_messages": setIntRange(func(cfg *config.Config) *int { return &cfg.ContextCompression.MicroCompaction.KeepRecentMessages }, 0, 1000),
	"context_compression.micro_compaction.min_content_tokens":   setIntRange(func(cfg *config.Config) *int { return &cfg.ContextCompression.MicroCompaction.MinContentTokens }, 0, 1000000),
	"context_compression.micro_compaction.min_context_ratio":    setMicroMinContextRatio,
	"context_compression.micro_compaction.cache_miss_threshold": setMicroCacheMissThreshold,
	"context_compression.micro_compaction.truncated_marker":     setString(func(cfg *config.Config) *string { return &cfg.ContextCompression.MicroCompaction.TruncatedMarker }),
	// Per-turn temporal context injection (gap CX6): no hidden knobs — the
	// enable switch, display zone, and refresh interval are settable like
	// every other runtime behavior.
	"time_context.enabled":                       setBool(func(cfg *config.Config) *bool { return &cfg.TimeContext.Enabled }),
	"time_context.time_zone":                     setString(func(cfg *config.Config) *string { return &cfg.TimeContext.TimeZone }),
	"time_context.refresh_interval":              setTimeContextRefreshInterval,
	"execution.loop_warning":                     setInt(func(cfg *config.Config) *int { return &cfg.Execution.LoopWarning }),
	"execution.loop_interrupt":                   setInt(func(cfg *config.Config) *int { return &cfg.Execution.LoopInterrupt }),
	"execution.disable_thinking_loop_detection":  setBoolPtr(func(cfg *config.Config) **bool { return &cfg.Execution.DisableThinkingLoopDetection }),
	"execution.disable_tool_loop_detection":      setBoolPtr(func(cfg *config.Config) **bool { return &cfg.Execution.DisableToolLoopDetection }),
	"execution.disable_stream_loop_detection":    setBoolPtr(func(cfg *config.Config) **bool { return &cfg.Execution.DisableStreamLoopDetection }),
	"execution.disable_thinking_stall_detection": setBoolPtr(func(cfg *config.Config) **bool { return &cfg.Execution.DisableThinkingStallDetection }),
	"execution.stream_loop_max_repeats":          setInt(func(cfg *config.Config) *int { return &cfg.Execution.StreamLoopMaxRepeats }),
	"execution.stream_loop_min_period":           setInt(func(cfg *config.Config) *int { return &cfg.Execution.StreamLoopMinPeriod }),
	"execution.stream_loop_max_strikes":          setInt(func(cfg *config.Config) *int { return &cfg.Execution.StreamLoopMaxStrikes }),
	"execution.stream_loop_reset_after":          setInt(func(cfg *config.Config) *int { return &cfg.Execution.StreamLoopResetAfter }),
	"execution.disable_tool_budget":              setBool(func(cfg *config.Config) *bool { return &cfg.Execution.DisableToolBudget }),
	"skills.execution_mode":                      setString(func(cfg *config.Config) *string { return &cfg.Skills.ExecutionMode }),
	"tools.bash.enable_complexity_analysis":      setBool(func(cfg *config.Config) *bool { return &cfg.Tools.Bash.EnableComplexityAnalysis }),
	"tools.bash.warn_file_edits":                 setBoolPtr(func(cfg *config.Config) **bool { return &cfg.Tools.Bash.WarnFileEdits }),
	"tools.bash.jail":                            setBool(func(cfg *config.Config) *bool { return &cfg.Tools.Bash.Jail }),
	"tools.bash.max_complexity_score":            setInt(func(cfg *config.Config) *int { return &cfg.Tools.Bash.MaxComplexityScore }),
	"tools.terminal.sandbox.enabled":             setBool(func(cfg *config.Config) *bool { return &cfg.Tools.Terminal.Sandbox.Enabled }),
	"tools.enabled.goal":                         setBool(func(cfg *config.Config) *bool { return &cfg.Tools.Enabled.Goal }),
	"tools.enabled.lsp":                          setBool(func(cfg *config.Config) *bool { return &cfg.Tools.Enabled.LSP }),
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
	// Selector sentinel values ("__delete__X", "__add__", …) must never be
	// persisted as a model ID (active model ended up named
	// "__delete__deepseek-v4-flash").
	if strings.HasPrefix(value, "__") {
		return fmt.Errorf("invalid model value: %q is a selector action value, not a model ID", value)
	}
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

// setSpinnerLocation validates and sets tui.spinner_location (chat|statusbar).
func setSpinnerLocation(cfg *config.Config, value string) error {
	switch value {
	case "", "chat", "statusbar":
		cfg.TUI.SpinnerLocation = value
		return nil
	}
	return fmt.Errorf("tui.spinner_location must be chat or statusbar (got %q)", value)
}

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

// setOnErrorStrategy validates and sets the on-error recovery strategy
// (context_compression.on_error_strategy). Empty resets to the default
// (hybrid).
func setOnErrorStrategy(cfg *config.Config, value string) error {
	switch strings.ToLower(value) {
	case "", "tool_elision", "selective", "summarize", "hybrid", "micro":
		cfg.ContextCompression.OnErrorStrategy = strings.ToLower(value)
		return nil
	}
	return fmt.Errorf("context_compression.on_error_strategy must be one of: tool_elision, selective, summarize, hybrid, micro")
}

// setLayerStrategy validates and sets one per-layer compression strategy
// (strategies.soft|trigger|hard). Any strategy is allowed on any layer.
func setLayerStrategy(field func(cfg *config.Config) *string) func(cfg *config.Config, value string) error {
	return func(cfg *config.Config, value string) error {
		v := strings.ToLower(value)
		switch v {
		case "", "tool_elision", "selective", "summarize", "hybrid", "micro":
			*field(cfg) = v
			return nil
		}
		return fmt.Errorf("strategy must be one of: tool_elision, selective, summarize, hybrid, micro")
	}
}

// setCacheGate validates and sets the prefix-cache gate toggle ("on"|"off").
func setCacheGate(cfg *config.Config, value string) error {
	switch strings.ToLower(value) {
	case "", "on", "off":
		cfg.ContextCompression.CacheGate = strings.ToLower(value)
		return nil
	}
	return fmt.Errorf("context_compression.cache_gate must be \"on\" or \"off\"")
}

// setPerModelCompressionField sets one compression override field for a single
// model (context_compression.per_model.<modelID>.<field>), creating the
// PerModel map / entry on demand. An empty value clears the field back to
// "inherit from the global section". The validation rules mirror the global
// setters (same allowed strategies, threshold ranges, on/off cache gate), so a
// per-model override can never be set to a value the global field would reject.
func setPerModelCompressionField(cfg *config.Config, modelID, field, value string) error {
	pm := cfg.ContextCompression.PerModel
	if pm == nil {
		pm = map[string]config.ModelCompressionOverride{}
		cfg.ContextCompression.PerModel = pm
	}
	ov := pm[modelID]
	if err := applyPerModelField(&ov, field, value); err != nil {
		return err
	}
	pm[modelID] = ov
	return nil
}

// applyPerModelField validates and writes one per-model override field.
func applyPerModelField(ov *config.ModelCompressionOverride, field, value string) error {
	switch field {
	case "strategy":
		v := strings.ToLower(value)
		switch v {
		case "", "tool_elision", "selective", "summarize", "hybrid", "micro":
			ov.Strategy = v
			return nil
		}
		return fmt.Errorf("per-model strategy must be one of: tool_elision, selective, summarize, hybrid, micro")
	case "strategies.soft", "strategies.trigger", "strategies.hard":
		return setPerModelLayerStrategy(ov, field, value)
	case "thresholds.soft_percent":
		return setPerModelLevel(&ov.Thresholds.SoftPercent, field, value, true)
	case "thresholds.trigger_percent":
		return setPerModelLevel(&ov.Thresholds.TriggerPercent, field, value, false)
	case "thresholds.hard_percent":
		return setPerModelLevel(&ov.Thresholds.HardPercent, field, value, false)
	case "max_tokens":
		return setPerModelIntRange(&ov.MaxTokens, field, value, 0, 1<<30)
	case "cache_gate":
		v := strings.ToLower(value)
		switch v {
		case "", "on", "off":
			ov.CacheGate = v
			return nil
		}
		return fmt.Errorf("per-model cache_gate must be \"on\" or \"off\"")
	case "preserve_recent_turns":
		return setPerModelIntRange(&ov.PreserveRecentTurns, field, value, 0, 100)
	}
	return fmt.Errorf("unknown per-model compression field: %s", field)
}

// setPerModelLayerStrategy validates a per-layer strategy override; the soft
// layer must stay zero-LLM (micro or tool_elision), mirroring the global rule.
func setPerModelLayerStrategy(ov *config.ModelCompressionOverride, field, value string) error {
	v := strings.ToLower(value)
	switch v {
	case "", "tool_elision", "micro":
		// valid for every layer
	case "selective", "summarize", "hybrid":
		if field == "strategies.soft" {
			return fmt.Errorf("soft layer strategy must be zero-LLM (micro or tool_elision)")
		}
	default:
		return fmt.Errorf("strategy must be one of: tool_elision, selective, summarize, hybrid, micro")
	}
	switch field {
	case "strategies.soft":
		ov.Strategies.Soft = v
	case "strategies.trigger":
		ov.Strategies.Trigger = v
	case "strategies.hard":
		ov.Strategies.Hard = v
	}
	return nil
}

// setPerModelIntRange parses an int override; empty clears it to 0 (inherit).
func setPerModelIntRange(dst *int, field, value string, min, max int) error {
	if value == "" {
		*dst = 0
		return nil
	}
	v, err := strconv.Atoi(value)
	if err != nil {
		return err
	}
	if v < min || v > max {
		return fmt.Errorf("%s must be between %d and %d (got %d)", field, min, max, v)
	}
	*dst = v
	return nil
}

// setPerModelLevel parses a threshold-level override, mirroring the global
// validation (validateCompressionLevel): empty/0 = inherit, -1 = disable (soft
// layer only), otherwise 10-95 in 5% increments.
func setPerModelLevel(dst *int, field, value string, allowDisable bool) error {
	if value == "" {
		*dst = 0
		return nil
	}
	v, err := strconv.Atoi(value)
	if err != nil {
		return err
	}
	if v == 0 || (allowDisable && v == -1) {
		*dst = v
		return nil
	}
	if v < 10 || v > 95 || v%5 != 0 {
		return fmt.Errorf("%s must be 10-95 in 5%% increments (got %d)", field, v)
	}
	*dst = v
	return nil
}

// setMicroMinContextRatio validates the micro-compaction usage gate: 0 (or
// empty) clears to the SDK default (0.5); anything else must be a ratio in
// (0, 1].
func setMicroMinContextRatio(cfg *config.Config, value string) error {
	if value == "" || value == "0" {
		cfg.ContextCompression.MicroCompaction.MinContextRatio = 0
		return nil
	}
	f, err := strconv.ParseFloat(value, 64)
	if err != nil || f <= 0 || f > 1 {
		return fmt.Errorf("context_compression.micro_compaction.min_context_ratio must be 0 (default) or a ratio in (0, 1], got %q", value)
	}
	cfg.ContextCompression.MicroCompaction.MinContextRatio = f
	return nil
}

// setMicroCacheMissThreshold validates the micro-compaction cold-cache gate
// as a Go duration ("15m", "1h"); empty clears to the SDK default (1h) and
// "0" disables the cache protection (cache always presumed cold).
func setMicroCacheMissThreshold(cfg *config.Config, value string) error {
	if value == "" {
		cfg.ContextCompression.MicroCompaction.CacheMissThreshold = ""
		return nil
	}
	if _, err := time.ParseDuration(value); err != nil {
		return fmt.Errorf("context_compression.micro_compaction.cache_miss_threshold must be a duration (e.g. 15m, 1h), got %q", value)
	}
	cfg.ContextCompression.MicroCompaction.CacheMissThreshold = value
	return nil
}

// setTimeContextRefreshInterval validates the time_context refresh interval
// as a Go duration ("60s", "5m", "0"). Empty or "0" means inject at every
// eligible step entry (no suppression).
func setTimeContextRefreshInterval(cfg *config.Config, value string) error {
	if value == "" || value == "0" {
		cfg.TimeContext.RefreshInterval = ""
		return nil
	}
	if _, err := time.ParseDuration(value); err != nil {
		return fmt.Errorf("time_context.refresh_interval must be a duration (e.g. 60s, 5m) or 0, got %q", value)
	}
	cfg.TimeContext.RefreshInterval = value
	return nil
}

// setTriggerPercentClearLegacy sets the tiered trigger_percent AND clears the
// deprecated legacy ThresholdPercent alias. The legacy alias otherwise wins over
// Thresholds.TriggerPercent both in the menu display (compressionTriggerValue)
// and at runtime (resolveAgenticThresholds), so a config file still carrying
// `threshold_percent:` would permanently shadow any edit to the tiered field
// (Issue 2 — trigger threshold changes not reflected). Clearing it on an
// explicit edit makes the new value take effect while leaving untouched legacy
// configs (which never run this setter) at their documented behavior.
func setTriggerPercentClearLegacy(cfg *config.Config, value string) error {
	v, err := strconv.Atoi(value)
	if err != nil {
		return err
	}
	if v < 0 || v > 100 {
		return fmt.Errorf("value must be between 0 and 100 (got %d)", v)
	}
	cfg.ContextCompression.Thresholds.TriggerPercent = v
	cfg.ContextCompression.ThresholdPercent = 0
	return nil
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
	if setter, ok := configSetters[key]; ok {
		return setter(cfg, value)
	}
	// Dynamic per-model compression override keys:
	// context_compression.per_model.<modelID>.<field> — the model ID is embedded
	// in the path, so these cannot be listed in the static configSetters map.
	if modelID, field, ok := parsePerModelCompressionKey(path); ok {
		return setPerModelCompressionField(cfg, modelID, field, value)
	}
	return fmt.Errorf("unknown config key: %s", key)
}

// parsePerModelCompressionKey splits a context_compression.per_model.<modelID>.<field>
// path into its model ID and field. ok is false for any other key shape.
func parsePerModelCompressionKey(path []string) (modelID, field string, ok bool) {
	// path = [context_compression, per_model, <modelID>, <field...>]
	if len(path) < 4 || path[0] != "context_compression" || path[1] != "per_model" {
		return "", "", false
	}
	modelID = path[2]
	if modelID == "" {
		return "", "", false
	}
	field = strings.Join(path[3:], ".")
	return modelID, field, field != ""
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
