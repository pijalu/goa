// SPDX-License-Identifier: GPL-3.0-or-later
package core

import (
	"time"

	configpkg "github.com/pijalu/goa/config"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/multiagent"
)

func (am *AgentManager) ResetConversationID() string {
	am.mu.Lock()
	defer am.mu.Unlock()
	if am.activeAgent == nil || am.sessionStore == nil {
		return ""
	}
	id := am.sessionStore.StartSession()
	opts := am.activeAgent.StreamOptions()
	opts.SessionID = id
	am.activeAgent.SetStreamOptions(opts)
	return id
}
func (am *AgentManager) ActiveModel() agenticprovider.Model {
	am.mu.Lock()
	defer am.mu.Unlock()
	if am.activeAgent == nil {
		return agenticprovider.Model{}
	}
	return am.activeAgent.Model()
}
func (am *AgentManager) TurnHistory() []TurnRecord { return am.turnRecorder.TurnHistory() }
func (am *AgentManager) LastTurn() *TurnRecord     { return am.turnRecorder.LastTurn() }
func (am *AgentManager) CurrentTurn() *TurnRecord  { return am.turnRecorder.CurrentTurn() }

// TurnRecorder exposes the session turn recorder for identity-tagged
// sub-agent turn ingestion (companion / workflow stage cache stats feeding
// the per-agent /stats:cache sections).
func (am *AgentManager) TurnRecorder() *TurnRecorder { return am.turnRecorder }
func (am *AgentManager) EmitEvent(text string)     { am.emitFlash(text) }
func (am *AgentManager) SetForegroundOrchestrator(orch *multiagent.ForegroundOrchestrator) {
	am.mu.Lock()
	am.foregroundOrch = orch
	am.mu.Unlock()
	am.companion.SetForegroundOrchestrator(orch)
}
func (am *AgentManager) SetCompanionTimeout(d time.Duration) { am.companion.SetMessageTimeout(d) }
func (am *AgentManager) SetStateStore(ss *StateStore) {
	am.mu.Lock()
	am.stateStore = ss
	am.mu.Unlock()
	am.modeMgr.SetStateStore(ss)
}
func (am *AgentManager) SetConfigSaver(saver configpkg.ConfigSaver) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.configSaver = saver
}
func (am *AgentManager) foregroundOrchestrator() *multiagent.ForegroundOrchestrator {
	am.mu.Lock()
	defer am.mu.Unlock()
	return am.foregroundOrch
}
func (am *AgentManager) stateStoreRef() *StateStore {
	am.mu.Lock()
	defer am.mu.Unlock()
	return am.stateStore
}
func (am *AgentManager) SetInputHistory([]string) error { return nil }
func (am *AgentManager) GetInputHistory() []string      { return nil }
func (am *AgentManager) SessionID() string {
	am.mu.Lock()
	defer am.mu.Unlock()
	if am.sessionStore == nil {
		return ""
	}
	return am.sessionStore.SessionID()
}
