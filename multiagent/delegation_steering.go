// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package multiagent

import (
	"sync"

	"github.com/pijalu/goa/internal/agentic"
)

// delegation_steering.go gives delegated sub-agent runs a mid-turn steering
// path (T5): while a delegation is running, user input typed on its tab must
// reach THAT run, not vanish into the main agent's queue. The mechanism
// mirrors the main agent's SteeringSource wiring: DelegateTool binds one
// DelegationSteeringQueue to the delegated agent for the duration of the run
// (the agent drains it between stream rounds via SetSteeringSource), and the
// app enqueues through SteerDelegation.

// DelegationSteeringQueue buffers mid-turn steering messages for ONE
// delegated run. It satisfies agentic.SteeringSource (Drain) so it can be
// attached directly to an agentic.Agent; Append is safe from any goroutine
// (the TUI command loop) because Drain runs on the delegated agent's own
// goroutine.
type DelegationSteeringQueue struct {
	mu      sync.Mutex
	pending []string
}

// Append queues one steering message.
func (q *DelegationSteeringQueue) Append(text string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.pending = append(q.pending, text)
}

// Len reports the number of pending messages.
func (q *DelegationSteeringQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending)
}

// Drain atomically returns and clears all pending messages. Implements
// agentic.SteeringSource.
func (q *DelegationSteeringQueue) Drain() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.pending) == 0 {
		return nil
	}
	out := q.pending
	q.pending = nil
	return out
}

// BindDelegationSteering creates (or replaces) the steering queue for
// delegationID and returns it. The caller attaches it to the delegated agent;
// UnbindDelegationSteering removes it when the run ends.
func (o *ForegroundOrchestrator) BindDelegationSteering(delegationID string) *DelegationSteeringQueue {
	if delegationID == "" {
		return nil
	}
	q := &DelegationSteeringQueue{}
	o.delegationSteering.Store(delegationID, q)
	return q
}

// UnbindDelegationSteering drops the steering queue for delegationID. Late
// steering for a finished delegation then reports false instead of buffering
// text nobody will drain.
func (o *ForegroundOrchestrator) UnbindDelegationSteering(delegationID string) {
	if delegationID == "" {
		return
	}
	o.delegationSteering.Delete(delegationID)
}

// SteerDelegation appends text to the named delegation's steering queue,
// reporting whether the delegation is currently bound (running). Safe from
// any goroutine.
func (o *ForegroundOrchestrator) SteerDelegation(delegationID, text string) bool {
	if o == nil || delegationID == "" || text == "" {
		return false
	}
	v, ok := o.delegationSteering.Load(delegationID)
	if !ok {
		return false
	}
	q, ok := v.(*DelegationSteeringQueue)
	if !ok {
		return false
	}
	q.Append(text)
	return true
}

// compile-time interface check: the queue is a valid agentic.SteeringSource.
var _ agentic.SteeringSource = (*DelegationSteeringQueue)(nil)
