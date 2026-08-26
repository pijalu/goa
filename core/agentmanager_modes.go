// SPDX-License-Identifier: GPL-3.0-or-later
package core

import (
	"fmt"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"

	"github.com/pijalu/goa/multiagent"
)

func (am *AgentManager) SetPendingInputHistory(h []string) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.pendingInputHistory = h
}
func (am *AgentManager) GetAndClearPendingInputHistory() []string {
	am.mu.Lock()
	defer am.mu.Unlock()
	h := am.pendingInputHistory
	am.pendingInputHistory = nil
	return h
}
func (am *AgentManager) SetAgentDrivenEnabled(enabled bool) error {
	if err := am.modeMgr.SetAgentDrivenEnabled(enabled); err != nil {
		am.emitFlash("Failed to load agent-driven prompt: " + err.Error())
	}
	if err := am.persistState(); err != nil {
		return fmt.Errorf("failed to save agent-driven state: %w", err)
	}
	return nil
}
func (am *AgentManager) AgentDrivenEnabled() bool  { return am.modeMgr.AgentDrivenEnabled() }
func (am *AgentManager) AgentDrivenPrompt() string { return am.modeMgr.AgentDrivenPrompt() }
func (am *AgentManager) SetAgentDrivenChangeCallback(cb func(bool)) {
	am.agentDriven.SetChangeCallback(cb)
}
func (am *AgentManager) CurrentMode() internal.ModeState { return am.modeMgr.CurrentMode() }
func (am *AgentManager) SetMode(ms internal.ModeState) internal.ModeState {
	old := am.CurrentMode()
	if info := am.modeMgr.SetMode(ms); info != nil {
		am.emitModeChange(info.OldMode, info.NewMode, info.Source)
		am.emitModeChangeFlash(info.OldMode, info.NewMode)
		am.dispatchLifecycle("mode_enter", map[string]any{"old_mode": string(old.Major), "new_mode": string(info.NewMode.Major), "autonomy": string(info.NewMode.Autonomy), "source": info.Source})
		am.queueMajorModePrompt(ms.Major)
	}
	if err := am.persistState(); err != nil {
		am.emitFlash("Failed to save mode state: " + err.Error())
	}
	return ms
}
func (am *AgentManager) PushMode(ms internal.ModeState, source string) internal.ModeState {
	old := am.CurrentMode()
	info := am.modeMgr.PushMode(ms, source)
	if info != nil {
		am.emitModeChange(info.OldMode, info.NewMode, info.Source)
		am.emitModeChangeFlash(info.OldMode, info.NewMode)
		am.dispatchLifecycle("mode_enter", map[string]any{"old_mode": string(old.Major), "new_mode": string(info.NewMode.Major), "autonomy": string(info.NewMode.Autonomy), "source": info.Source})
		am.queueMajorModePrompt(info.NewMode.Major)
	}
	if err := am.persistState(); err != nil {
		am.emitFlash("Failed to save mode state: " + err.Error())
	}
	if info == nil {
		return ms
	}
	return info.OldMode
}
func (am *AgentManager) PopMode() internal.ModeState {
	old := am.CurrentMode()
	info := am.modeMgr.PopMode()
	if info != nil {
		am.emitModeChange(info.OldMode, info.NewMode, info.Source)
		am.emitModeChangeFlash(info.OldMode, info.NewMode)
		am.dispatchLifecycle("mode_enter", map[string]any{"old_mode": string(old.Major), "new_mode": string(info.NewMode.Major), "autonomy": string(info.NewMode.Autonomy), "source": info.Source})
		am.queueMajorModePrompt(info.NewMode.Major)
	}
	if err := am.persistState(); err != nil {
		am.emitFlash("Failed to save mode state: " + err.Error())
	}
	if info == nil {
		return internal.ModeState{}
	}
	return info.NewMode
}
func (am *AgentManager) PreviousMode() *internal.ModeState { return am.modeMgr.PreviousMode() }
func (am *AgentManager) Source() string                    { return am.modeMgr.Source() }
func (am *AgentManager) SetCompanionAgent(a *agentic.Agent) {
	am.companion.SetCompanionAgent(a, am.agentBus)
}
func (am *AgentManager) SetMinorMode(mode string, enabled bool) error {
	orch := am.foregroundOrchestrator()
	if orch == nil {
		return fmt.Errorf("no orchestrator available")
	}
	switch mode {
	case "companion":
		if enabled {
			orch.SetMode(multiagent.WorkflowAgentDriven)
		} else {
			orch.SetMode(multiagent.WorkflowInactive)
		}
		if err := am.modeMgr.SetAgentDrivenEnabled(enabled); err != nil {
			return fmt.Errorf("failed to sync agent-driven state: %w", err)
		}
	default:
		return fmt.Errorf("unknown minor mode: %q", mode)
	}
	activeMode := ""
	if enabled {
		activeMode = mode
	}
	am.modeMgr.SetCurrentMinorMode(activeMode)
	if err := am.persistState(); err != nil {
		return fmt.Errorf("failed to save minor mode: %w", err)
	}
	am.emitMinorMode(activeMode)
	return nil
}
func (am *AgentManager) MinorMode() string { return am.modeMgr.CurrentMinorMode() }
func (am *AgentManager) SetThinkingLevel(level string) error {
	if err := am.applySessionThinkingLevel(level); err != nil {
		return err
	}
	return am.saveModelThinkingLevel(level)
}
func (am *AgentManager) RestoreThinkingLevel(level string) error {
	return am.applySessionThinkingLevel(level)
}
func (am *AgentManager) applySessionThinkingLevel(level string) error {
	am.modeMgr.SetThinkingLevel(level)
	am.queueThinkingLevel(level)
	if err := am.persistState(); err != nil {
		return fmt.Errorf("failed to save thinking level: %w", err)
	}
	am.emitThinkingLevel(level)
	return nil
}
func (am *AgentManager) saveModelThinkingLevel(level string) error {
	am.mu.Lock()
	cfg := am.cfg
	saver := am.configSaver
	suppressed := am.modelPersistenceSuppressed
	am.mu.Unlock()
	if suppressed {
		// A team (session-level or goal overlay) currently governs the session
		// model: persisting now would write the TEAM's model as the user's
		// saved choice (RC-5 — observed: project config ended up with the
		// companion's model). The session-level change still applies.
		return nil
	}
	if cfg == nil || cfg.ActiveModel == "" {
		return nil
	}
	mdl := cfg.GetModelByID(cfg.ActiveModel)
	if mdl == nil {
		return nil
	}
	mdl.ThinkingLevel = level
	if saver == nil {
		return nil
	}
	if err := saver.SaveHomeProvidersAndModels(cfg); err != nil {
		return fmt.Errorf("failed to save model thinking level: %w", err)
	}
	if cfg.Execution.AutoSaveModelEnabled() {
		if err := saver.SaveProjectProvidersAndModels(cfg); err != nil {
			return fmt.Errorf("failed to save model thinking level to project: %w", err)
		}
	}
	return nil
}
func (am *AgentManager) queueThinkingLevel(level string) {
	am.mu.Lock()
	defer am.mu.Unlock()
	if am.activeAgent == nil {
		return
	}
	am.pendingThinkingLevel = &level
}
func (am *AgentManager) applyPendingThinkingLevel() {
	am.mu.Lock()
	pending := am.pendingThinkingLevel
	am.pendingThinkingLevel = nil
	am.mu.Unlock()
	if pending != nil && am.activeAgent != nil {
		am.activeAgent.SetReasoningEffort(agentic.ReasoningEffort(*pending))
	}
}
func (am *AgentManager) GetThinkingLevel() string { return am.modeMgr.GetThinkingLevel() }

// SetModelPersistenceSuppressed gates saving active_provider/active_model
// (and per-model thinking levels) to config files. The team manager enables
// it for the duration of a team/overlay so the team's model is never
// persisted as the user's saved choice (RC-5).
func (am *AgentManager) SetModelPersistenceSuppressed(suppressed bool) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.modelPersistenceSuppressed = suppressed
}

// ModelPersistenceSuppressed reports whether model persistence is currently
// gated (team governing the session model).
func (am *AgentManager) ModelPersistenceSuppressed() bool {
	am.mu.Lock()
	defer am.mu.Unlock()
	return am.modelPersistenceSuppressed
}
