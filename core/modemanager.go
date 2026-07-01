// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/prompts"
)

// ModeChangeInfo describes a mode change produced by ModeManager.
type ModeChangeInfo struct {
	OldMode internal.ModeState
	NewMode internal.ModeState
	Source  string
}

// ModeManager owns the live mode state, agent-driven gate, thinking level, and
// minor-mode label. Persistence is coordinated by AgentManager.
type ModeManager struct {
	mu            sync.RWMutex
	sessionState  *SessionState
	stateStore    *StateStore
	thinkingLevel string
	agentDriven   *AgentDrivenGate
	currentMinor  string
}

// NewModeManager creates a mode manager from the given session state.
func NewModeManager(sessionState *SessionState, agentDriven *AgentDrivenGate) *ModeManager {
	return &ModeManager{
		sessionState: sessionState,
		agentDriven:  agentDriven,
	}
}

// SetStateStore sets the state store used for persistence.
func (mm *ModeManager) SetStateStore(ss *StateStore) {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	mm.stateStore = ss
}

// CurrentMode returns the current mode state.
func (mm *ModeManager) CurrentMode() internal.ModeState {
	mm.mu.RLock()
	defer mm.mu.RUnlock()
	if mm.sessionState == nil {
		return internal.ModeState{}
	}
	return mm.sessionState.Current()
}

// SetMode replaces the current mode and returns the change info.
func (mm *ModeManager) SetMode(ms internal.ModeState) *ModeChangeInfo {
	mm.mu.Lock()
	if mm.sessionState == nil {
		mm.mu.Unlock()
		return nil
	}
	old := mm.sessionState.Current()
	mm.sessionState.SetMode(ms)
	mm.mu.Unlock()
	return &ModeChangeInfo{OldMode: old, NewMode: ms, Source: "user"}
}

// PushMode saves the current mode and activates a new one.
func (mm *ModeManager) PushMode(ms internal.ModeState, source string) *ModeChangeInfo {
	mm.mu.Lock()
	if mm.sessionState == nil {
		mm.mu.Unlock()
		return nil
	}
	old := mm.sessionState.Current()
	mm.sessionState.PushMode(ms, source)
	mm.mu.Unlock()
	return &ModeChangeInfo{OldMode: old, NewMode: ms, Source: source}
}

// PopMode restores the previous mode from the stack.
func (mm *ModeManager) PopMode() *ModeChangeInfo {
	mm.mu.Lock()
	if mm.sessionState == nil {
		mm.mu.Unlock()
		return nil
	}
	old := mm.sessionState.Current()
	restored := mm.sessionState.PopMode()
	mm.mu.Unlock()
	return &ModeChangeInfo{OldMode: old, NewMode: restored, Source: "user"}
}

// PreviousMode returns the mode before the last push, or nil.
func (mm *ModeManager) PreviousMode() *internal.ModeState {
	mm.mu.RLock()
	defer mm.mu.RUnlock()
	if mm.sessionState == nil {
		return nil
	}
	return mm.sessionState.PreviousMode()
}

// Source returns the source of the current pushed mode.
func (mm *ModeManager) Source() string {
	mm.mu.RLock()
	defer mm.mu.RUnlock()
	if mm.sessionState == nil {
		return ""
	}
	return mm.sessionState.Source()
}

// SetThinkingLevel sets the reasoning effort level.
func (mm *ModeManager) SetThinkingLevel(level string) {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	mm.thinkingLevel = level
}

// GetThinkingLevel returns the current thinking level.
func (mm *ModeManager) GetThinkingLevel() string {
	mm.mu.RLock()
	defer mm.mu.RUnlock()
	return mm.thinkingLevel
}

// SetAgentDrivenEnabled updates the agent-driven gate and loads/clears the prompt.
func (mm *ModeManager) SetAgentDrivenEnabled(enabled bool) error {
	mm.agentDriven.SetEnabled(enabled)

	if enabled {
		if mm.agentDriven.Prompt() == "" {
			p, err := prompts.LoadAgentDrivenPrompt()
			if err != nil {
				return fmt.Errorf("failed to load agent-driven prompt: %w", err)
			}
			mm.agentDriven.SetPrompt(p)
		}
	} else {
		mm.agentDriven.SetPrompt("")
	}
	return nil
}

// AgentDrivenEnabled reports whether agent-driven tools are active.
func (mm *ModeManager) AgentDrivenEnabled() bool {
	return mm.agentDriven.Enabled()
}

// AgentDrivenPrompt returns the agent-driven system-prompt addition.
func (mm *ModeManager) AgentDrivenPrompt() string {
	return mm.agentDriven.Prompt()
}

// SetCurrentMinorMode records the active minor mode label.
func (mm *ModeManager) SetCurrentMinorMode(mode string) {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	mm.currentMinor = mode
}

// CurrentMinorMode returns the active minor mode label, or "" if none.
func (mm *ModeManager) CurrentMinorMode() string {
	mm.mu.RLock()
	defer mm.mu.RUnlock()
	return mm.currentMinor
}

// PersistState saves the current mode state, minor mode, and agent-driven flag.
func (mm *ModeManager) PersistState() error {
	mm.mu.RLock()
	ss := mm.stateStore
	ad := mm.agentDriven.Enabled()
	tl := mm.thinkingLevel
	minor := mm.currentMinor
	mm.mu.RUnlock()

	if ss == nil {
		return nil
	}
	snap := SessionStateSnapshot{
		ModeState:          mm.CurrentMode(),
		MinorMode:          minor,
		AgentDrivenEnabled: ad,
		ThinkingLevel:      tl,
	}
	return ss.Save(snap)
}

// RestoreFromSnapshot restores mode state from a persisted snapshot.
func (mm *ModeManager) RestoreFromSnapshot(snap SessionStateSnapshot) {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	if mm.sessionState != nil && snap.ModeState.Major != "" {
		mm.sessionState.SetMode(snap.ModeState)
	}
	mm.thinkingLevel = snap.ThinkingLevel
	mm.currentMinor = snap.MinorMode
	mm.agentDriven.SetEnabled(snap.AgentDrivenEnabled)
}

// MarshalCompanionHistory serializes the companion agent history for persistence.
func MarshalCompanionHistory(agent *agentic.Agent) []json.RawMessage {
	if agent == nil {
		return nil
	}
	var out []json.RawMessage
	for _, msg := range agent.GetHistory() {
		data, _ := json.Marshal(msg)
		if data != nil {
			out = append(out, data)
		}
	}
	return out
}
