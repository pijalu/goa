// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/hooks"
	"github.com/pijalu/goa/internal/perms"
)

// AgentBus returns the shared agent communication bus.
func (am *AgentManager) AgentBus() *agentic.AgentBus {
	return am.agentBus
}

// SetGoalStateProvider sets the provider used to inject goal context into the
// system prompt. Call before StartSession.
func (am *AgentManager) SetGoalStateProvider(p agentic.GoalStateProvider) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.goalStateProvider = p
}

// SetSpillPolicyFactory sets the factory used to build the per-session
// tool-result spill policy (gap CX2). The factory receives the session ID at
// StartSession so the implementation can scope its spill dir; a nil return
// disables spilling for that session. Call before StartSession.
func (am *AgentManager) SetSpillPolicyFactory(f func(sessionID string) agentic.SpillPolicy) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.spillPolicyFactory = f
}

// SetStickyProvider sets the always-on instruction source (sticky knowledge
// skills) wired into every session agent. Call before StartSession.
func (am *AgentManager) SetStickyProvider(p agentic.StickyProvider) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.stickyProvider = p
}

// SetPreTurnProvider sets the provider that delivers user-role content at the
// start of every turn ahead of the user message (e.g. due schedule reminders).
// Call before StartSession.
func (am *AgentManager) SetPreTurnProvider(p agentic.PreTurnProvider) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.preTurnProvider = p
}

// ActiveAgentStickyBlocks returns the sticky instruction blocks of the
// active session agent, or nil when no session/provider is set. Used for
// wiring verification and diagnostics.
func (am *AgentManager) ActiveAgentStickyBlocks() []string {
	am.mu.Lock()
	defer am.mu.Unlock()
	if am.activeAgent == nil {
		return nil
	}
	return am.activeAgent.StickyBlocks()
}

// LifecycleRegistry is the minimal interface AgentManager needs to dispatch
// plugin lifecycle events.
type LifecycleRegistry interface {
	Dispatch(hookType string, payload map[string]any)
}

// SetForwardInternalEvents enables or disables forwarding of agent events to
// the internal am.events channel returned by Events(). The TUI does not read
// this channel, so the default (false) avoids blocking the agent when the
// internal buffer fills. Headless mode and ACP consumers must call this with
// true before starting a session.
func (am *AgentManager) SetForwardInternalEvents(enabled bool) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.forwardInternalEvents = enabled
}

// SetLifecycleRegistry wires the plugin lifecycle registry. Passing nil disables
// lifecycle dispatch.
// SetConfirmTool sets the callback used to approve tool calls that require
// user confirmation in ask/confirm autonomy modes.
func (am *AgentManager) SetConfirmTool(fn func(ctx context.Context, toolName, input string) (bool, error)) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.confirmTool = fn
}

// PolicySource returns the safety-gating callbacks and scope the main agent
// runs under — autonomy level, guard rules, tool confirmation, and project
// dir — in a form the sub-agent pool can inherit (C2). Sub-agents must be
// subject to the same gating as the main agent; without this they would run
// tools unconfirmed even in ask/confirm autonomy modes. The closures read live
// state (current mode, mode registry) so sub-agent gating tracks mode changes
// just like the main agent's.
func (am *AgentManager) PolicySource() (getAutonomy func() internal.AutonomyLevel, getGuard func() perms.GuardConfig, confirm func(context.Context, string, string) (bool, error), projectDir string) {
	am.mu.Lock()
	confirmTool := am.confirmTool
	projectDir = am.projectDir
	am.mu.Unlock()

	getAutonomy = func() internal.AutonomyLevel { return am.CurrentMode().Autonomy }
	getGuard = func() perms.GuardConfig {
		if am.modeRegistry == nil {
			return perms.GuardConfig{}
		}
		spec, err := am.modeRegistry.Resolve(am.CurrentMode().Major)
		if err != nil {
			return perms.GuardConfig{}
		}
		return spec.Guard
	}
	return getAutonomy, getGuard, confirmTool, projectDir
}

func (am *AgentManager) SetLifecycleRegistry(r LifecycleRegistry) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.lifecycleRegistry = r
}

// SetHookEngine wires the user-defined lifecycle hook engine. When nil, no
// hooks run. The engine is passed to every agent created by this manager.
func (am *AgentManager) SetHookEngine(e hooks.AgentHookEngine) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.hookEngine = e
}

// SetContextWindowRefresher sets a callback that re-detects the active local
// model's loaded context window after the model has finished loading. The
// callback is invoked once on the first state-change event after a new session
// starts, so the context window used for compression and the footer reflects
// the real loaded length (e.g. LM Studio's loaded_context_length) instead of
// the static registry maximum.
func (am *AgentManager) SetContextWindowRefresher(fn func() int) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.contextWindowRefresher = fn
}

// SetGoalTokenRecorder registers a callback invoked for each EventTokenStats
// event with the cumulative token count for the current turn. Used by the
// goal system to track token budget consumption. Passing nil disables.
func (am *AgentManager) SetGoalTokenRecorder(fn func(totalTokens int)) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.goalTokenRecorder = fn
}

// SetActiveGoalIDProvider registers a callback returning the ID of the goal
// active at turn finalize time ("" = none). The main agent's TurnRecord is
// tagged with it so /stats:cache can group turns per goal. Passing nil
// disables tagging.
func (am *AgentManager) SetActiveGoalIDProvider(fn func() string) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.activeGoalID = fn
}

// currentGoalID resolves the active goal ID at finalize time ("" when no
// provider is wired or no goal is active).
func (am *AgentManager) currentGoalID() string {
	am.mu.Lock()
	fn := am.activeGoalID
	am.mu.Unlock()
	if fn == nil {
		return ""
	}
	return fn()
}

func (am *AgentManager) dispatchLifecycle(hookType string, payload map[string]any) {
	if am.lifecycleRegistry == nil {
		return
	}
	am.lifecycleRegistry.Dispatch(hookType, payload)
}

// SetPostTurnHook registers a callback invoked after each user turn completes.
// The callback runs in the same goroutine that ran the turn.
func (am *AgentManager) SetPostTurnHook(hook func()) {
	am.mu.Lock()
	defer am.mu.Unlock()
	if am.postTurnHook != nil {
		old := am.postTurnHook
		am.postTurnHook = func() {
			old()
			hook()
		}
		return
	}
	am.postTurnHook = hook
}

func (am *AgentManager) buildAgenticConfig(mdl agenticprovider.Model, opts agenticprovider.StreamOptions, systemPrompt string, tools []agentic.Tool, cfg *config.Config) agentic.Config {
	logger := am.logger
	if logger == nil {
		logger = agentic.NewLogger(agentic.Info)
	}
	agenticCfg := agentic.Config{
		Model:                    mdl,
		APIKey:                   opts.APIKey,
		StreamOptions:            opts,
		SystemPrompt:             systemPrompt,
		Tools:                    tools,
		Logger:                   logger,
		MaxToolRepeatTotal:       cfg.Execution.MaxToolRepeatTotal,
		MaxToolRepeatConsecutive: cfg.Execution.MaxToolRepeatConsecutive,
		MaxToolCalls:             cfg.Execution.MaxToolCalls,
		MaxStreamRounds:          cfg.Execution.MaxStreamRounds,
		MaxConsecutiveToolRounds: cfg.Execution.MaxConsecutiveToolRounds,
		DisableToolBudget:        am.disableToolBudget || cfg.Execution.DisableToolBudget,
		ToolCallLimitResetWindow: cfg.Execution.ToolCallLimitResetWindow,
		AutoHealToolCalls:        cfg.Execution.AutoHealToolCalls,
		ReasoningEffort:          agentic.ReasoningEffort(cfg.GetReasoningEffort()),
		ToolResultAsUser:         cfg.GetToolResultAsUser(),
		SkillExecutionMode:       agentic.SkillExecutionMode(cfg.Skills.ExecutionMode),
		GoalStateProvider:        am.goalStateProvider,
		StickyProvider:           am.stickyProvider,
		PreTurnProvider:          am.preTurnProvider,
		ProjectDir:               am.projectDir,
		SessionID:                opts.SessionID,
		GetAutonomy:              func() internal.AutonomyLevel { return am.CurrentMode().Autonomy },
		GetGuardConfig: func() perms.GuardConfig {
			if am.modeRegistry == nil {
				return perms.GuardConfig{}
			}
			spec, err := am.modeRegistry.Resolve(am.CurrentMode().Major)
			if err != nil {
				return perms.GuardConfig{}
			}
			return spec.Guard
		},
		ThinkingStallWarn: time.Duration(cfg.Execution.ThinkingStallWarnSeconds) * time.Second,
		ThinkingStallStop: time.Duration(cfg.Execution.ThinkingStallStopSeconds) * time.Second,
		ConfirmTool:       am.confirmTool,
		HookEngine:        am.hookEngine,
		// The streaming text loop detector lives in the agent, but its
		// enable/disable state and repeat threshold are shared with the other
		// loop detectors (temp + persist overrides) via the core loop detector.
		StreamLoopDisabled:   am.streamLoopDisabled,
		StreamLoopMaxRepeats: am.streamLoopMaxRepeats,
		StreamLoopMinPeriod:  am.streamLoopMinPeriod,
		StreamLoopMaxStrikes: cfg.Execution.StreamLoopMaxStrikes,
		StreamLoopResetAfter: cfg.Execution.StreamLoopResetAfter,
		// The thinking-stall watchdog shares the temp + persist override
		// machinery with the loop detectors, under its own "stall" kind.
		ThinkingStallDisabled: func() bool {
			if am.loopDetector == nil {
				return false
			}
			return am.loopDetector.Disabled("stall")
		},
	}
	compressionCfg := am.buildCompressionConfig(cfg, mdl.ID, mdl.ContextWindow)
	if cfg.ContextCompression.EnabledValue() || compressionCfg.MaxTokens > 0 {
		agenticCfg.ContextCompression = compressionCfg
	}
	// Remote-compaction opt-in gate (Codex Phase 2b): ANDed with the
	// provider/model capability inside the agent. Default off keeps the local
	// compression ladder unchanged; detection/gating only, no request logic.
	agenticCfg.RemoteCompactionEnabled = cfg.Features.RemoteCompactionEnabled()
	// Per-turn temporal context injection (gap CX6): off by default. The
	// refresh interval string is parsed at build time; a malformed value is
	// rejected by config validation, so an empty parse result here only
	// happens for an explicit "0" (inject every eligible step).
	agenticCfg.TimeContext = agentic.TimeContextConfig{
		Enabled:         cfg.TimeContext.Enabled,
		TimeZone:        cfg.TimeContext.TimeZone,
		RefreshInterval: timeContextRefreshInterval(cfg.TimeContext.RefreshInterval),
	}
	// Tool-result spill policy (gap CX2): built per session so the spill dir
	// is scoped to the session ID carried in the stream options.
	if am.spillPolicyFactory != nil {
		agenticCfg.SpillPolicy = am.spillPolicyFactory(opts.SessionID)
	}
	// Workspace-instruction lifecycle (gap CX5): seed the tracker with the
	// baseline context files already rendered into the system prompt, so
	// baseline scopes are treated as loaded and only lifecycle changes are
	// reported as durable user messages.
	if am.projectDir != "" {
		agenticCfg.InstructionTracker = internal.NewInstructionTracker(
			am.projectDir,
			internal.LoadProjectContextFiles(am.projectDir, cfg.ConfigDir),
		)
	}
	if level := am.modeMgr.GetThinkingLevel(); level != "" {
		agenticCfg.ReasoningEffort = agentic.ReasoningEffort(level)
	}
	return agenticCfg
}

// SetDisableToolBudget toggles the session-level tool-call budget check.
// When disabled, the agent allows unlimited tool calls per turn.
// This flag is NOT persisted — it resets on application restart.
// Call with false to re-enable the budget check using the configured limit.
func (am *AgentManager) SetDisableToolBudget(disabled bool) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.disableToolBudget = disabled
}

// streamLoopDisabled reports whether the streaming loop detector is off
// (nil-safe wrapper around the shared core loop detector).
func (am *AgentManager) streamLoopDisabled() bool {
	if am.loopDetector == nil {
		return false
	}
	return am.loopDetector.Disabled("stream")
}

// streamLoopMaxRepeats returns the live stream-loop repeat threshold
// (nil-safe; 0 lets the agent fall back to its default).
func (am *AgentManager) streamLoopMaxRepeats() int {
	if am.loopDetector == nil {
		return 0
	}
	return am.loopDetector.StreamMaxRepeats()
}

// streamLoopMinPeriod returns the live stream-loop minimum repeat-unit
// length (nil-safe; 0 lets the agent fall back to its default).
func (am *AgentManager) streamLoopMinPeriod() int {
	if am.loopDetector == nil {
		return 0
	}
	return am.loopDetector.StreamMinPeriod()
}

func (am *AgentManager) buildCompressionConfig(cfg *config.Config, modelID string, modelContextWindow int) agentic.ContextCompressionConfig {
	// We intentionally do NOT fall back to modelContextWindow here. When the
	// user has not configured a compression limit, leaving MaxTokens at 0 lets
	// the agent use the runtime model window (which may be refreshed later,
	// e.g., for local models whose loaded context is smaller than the default).
	// Auto-deriving a hard MaxTokens from the initial model window would make
	// the value stale and hide the real capacity in the UI.
	ov := overlayCompressionForModel(cfg.ContextCompression, modelID)

	thresholds := am.resolveAgenticThresholds(cfg, ov.thresholds, ov.legacyTrigger)
	// Honor the Enabled toggle: an explicit `enabled: false` disables every
	// proactive layer (all thresholds zeroed — with the opt-in semantics 0
	// disables each layer including the hard ceiling). The reactive safety
	// net (on_context_error / on_error_strategy) is unaffected.
	if !cfg.ContextCompression.EnabledValue() {
		thresholds = agentic.CompressionThresholds{}
	}

	// The legacy whole-config `strategy` was the main compression strategy; with
	// the trigger layer gone it maps onto the HARD layer (the ceiling at which
	// the main strategy fires) when no explicit strategies.hard is set.
	strategies := agenticLayerStrategies(ov.strategies)
	if strategies.Hard == "" && ov.strategy != "" {
		strategies.Hard = agentic.CompressionStrategy(ov.strategy)
	}

	return agentic.ContextCompressionConfig{
		MaxTokens:           ov.maxTokens,
		Thresholds:          thresholds,
		OnContextError:      cfg.ContextCompression.OnContextError,
		OnErrorStrategy:     agentic.CompressionStrategy(cfg.ContextCompression.OnErrorStrategy),
		Strategies:          strategies,
		DisableCacheGate:    ov.cacheGate == "off",
		PreserveRecentTurns: ov.preserveRecentTurns,
		MicroCompaction:     buildMicroCompactionConfig(cfg.ContextCompression.MicroCompaction),
		ToolResultPruning:   buildToolResultPruningConfig(cfg.ContextCompression.ToolResultPruning),
		FreshWindow:         buildFreshWindowConfig(cfg.ContextCompression.FreshWindow),
	}
}

// buildFreshWindowConfig maps the YAML fresh-window settings (Codex Phase
// 2b.3) to the SDK config: the enabled gate plus the preservation tail bound
// (0 = inherit the SDK inheritance chain, which bottoms out at
// PreserveRecentTurns → 2).
func buildFreshWindowConfig(s config.FreshWindowSettings) agentic.FreshWindowConfig {
	return agentic.FreshWindowConfig{
		Enabled:             s.Enabled != nil && *s.Enabled,
		PreserveRecentTurns: s.PreserveRecentTurns,
	}
}

// timeContextRefreshInterval parses the time_context refresh interval string
// into a duration. Empty or unparseable (validation already rejected the
// latter) means zero: inject at every eligible step entry.
func timeContextRefreshInterval(s string) time.Duration {
	if s == "" {
		return 0
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0
	}
	return d
}

// buildToolResultPruningConfig maps the YAML tool-result pruner settings to
// the SDK config. Enabled defaults OFF (nil = off): pre-compaction pruning
// only runs when explicitly enabled; the character budgets inherit the SDK
// defaults (threshold 8192, head 4096, tail 1024 code points) when zero.
func buildToolResultPruningConfig(s config.ToolResultPruningSettings) agentic.ToolResultPruningConfig {
	out := agentic.DefaultToolResultPruningConfig
	out.Enabled = s.PruningEnabled()
	if s.ThresholdChars > 0 {
		out.ThresholdChars = s.ThresholdChars
	}
	if s.HeadChars > 0 {
		out.HeadChars = s.HeadChars
	}
	if s.TailChars > 0 {
		out.TailChars = s.TailChars
	}
	return out
}

// agenticLayerStrategies maps the config layer strategies to the SDK type;
// empty fields stay empty so the SDK defaults (micro/elision-or-legacy/
// summarize) apply.
func agenticLayerStrategies(s config.CompressionLayerStrategiesConfig) agentic.CompressionLayerStrategies {
	return agentic.CompressionLayerStrategies{
		Soft: agentic.CompressionStrategy(s.Soft),
		Hard: agentic.CompressionStrategy(s.Hard),
	}
}

// compressionOverlay holds the effective compression settings after applying
// a per-model override on top of the global section.
type compressionOverlay struct {
	maxTokens           int
	strategy            string
	preserveRecentTurns int
	thresholds          config.CompressionThresholdsConfig
	strategies          config.CompressionLayerStrategiesConfig
	cacheGate           string
	legacyTrigger       int
}

// overlayCompressionForModel resolves the per-model overlay: start from the
// global section, then apply non-zero fields of the matching per_model entry
// (keyed by models[].id, which is what provider.Model.ID carries for the
// resolved active model).
func overlayCompressionForModel(cc config.ContextCompressionConfig, modelID string) compressionOverlay {
	ov := compressionOverlay{
		maxTokens:           cc.MaxTokens,
		strategy:            cc.Strategy,
		preserveRecentTurns: cc.PreserveRecentTurns,
		thresholds:          cc.Thresholds,
		strategies:          cc.Strategies,
		cacheGate:           cc.CacheGate,
		legacyTrigger:       cc.ThresholdPercent,
	}
	if modelID == "" {
		return ov
	}
	o, ok := cc.PerModel[modelID]
	if !ok {
		return ov
	}
	applyCompressionOverride(&ov, o)
	return ov
}

// resolveAgenticThresholds folds the config-layer thresholds with the legacy
// threshold_percent alias and the deprecated Execution.TokenCritical fallback,
// producing the SDK-level (soft/hard) thresholds. The SDK model is exactly
// soft / hard / on-error — there is no trigger layer — so every legacy
// "trigger" level (threshold_percent, thresholds.trigger_percent,
// TokenCritical) maps onto the HARD ceiling: it was the fill level at which
// the main strategy fired, and the hard ceiling is now that point. Precedence:
// explicit hard_percent → legacy threshold_percent → trigger_percent →
// TokenCritical (deprecated, logged once) → SDK default (0 = let the SDK
// default apply).
func (am *AgentManager) resolveAgenticThresholds(cfg *config.Config, t config.CompressionThresholdsConfig, legacyTrigger int) agentic.CompressionThresholds {
	out := agentic.CompressionThresholds{
		SoftPercent: t.SoftPercent,
		HardPercent: t.HardPercent,
	}
	if out.HardPercent == 0 && legacyTrigger > 0 {
		out.HardPercent = legacyTrigger
	}
	if out.HardPercent == 0 && t.TriggerPercent > 0 {
		out.HardPercent = t.TriggerPercent
	}
	if out.HardPercent == 0 && cfg.Execution.TokenCritical > 0 {
		am.logTokenCriticalDeprecationOnce()
		out.HardPercent = cfg.Execution.TokenCritical
	}
	return out
}

// logTokenCriticalDeprecationOnce warns (once per process) that the
// execution.token_critical fallback for the compression ceiling is deprecated.
var tokenCriticalDeprecationLogged atomic.Bool

func (am *AgentManager) logTokenCriticalDeprecationOnce() {
	if tokenCriticalDeprecationLogged.CompareAndSwap(false, true) && am.logger != nil {
		am.logger.Log(agentic.Warn, "execution.token_critical is deprecated as a compression ceiling fallback; use context_compression.thresholds.hard_percent instead")
	}
}

func buildMicroCompactionConfig(m config.MicroCompactionSettings) agentic.MicroCompactionConfig {
	microCfg := agentic.DefaultMicroCompactionConfig
	// Enabled is opt-in (DefaultMicroCompactionConfig leaves it false): only an
	// explicit `enabled: true` turns micro compaction on as the pre-summarize
	// validation step. Summarize stays the default compaction path otherwise.
	if m.Enabled != nil {
		microCfg.Enabled = *m.Enabled
	}
	if m.KeepRecentMessages > 0 {
		microCfg.KeepRecentMessages = m.KeepRecentMessages
	}
	if m.MinContentTokens > 0 {
		microCfg.MinContentTokens = m.MinContentTokens
	}
	if m.MinContextRatio > 0 {
		microCfg.MinContextRatio = m.MinContextRatio
	}
	if m.TruncatedMarker != "" {
		microCfg.TruncatedMarker = m.TruncatedMarker
	}
	if d := m.CacheMissThreshold; d != "" {
		if dur, err := time.ParseDuration(d); err == nil {
			microCfg.CacheMissThreshold = dur
		}
	}
	return microCfg
}
