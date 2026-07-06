// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/prompts"
)

// AgentStatus is the lifecycle state of a managed agent handle.
type AgentStatus string

const (
	// AgentPending means the handle has been acquired but has not started a
	// turn yet.
	AgentPending AgentStatus = "pending"
	// AgentRunning means the agent is currently streaming a turn.
	AgentRunning AgentStatus = "running"
	// AgentIdle means the agent is acquired but waiting between turns.
	AgentIdle AgentStatus = "idle"
	// AgentFinished means the agent reported a successful terminal outcome.
	AgentFinished AgentStatus = "finished"
	// AgentCrashed means the agent panicked or its turn failed terminally.
	AgentCrashed AgentStatus = "crashed"
)

// AgentStats holds live, lock-protected counters for a managed agent. All
// mutators are goroutine-safe; readers must use Snapshot.
type AgentStats struct {
	mu sync.Mutex

	Turns         int
	TokensIn      int
	TokensOut     int
	CacheRead     int
	CacheCreation int
	ToolCalls     int
	Status        AgentStatus
	StartedAt     time.Time
	UpdatedAt     time.Time
}

// AgentStatsSnapshot is an immutable point-in-time copy of AgentStats.
type AgentStatsSnapshot struct {
	Turns         int
	TokensIn      int
	TokensOut     int
	CacheRead     int
	CacheCreation int
	ToolCalls     int
	Status        AgentStatus
	StartedAt     time.Time
	UpdatedAt     time.Time
}

// NewAgentStats returns a zeroed stats object in the Pending state.
func NewAgentStats() *AgentStats {
	now := time.Now()
	return &AgentStats{Status: AgentPending, StartedAt: now, UpdatedAt: now}
}

// Snapshot returns an immutable copy of the current stats.
func (s *AgentStats) Snapshot() AgentStatsSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return AgentStatsSnapshot{
		Turns:         s.Turns,
		TokensIn:      s.TokensIn,
		TokensOut:     s.TokensOut,
		CacheRead:     s.CacheRead,
		CacheCreation: s.CacheCreation,
		ToolCalls:     s.ToolCalls,
		Status:        s.Status,
		StartedAt:     s.StartedAt,
		UpdatedAt:     s.UpdatedAt,
	}
}

// AddUsage merges a usage delta into the stats. Negative deltas are ignored.
func (s *AgentStats) AddUsage(tokensIn, tokensOut, cacheRead, cacheCreation int) {
	if tokensIn < 0 || tokensOut < 0 || cacheRead < 0 || cacheCreation < 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.TokensIn += tokensIn
	s.TokensOut += tokensOut
	s.CacheRead += cacheRead
	s.CacheCreation += cacheCreation
	s.UpdatedAt = time.Now()
}

// IncToolCall increments the tool-call counter.
func (s *AgentStats) IncToolCall() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ToolCalls++
	s.UpdatedAt = time.Now()
}

// IncTurn increments the turn counter.
func (s *AgentStats) IncTurn() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Turns++
	s.UpdatedAt = time.Now()
}

// SetStatus transitions the agent status and stamps UpdatedAt.
func (s *AgentStats) SetStatus(st AgentStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Status = st
	s.UpdatedAt = time.Now()
}

// TurnFunc drives one turn of the underlying agent for a handle. Production
// wires it to agentic.Agent.Run via an adapter; tests substitute a fake so
// the runtime is exercisable without a live provider. The handle does NOT
// import agentic — keeping this package pure (SOLID: dependency inversion).
type TurnFunc func(ctx context.Context, prompt string) error

// AgentHandle is a live, pool-tracked reference to a managed sub-agent.
//
// It carries its own steering queue (one per agent, generalized from the
// B5 SteeringQueue) so the orchestrator and the user can inject follow-up
// messages targeted at this specific agent between turns.
type AgentHandle struct {
	// ID is a unique, run-scoped identifier (e.g. "coder-1").
	ID string
	// Role is the configured role name this handle was acquired under.
	Role string
	// Model is the resolved model id for this handle.
	Model string

	// Stats tracks live usage counters.
	Stats *AgentStats
	// Steering buffers pending user/orchestrator messages for this agent.
	Steering *core.SteeringQueue

	// Run drives one turn of the underlying agent. Set by the factory (the
	// adapter wires it to agentic.Agent.Run). Nil-safe: a nil Run is a no-op
	// turn (used by pure-logic tests that don't drive a real agent).
	Run TurnFunc

	// done is closed when the handle has reached a terminal state and the
	// pool slot has been released.
	done chan struct{}
}

// NewAgentHandle constructs a handle with fresh stats and an empty steering
// queue. The handle is in the Pending state.
func NewAgentHandle(id, role, model string) *AgentHandle {
	return &AgentHandle{
		ID:       id,
		Role:     role,
		Model:    model,
		Stats:    NewAgentStats(),
		Steering: core.NewSteeringQueue(),
		done:     make(chan struct{}),
	}
}

// Done returns a channel closed when the handle is terminal and released.
func (h *AgentHandle) Done() <-chan struct{} { return h.done }

// RunTurn drives one turn, draining pending steering into the prompt first
// (kimi-code append-on-top model). A nil Run func is treated as a successful
// no-op turn so pure-logic tests can drive the runtime without an agent.
func (h *AgentHandle) RunTurn(ctx context.Context, basePrompt string) error {
	prompt := basePrompt
	steeringPrefix, _ := prompts.LoadOrchestratePrompt("steering_prefix")
	steeringPrefix = strings.TrimSpace(steeringPrefix)
	if steeringPrefix == "" {
		steeringPrefix = "[Steering]"
	}
	if extra := h.DrainSteering(); len(extra) > 0 {
		prompt = prompt + "\n\n" + steeringPrefix + " " + strings.Join(extra, "\n"+steeringPrefix+" ")
	}
	if h.Run == nil {
		return nil
	}
	return h.Run(ctx, prompt)
}

// Steer appends a steering message to this agent's queue.
func (h *AgentHandle) Steer(text string) {
	if h.Steering == nil {
		return
	}
	h.Steering.Append(text)
}

// DrainSteering flushes and returns pending steering messages.
func (h *AgentHandle) DrainSteering() []string {
	if h.Steering == nil {
		return nil
	}
	return h.Steering.Flush()
}

// markReleased closes the done channel. It is idempotent.
func (h *AgentHandle) markReleased() {
	select {
	case <-h.done:
	default:
		close(h.done)
	}
}
