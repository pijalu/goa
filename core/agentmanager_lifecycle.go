// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
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

func (am *AgentManager) SetLifecycleRegistry(r LifecycleRegistry) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.lifecycleRegistry = r
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
		DisableToolBudget:        am.disableToolBudget || cfg.Execution.DisableToolBudget,
		ToolCallLimitResetWindow: cfg.Execution.ToolCallLimitResetWindow,
		AutoHealToolCalls:        cfg.Execution.AutoHealToolCalls,
		ReasoningEffort:          agentic.ReasoningEffort(cfg.GetReasoningEffort()),
		ToolResultAsUser:         cfg.GetToolResultAsUser(),
		SkillExecutionMode:       agentic.SkillExecutionMode(cfg.Skills.ExecutionMode),
		GoalStateProvider:        am.goalStateProvider,
		ProjectDir:               am.projectDir,
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
	}
	compressionCfg := am.buildCompressionConfig(cfg, mdl.ContextWindow)
	if cfg.ContextCompression.Enabled || compressionCfg.MaxTokens > 0 {
		agenticCfg.ContextCompression = compressionCfg
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

func (am *AgentManager) buildCompressionConfig(cfg *config.Config, modelContextWindow int) agentic.ContextCompressionConfig {
	// We intentionally do NOT fall back to modelContextWindow here. When the
	// user has not configured a compression limit, leaving MaxTokens at 0 lets
	// the agent use the runtime model window (which may be refreshed later,
	// e.g., for local models whose loaded context is smaller than the default).
	// Auto-deriving a hard MaxTokens from the initial model window would make
	// the value stale and hide the real capacity in the UI.
	return agentic.ContextCompressionConfig{
		MaxTokens:           cfg.ContextCompression.MaxTokens,
		ThresholdPercent:    compressionThreshold(cfg),
		OnContextError:      cfg.ContextCompression.OnContextError,
		Strategy:            compressionStrategy(cfg),
		PreserveRecentTurns: cfg.ContextCompression.PreserveRecentTurns,
		MicroCompaction:     buildMicroCompactionConfig(cfg.ContextCompression.MicroCompaction),
	}
}

func compressionThreshold(cfg *config.Config) int {
	if cfg.ContextCompression.ThresholdPercent > 0 {
		return cfg.ContextCompression.ThresholdPercent
	}
	if cfg.Execution.TokenCritical > 0 {
		return cfg.Execution.TokenCritical
	}
	return 90
}

func compressionStrategy(cfg *config.Config) agentic.CompressionStrategy {
	if s := agentic.CompressionStrategy(cfg.ContextCompression.Strategy); s != "" {
		return s
	}
	return agentic.CompressionToolElision
}

func buildMicroCompactionConfig(m config.MicroCompactionSettings) agentic.MicroCompactionConfig {
	microCfg := agentic.DefaultMicroCompactionConfig
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
