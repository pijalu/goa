// SPDX-License-Identifier: GPL-3.0-or-later

package commands

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal"
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

// doAddModel adds or updates a model. Model identity is provider-scoped
// (Bug A, 2026-08-27): an explicit ID updates in place ONLY when the provider
// also matches (a genuine upsert). When the ID is already taken by a
// DIFFERENT provider, the add must not clobber that binding — a new entry is
// appended under a unique provider-qualified ID instead.
func doAddModel(cfg *config.Config, saver config.ConfigSaver, out core.OutputWriter, id, providerID, modelName string) error {
	for i := range cfg.Models {
		if cfg.Models[i].ID != id || cfg.Models[i].ProviderID != providerID {
			continue
		}
		cfg.Models[i].Model = modelName
		if cfg.Models[i].Name == "" {
			cfg.Models[i].Name = modelName
		}
		return saveAndReport(out, saver, cfg, "model", id)
	}
	modelID := uniqueModelID(cfg.Models, id, providerID)
	cfg.Models = append(cfg.Models, config.ModelConfig{
		ID:         modelID,
		Name:       modelName,
		ProviderID: providerID,
		Model:      modelName,
	})
	return saveAndReport(out, saver, cfg, "model", modelID)
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
	// Changing the active model may also change the provider: persist that
	// provider switch, and ALWAYS push the couple into the live agent — even
	// a same-provider switch must reach the session, or the next turn keeps
	// running on the previous model.
	if key == "active_model" {
		persistActiveModelProviderSwitch(ctx, prevProvider, value)
		propagateModelSwitch(ctx, ctx.Config)
	}
	writeFmt(ctx, "Set %s = %s\n", key, value)
	ctx.FooterRefresh()
	return nil
}

// persistActiveModelProviderSwitch persists the provider that followed a
// /config set active_model switch (no-op when the model stayed on its
// provider).
func persistActiveModelProviderSwitch(ctx core.Context, prevProvider, value string) {
	if ctx.Config.ActiveProvider == prevProvider || ctx.ConfigSaver == nil {
		return
	}
	if err := ctx.ConfigSaver.SaveHomeField([]string{"active_provider"}, ctx.Config.ActiveProvider); err != nil {
		writeFmt(ctx, "set active_model = %s (provider switch to %s not persisted: %v)\n", value, ctx.Config.ActiveProvider, err)
		return
	}
	writeFmt(ctx, "Set active_provider = %s\n", ctx.Config.ActiveProvider)
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

func syncRuntimeConfig(ctx core.Context, key, value string) error {
	// Loop-detector overrides sync straight to the detector; they do not
	// require a running agent, so handle them before the AgentManager guard.
	if syncLoopDetectorConfig(ctx, key) {
		return nil
	}
	// Goal-limit keys sync straight to the goal subsystem; they also do not
	// require a running agent.
	if key == "goals.default_turn_budget" || key == "goals.stall_turns" {
		syncGoalLimits(ctx)
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
	case "execution.auto_heal_tool_calls":
		// Tool-call fixing is snapshotted into the agent at session start;
		// push the fresh value so an ongoing session uses it immediately
		// (bugs-20260826-config-tool-live-sync).
		ctx.AgentManager.RefreshAutoHeal()
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
