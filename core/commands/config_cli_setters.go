// SPDX-License-Identifier: GPL-3.0-or-later

package commands

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/spinner"
)

// configSetter updates a single config field from a string value.
type configSetter func(cfg *config.Config, value string) error

var configSetters = map[string]configSetter{
	"mode.default.major":                             setActiveMajor,
	"active_provider":                                setString(func(cfg *config.Config) *string { return &cfg.ActiveProvider }),
	"active_model":                                   setActiveModel,
	"multi_agent.companion_model":                    setStringWithValidate(func(cfg *config.Config) *string { return &cfg.MultiAgent.CompanionModel }, validateActiveModel),
	"execution.mode":                                 setExecutionMode,
	"execution.retries":                              setInt(func(cfg *config.Config) *int { return &cfg.Execution.Retries }),
	"execution.auto_save_model":                      setBoolPtr(func(cfg *config.Config) **bool { return &cfg.Execution.AutoSaveModel }),
	"execution.auto_heal_tool_calls":                 setBool(func(cfg *config.Config) *bool { return &cfg.Execution.AutoHealToolCalls }),
	"mode.plan_file_path":                            setString(func(cfg *config.Config) *string { return &cfg.Mode.PlanFilePath }),
	"execution.max_tool_calls":                       setInt(func(cfg *config.Config) *int { return &cfg.Execution.MaxToolCalls }),
	"execution.max_tool_repeat_total":                setInt(func(cfg *config.Config) *int { return &cfg.Execution.MaxToolRepeatTotal }),
	"execution.max_tool_repeat_consecutive":          setInt(func(cfg *config.Config) *int { return &cfg.Execution.MaxToolRepeatConsecutive }),
	"execution.max_tool_error_streak":                setInt(func(cfg *config.Config) *int { return &cfg.Execution.MaxToolErrorStreak }),
	"execution.tool_call_limit_reset_window":         setInt(func(cfg *config.Config) *int { return &cfg.Execution.ToolCallLimitResetWindow }),
	"execution.max_stream_rounds":                    setInt(func(cfg *config.Config) *int { return &cfg.Execution.MaxStreamRounds }),
	"goals.default_turn_budget":                      setInt(func(cfg *config.Config) *int { return &cfg.Goals.DefaultTurnBudget }),
	"goals.stall_turns":                              setInt(func(cfg *config.Config) *int { return &cfg.Goals.StallTurns }),
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
	// Fresh-window (token-budget) strategy knobs (Codex Phase 2b.3): the
	// enabled gate and the preservation tail bound, settable like every
	// other compression knob (no hidden configuration keys).
	"context_compression.fresh_window.enabled":               setBoolPtr(func(cfg *config.Config) **bool { return &cfg.ContextCompression.FreshWindow.Enabled }),
	"context_compression.fresh_window.preserve_recent_turns": setIntRange(func(cfg *config.Config) *int { return &cfg.ContextCompression.FreshWindow.PreserveRecentTurns }, 0, 100),
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
	"execution.runaway_loop_max_repeats":         setInt(func(cfg *config.Config) *int { return &cfg.Execution.RunawayLoopMaxRepeats }),
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
	case "", "tool_elision", "selective", "summarize", "hybrid", "micro", "fresh_window":
		cfg.ContextCompression.Strategy = strings.ToLower(value)
		return nil
	}
	return fmt.Errorf("context_compression.strategy must be one of: tool_elision, selective, summarize, hybrid, micro, fresh_window")
}

// setOnErrorStrategy validates and sets the on-error recovery strategy
// (context_compression.on_error_strategy). Empty resets to the default
// (hybrid).
func setOnErrorStrategy(cfg *config.Config, value string) error {
	switch strings.ToLower(value) {
	case "", "tool_elision", "selective", "summarize", "hybrid", "micro", "fresh_window":
		cfg.ContextCompression.OnErrorStrategy = strings.ToLower(value)
		return nil
	}
	return fmt.Errorf("context_compression.on_error_strategy must be one of: tool_elision, selective, summarize, hybrid, micro, fresh_window")
}

// setLayerStrategy validates and sets one per-layer compression strategy
// (strategies.soft|trigger|hard). Any strategy is allowed on any layer.
func setLayerStrategy(field func(cfg *config.Config) *string) func(cfg *config.Config, value string) error {
	return func(cfg *config.Config, value string) error {
		v := strings.ToLower(value)
		switch v {
		case "", "tool_elision", "selective", "summarize", "hybrid", "micro", "fresh_window":
			*field(cfg) = v
			return nil
		}
		return fmt.Errorf("strategy must be one of: tool_elision, selective, summarize, hybrid, micro, fresh_window")
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
	case "enabled":
		return setPerModelEnabled(ov, value)
	case "strategy":
		v := strings.ToLower(value)
		switch v {
		case "", "tool_elision", "selective", "summarize", "hybrid", "micro", "fresh_window":
			ov.Strategy = v
			return nil
		}
		return fmt.Errorf("per-model strategy must be one of: tool_elision, selective, summarize, hybrid, micro, fresh_window")
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

// setPerModelEnabled validates and writes the per-model enable tri-state
// (bugs.md 2026-08-26): empty clears to inherit; true/false materialize an
// explicit per-model enable/disable.
func setPerModelEnabled(ov *config.ModelCompressionOverride, value string) error {
	switch strings.ToLower(value) {
	case "":
		ov.Enabled = nil
		return nil
	case "true", "on", "1", "yes":
		ov.Enabled = boolPtr(true)
		return nil
	case "false", "off", "0", "no":
		ov.Enabled = boolPtr(false)
		return nil
	}
	return fmt.Errorf("per-model enabled must be \"true\" or \"false\" (empty = inherit)")
}

// setPerModelLayerStrategy validates a per-layer strategy override; the soft
// layer must stay zero-LLM (micro, tool_elision or fresh_window), mirroring
// the global rule.
func setPerModelLayerStrategy(ov *config.ModelCompressionOverride, field, value string) error {
	v := strings.ToLower(value)
	switch v {
	case "", "tool_elision", "micro", "fresh_window":
		// valid for every layer
	case "selective", "summarize", "hybrid":
		if field == "strategies.soft" {
			return fmt.Errorf("soft layer strategy must be zero-LLM (micro, tool_elision or fresh_window)")
		}
	default:
		return fmt.Errorf("strategy must be one of: tool_elision, selective, summarize, hybrid, micro, fresh_window")
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
	if providerID, field, ok := parseProviderRetryKey(path); ok {
		return setProviderRetryField(cfg, providerID, field, value)
	}
	return fmt.Errorf("unknown config key: %s", key)
}

// parsePerModelCompressionKey splits a context_compression.per_model.<modelID>.<field>
// path into its model ID and field. ok is false for any other key shape.
func parseProviderRetryKey(path []string) (providerID, field string, ok bool) {
	if len(path) < 3 || path[0] != "providers" || path[1] == "" {
		return "", "", false
	}
	field = strings.Join(path[2:], ".")
	switch field {
	case "idle_timeout", "max_retry_delay", "retry_policy.max_retries", "retry_policy.backoff.max_ms":
		return path[1], field, true
	default:
		return "", "", false
	}
}

func setProviderRetryField(cfg *config.Config, providerID, field, value string) error {
	for i := range cfg.Providers {
		if cfg.Providers[i].ID == providerID {
			return setProviderRetryValue(&cfg.Providers[i], field, value)
		}
	}
	return fmt.Errorf("unknown provider or retry key: providers.%s.%s", providerID, field)
}

func setProviderRetryValue(p *config.ProviderConfig, field, value string) error {
	switch field {
	case "idle_timeout":
		if _, err := time.ParseDuration(value); err != nil {
			return fmt.Errorf("idle_timeout must be a duration: %w", err)
		}
		p.IdleTimeout = value
	case "max_retry_delay":
		if _, err := time.ParseDuration(value); err != nil {
			return fmt.Errorf("max_retry_delay must be a duration: %w", err)
		}
		p.MaxRetryDelay = value
	case "retry_policy.max_retries":
		n, err := parseNonNegativeInt(value, "max_retries")
		if err != nil {
			return err
		}
		ensureRetryPolicy(p)
		p.RetryPolicy.MaxRetries = n
	case "retry_policy.backoff.max_ms":
		n, err := parseNonNegativeInt(value, "max_ms")
		if err != nil {
			return err
		}
		ensureRetryPolicy(p)
		p.RetryPolicy.Backoff.MaxMS = n
	default:
		return fmt.Errorf("unknown retry key: %s", field)
	}
	return nil
}

func parseNonNegativeInt(value, name string) (int, error) {
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", name)
	}
	return n, nil
}

func ensureRetryPolicy(p *config.ProviderConfig) {
	if p.RetryPolicy == nil {
		p.RetryPolicy = &config.RetryPolicyConfig{}
	}
}

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
