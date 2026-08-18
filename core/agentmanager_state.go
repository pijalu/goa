// SPDX-License-Identifier: GPL-3.0-or-later
package core

import (
	"fmt"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/event"
)

func (am *AgentManager) persistState() error {
	ss := am.stateStoreRef()
	if ss == nil {
		return nil
	}
	snap := SessionStateSnapshot{ModeState: am.modeMgr.CurrentMode(), MinorMode: am.modeMgr.CurrentMinorMode(), AgentDrivenEnabled: am.modeMgr.AgentDrivenEnabled(), ThinkingLevel: am.modeMgr.GetThinkingLevel()}
	if hist := MarshalCompanionHistory(am.companion.Agent()); len(hist) > 0 {
		snap.CompanionHistory = hist
	}
	return ss.Save(snap)
}
func (am *AgentManager) PersistState() error { return am.persistState() }
func (am *AgentManager) LogCompanionStarted(agent *agentic.Agent) {
	if agent == nil {
		return
	}
	mdl := agent.Model()
	if am.logger != nil {
		am.logger.Log(agentic.Info, "companion started: model=%s provider=%s", mdl.ID, string(mdl.Provider))
	}
	am.dispatchLifecycle("companion_started", map[string]any{"model": mdl.ID, "provider": string(mdl.Provider)})
	if am.sessionStore != nil {
		am.sessionStore.WriteEvent(agentic.OutputEvent{Type: agentic.EventProgress, Metadata: map[string]string{"event": "companion_started", "model": mdl.ID, "provider": string(mdl.Provider)}})
	}
}
func (am *AgentManager) emitInternalEvent(ev agentic.OutputEvent) {
	if am.forwardInternalEvents {
		am.events <- ev
	}
}
func (am *AgentManager) emitAgentEvent(ev agentic.OutputEvent) {
	if am.eventsOut == nil || am.eventFwd == nil {
		return
	}
	am.eventFwd.push(event.AgentEvent{Event: ev})
}
func (am *AgentManager) emitFlash(text string) {
	if am.eventsOut == nil {
		return
	}
	select {
	case am.eventsOut.Chat <- event.ChatEvent{Flash: &event.Flash{Text: text}}:
	default:
		{
		}
	}
}
func (am *AgentManager) emitModeChange(old, new internal.ModeState, source string) {
	if am.eventsOut == nil {
		return
	}
	select {
	case am.eventsOut.Footer <- event.FooterEvent{ModeChange: &event.ModeChange{OldMode: old, NewMode: new, Source: source}}:
	default:
		{
		}
	}
}
func (am *AgentManager) emitModeChangeFlash(old, new internal.ModeState) {
	if am.eventsOut == nil {
		return
	}
	am.mu.Lock()
	active := am.activeAgent != nil
	am.mu.Unlock()
	if !active {
		return
	}
	var text string
	switch {
	case old.Major != new.Major:
		text = fmt.Sprintf("Mode: %s", new.Major)
	case old.Autonomy != new.Autonomy:
		text = fmt.Sprintf("Autonomy: %s", new.Autonomy)
	default:
		return
	}
	select {
	case am.eventsOut.Chat <- event.ChatEvent{Flash: &event.Flash{Text: text}}:
	default:
		{
		}
	}
}
