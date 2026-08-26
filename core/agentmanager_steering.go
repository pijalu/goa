// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"strings"
)

// DispatchPendingSteering dispatches steering left over from a turn that did
// not pass through runAgentTurn — externally driven goal continuation turns
// (agentManagerRunner). Two buffers must drain: pendingSteering, where the
// finalizeTurn observer (EventEnd fires for goal turns too) stashes leftover
// steering, and the live steering queue, which catches anything appended
// after EventEnd. Without this dispatch the stashed text sits in
// pendingSteering until some unrelated future user turn (or forever) and the
// pending bubble never clears. Emits SteeringInjected so the UI clears the
// bubble and renders the consumed text. No-op when both buffers are empty.
func (am *AgentManager) DispatchPendingSteering() {
	am.mu.Lock()
	stashed := am.pendingSteering
	am.pendingSteering = ""
	am.mu.Unlock()

	pending := am.steering.Flush()
	if stashed != "" {
		pending = append([]string{stashed}, pending...)
	}
	if len(pending) == 0 {
		return
	}
	text := strings.Join(pending, "\n\n")
	am.emitSteeringInjected(text)
	_ = am.SendUserInput(text)
}

// SteeringQueue returns the session's steering queue. The TUI uses it to
// append user input while the agent is running.
func (am *AgentManager) SteeringQueue() *SteeringQueue {
	am.mu.Lock()
	defer am.mu.Unlock()
	return am.steering
}

// SetSteeringQueue replaces the steering queue. Used by tests and by wiring
// code that wants a shared queue instance.
func (am *AgentManager) SetSteeringQueue(sq *SteeringQueue) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.steering = sq
}
