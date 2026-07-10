// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/pijalu/goa/config"
)

// AgentFactory materializes a fresh AgentHandle for a (role, model) pair.
// The bounded pool calls it only after a slot has been reserved, so the
// factory must not itself block on caps. Returning an error rolls the
// reservation back.
//
// The factory is supplied by the orchestrator runtime (Phase 3) and bridges
// to multiagent.AgentPool.GetOrCreate under the hood. Keeping it as a
// callback lets the cap/block/release logic be unit-tested in isolation
// (Hard Rule #6: depend on abstractions).
//
// opts carries the caller's materialization preferences (e.g. requesting a
// brand-new agent instead of reusing a pooled one) so the factory can honor
// per-delegation intent without a side channel.
type AgentFactory func(role, model string, opts AcquireOptions) (*AgentHandle, error)

// AcquireOptions carries per-acquisition preferences. The zero value requests
// the default behavior: reuse an existing pooled agent for the role (so a
// specialist accumulates context across sequential delegations). Fresh forces
// a brand-new agent with no prior conversation.
type AcquireOptions struct {
	Fresh bool
}

// BoundedAgentPool wraps an AgentFactory with two independent caps:
//
//   - MaxTotalAgents bounds the number of concurrently-live handles across
//     all models. A value of 0 means unlimited.
//   - MaxAgentsPerModel bounds the concurrency for a single model id. A
//     missing entry or 0 means unlimited for that model.
//
// Acquire blocks (FIFO, context-cancellable) when either cap is saturated.
// Release frees the slot and wakes the head waiter.
type BoundedAgentPool struct {
	factory AgentFactory
	roles   map[string]config.OrchestratorRole
	totalCap int
	perModelCap map[string]int

	mu        sync.Mutex
	total     int
	perModel  map[string]int
	live      map[string]*AgentHandle
	nextID    int
	waiters   []chan struct{}
}

// NewBoundedAgentPool builds a pool from the orchestrator config and a
// factory. The role map is taken from cfg.Roles so Acquire can resolve
// role→model before billing against the per-model cap.
func NewBoundedAgentPool(cfg config.OrchestratorConfig, factory AgentFactory) *BoundedAgentPool {
	if factory == nil {
		factory = func(_, _ string, _ AcquireOptions) (*AgentHandle, error) {
			return nil, errors.New("orchestrator: no agent factory configured")
		}
	}
	roles := cfg.Roles
	if roles == nil {
		roles = map[string]config.OrchestratorRole{}
	}
	perModel := cfg.Pool.MaxAgentsPerModel
	if perModel == nil {
		perModel = map[string]int{}
	}
	return &BoundedAgentPool{
		factory:     factory,
		roles:       roles,
		totalCap:    cfg.Pool.MaxTotalAgents,
		perModelCap: perModel,
		perModel:    map[string]int{},
		live:        map[string]*AgentHandle{},
	}
}

// ErrUnknownRole is returned by Acquire when the role is not configured.
var ErrUnknownRole = errors.New("orchestrator: unknown role")

// Acquire reserves a slot for the given role and materializes a handle.
// It blocks while either cap is saturated, honoring ctx cancellation. The
// caller MUST call Release exactly once with the returned handle.
func (p *BoundedAgentPool) Acquire(ctx context.Context, role string, opts AcquireOptions) (*AgentHandle, error) {
	rcfg, ok := p.roles[role]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownRole, role)
	}
	model := rcfg.Model

	for {
		waiter, err := p.tryReserve(role, model)
		if err != nil {
			return nil, err
		}
		if waiter == nil {
			// Reserved successfully.
			break
		}
		// Blocked on a cap — wait for a release or cancellation.
		select {
		case <-waiter:
		case <-ctx.Done():
			p.cancelWaiter(role, model, waiter)
			return nil, ctx.Err()
		}
	}

	h, err := p.factory(role, model, opts)
	if err != nil {
		// Roll back the reservation and wake a waiter.
		p.mu.Lock()
		p.releaseLocked(role, model)
		p.mu.Unlock()
		return nil, err
	}
	p.mu.Lock()
	p.nextID++
	h.ID = fmt.Sprintf("%s-%d", role, p.nextID)
	p.live[h.ID] = h
	p.mu.Unlock()
	return h, nil
}

// tryReserve attempts to bill against both caps without blocking.
// Returns (nil waiter, err) on a hard failure (unknown role handled earlier),
// (non-nil waiter, nil) when blocked, or (nil waiter, nil) when reserved.
// On a blocked attempt the waiter is appended to the FIFO queue under the
// lock; the billing is NOT applied until a retry succeeds.
func (p *BoundedAgentPool) tryReserve(role, model string) (chan struct{}, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.underCapsLocked(model) {
		w := make(chan struct{})
		p.waiters = append(p.waiters, w)
		return w, nil
	}
	p.total++
	p.perModel[model]++
	return nil, nil
}

// cancelWaiter removes a blocked waiter from the queue. Counts were never
// incremented for it, so only the queue needs trimming.
func (p *BoundedAgentPool) cancelWaiter(role, model string, waiter chan struct{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, w := range p.waiters {
		if w == waiter {
			p.waiters = append(p.waiters[:i], p.waiters[i+1:]...)
			break
		}
	}
}

// underCapsLocked reports whether a new agent for model can be admitted now.
// 0 means unlimited for that axis.
func (p *BoundedAgentPool) underCapsLocked(model string) bool {
	if p.totalCap != 0 && p.total >= p.totalCap {
		return false
	}
	if cap := p.perModelCap[model]; cap != 0 && p.perModel[model] >= cap {
		return false
	}
	return true
}

// Release returns a handle's slot to the pool and wakes the head waiter.
// It is safe to call multiple times for the same handle (idempotent).
func (p *BoundedAgentPool) Release(h *AgentHandle) {
	if h == nil {
		return
	}
	h.markReleased()
	p.mu.Lock()
	_, stillLive := p.live[h.ID]
	if stillLive {
		delete(p.live, h.ID)
		p.releaseLocked(h.Role, h.Model)
	}
	p.mu.Unlock()
}

// releaseLocked decrements counts and signals the head waiter. It must be
// called with p.mu held. The signal is non-blocking so a waiter that has
// already cancelled never deadlocks the releaser.
func (p *BoundedAgentPool) releaseLocked(role, model string) {
	if p.total > 0 {
		p.total--
	}
	if p.perModel[model] > 0 {
		p.perModel[model]--
	}
	if len(p.waiters) > 0 {
		head := p.waiters[0]
		p.waiters = p.waiters[1:]
		select {
		case head <- struct{}{}:
		default:
			// Waiter already gone (cancelled) — drop silently; the next
			// release will wake the following waiter.
		}
	}
}

// Live returns a snapshot of currently-acquired handles. The slice is a
// copy; mutating it does not affect the pool.
func (p *BoundedAgentPool) Live() []*AgentHandle {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*AgentHandle, 0, len(p.live))
	for _, h := range p.live {
		out = append(out, h)
	}
	return out
}

// Counts returns (total, perModelCopy) for observability/testing.
func (p *BoundedAgentPool) Counts() (int, map[string]int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	perModel := make(map[string]int, len(p.perModel))
	for k, v := range p.perModel {
		perModel[k] = v
	}
	return p.total, perModel
}
