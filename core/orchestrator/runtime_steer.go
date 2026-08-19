// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

func (r *Runtime) SteerAgent(agentID, text string) bool {
	for _, h := range r.pool.Live() {
		if h.ID == agentID {
			h.Steer(text)
			r.emit(Event{Type: EventAgentSteered, AgentID: agentID, Role: h.Role,
				Payload: map[string]any{"from": "user", "text": text}})
			return true
		}
	}
	return false
}

// SteerAll broadcasts a steering message to every live handle (including the
// orchestrator role if present). If the orchestrator loop is paused waiting
// for a user answer, this also resumes the loop so the steering is processed
// immediately. Used by the Summary tab.
func (r *Runtime) SteerAll(text string) {
	for _, h := range r.pool.Live() {
		h.Steer(text)
		r.emit(Event{Type: EventAgentSteered, AgentID: h.ID, Role: h.Role,
			Payload: map[string]any{"from": "broadcast", "text": text}})
	}
	// Resume a paused orchestrator loop so the broadcast is not left dangling.
	r.loopMu.Lock()
	if r.loopActive && r.pendingUser && r.resumeCh != nil {
		r.pendingUser = false
		close(r.resumeCh)
		r.resumeCh = nil
	}
	r.loopMu.Unlock()
}

// SteerOrchestrator targets the orchestrator-role handle only.
func (r *Runtime) SteerOrchestrator(text string) bool {
	// Buffer the steering for the orchestrator so it survives across loop
	// iterations even when no orchestrator handle is currently live.
	r.orchSteerMu.Lock()
	r.orchSteer = append(r.orchSteer, text)
	r.orchSteerMu.Unlock()

	consumed := false
	for _, h := range r.pool.Live() {
		if h.Role == "orchestrator" {
			h.Steer(text)
			r.emit(Event{Type: EventAgentSteered, AgentID: h.ID, Role: h.Role,
				Payload: map[string]any{"from": "user", "text": text}})
			consumed = true
			break
		}
	}

	r.loopMu.Lock()
	if r.loopActive {
		consumed = true
		if r.pendingUser && r.resumeCh != nil {
			r.pendingUser = false
			close(r.resumeCh)
			r.resumeCh = nil
		}
	}
	r.loopMu.Unlock()

	return consumed
}
