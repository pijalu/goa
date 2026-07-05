// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pijalu/goa/config"
)

// Emitter forwards an orchestrator Event to the run's store and any live TUI
// subscriber. The runtime owns one Emitter; adapters/handles call it via the
// factory closure so this package stays free of agentic imports.
type Emitter func(Event)

// Runtime drives a single orchestration run. It composes the bounded pool
// (Phase 1), the agent handles (Phase 2), the topology selector (Phase 3),
// and the event store (Phase 4) into an executable run.
//
// The Runtime is intentionally agnostic about HOW an agent turn is driven and
// HOW agentic output maps to events — those are supplied by an adapter
// (internal/app) so this package is unit-testable without a live provider.
type Runtime struct {
	cfg       config.OrchestratorConfig
	pool      *BoundedAgentPool
	store     EventStore
	topology  Topology
	runID     string
	rootDir   string

	emitMu sync.Mutex
	bus    chan Event
	closed atomic.Bool

	doneCh chan struct{}

	objective string

	// msgs accumulates streamed assistant text per role so Delegate can return
	// a sub-agent's answer as the tool result without depending on the store.
	msgMu sync.Mutex
	msgs  map[string][]string

	// newID generates a unique run id. Override in tests for determinism.
	newID func() string
}

// NewRuntime constructs a Runtime. The pool must already wrap a factory that
// attaches the adapter's observer (so Stats/Events flow during turns). The
// store may be nil for in-memory-only runs (events still flow on the bus).
func NewRuntime(cfg config.OrchestratorConfig, pool *BoundedAgentPool, store EventStore, rootDir string) (*Runtime, error) {
	top, err := ParseTopology(cfg.Defaults.Topology, "")
	if err != nil {
		return nil, err
	}
	if pool == nil {
		return nil, errors.New("orchestrator: nil pool")
	}
	return &Runtime{
		cfg:      cfg,
		pool:     pool,
		store:    store,
		topology: top,
		rootDir:  rootDir,
		bus:      make(chan Event, 256),
		doneCh:   make(chan struct{}),
		msgs:     map[string][]string{},
		newID:    defaultRunID,
	}, nil
}

// Events returns a subscription channel for live TUI consumption. The channel
// is closed when the run finishes. Receivers must be prepared for non-blocking
// drops if they fall behind (bus is buffered; overflow drops to keep the run
// progressing — the on-disk store is the source of truth).
func (r *Runtime) Events() <-chan Event { return r.bus }

// SetIDGenerator overrides the run-id generator (tests).
func (r *Runtime) SetIDGenerator(fn func() string) { r.newID = fn }

// emit appends to the store (best-effort) and fans out on the bus. It never
// blocks the run: store errors are swallowed (logged via event), and the bus
// send is non-blocking.
func (r *Runtime) emit(evt Event) {
	if r.runID != "" && evt.RunID == "" {
		evt.RunID = r.runID
	}
	if r.store != nil {
		_ = r.store.Append(evt)
	}
	r.emitMu.Lock()
	defer r.emitMu.Unlock()
	if r.closed.Load() {
		return
	}
	select {
	case r.bus <- evt:
	default:
	}
}

// closeBus seals the event bus at run end.
func (r *Runtime) closeBus() {
	r.emitMu.Lock()
	defer r.emitMu.Unlock()
	if r.closed.Swap(true) {
		return
	}
	close(r.bus)
}

// Run drives the configured topology against the objective. It selects
// fanout/pipeline directly; hub currently falls back to fanout semantics
// (full hub needs the DelegateTool, tracked under Phase 3 remainder).
func (r *Runtime) Run(ctx context.Context, objective string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.runID = r.newID()
	r.objective = objective
	if r.store == nil {
		// Still set runID on emitted events (handled in emit).
	}

	r.emit(Event{
		Type: EventRunStarted,
		Payload: map[string]any{
			"objective": objective,
			"topology":  string(r.topology),
		},
	})

	var err error
	switch r.topology {
	case TopologyPipeline:
		err = r.runPipeline(ctx, objective)
	case TopologyHub:
		// Hub without DelegateTool behaves like fanout over the non-orchestrator
		// roles; the orchestrator role (if any) runs first to "plan".
		err = r.runHub(ctx, objective)
	default:
		err = r.runFanout(ctx, objective)
	}

	r.emit(Event{Type: EventRunFinished, Payload: map[string]any{"ok": err == nil}})
	r.closeBus()
	close(r.doneCh)
	return err
}

// managedRoles returns the configured roles that should actually run as
// sub-agents (everything except the reserved "orchestrator" role).
func (r *Runtime) managedRoles() []string {
	var roles []string
	for name := range r.cfg.Roles {
		if name == "orchestrator" {
			continue
		}
		roles = append(roles, name)
	}
	return roles
}

// runFanout acquires one agent per managed role and drives a single turn in
// parallel, honouring pool caps (Acquire blocks/queues under contention).
func (r *Runtime) runFanout(ctx context.Context, objective string) error {
	roles := r.managedRoles()
	if len(roles) == 0 {
		return errors.New("orchestrator: no managed roles configured (add roles other than \"orchestrator\")")
	}
	var wg sync.WaitGroup
	var firstErr atomic.Pointer[error]
	for _, role := range roles {
		wg.Add(1)
		go func(role string) {
			defer wg.Done()
			if err := r.driveOne(ctx, role, objective); err != nil {
				if p := firstErr.Load(); p == nil {
					e := err
					firstErr.Store(&e)
				}
			}
		}(role)
	}
	wg.Wait()
	if p := firstErr.Load(); p != nil {
		return *p
	}
	return nil
}

// runPipeline runs managed roles in configured-map iteration order, feeding
// each agent's output as context to the next. (Map order is non-deterministic;
// callers wanting strict ordering should configure roles via an ordered
// pipeline — tracked separately. For now stages run sequentially.)
func (r *Runtime) runPipeline(ctx context.Context, objective string) error {
	roles := r.managedRoles()
	if len(roles) == 0 {
		return errors.New("orchestrator: no managed roles configured")
	}
	carry := objective
	for _, role := range roles {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := r.driveOne(ctx, role, carry); err != nil {
			return err
		}
		// Feed this agent's last streamed message forward as context.
		carry = r.lastMessageFor(role) + "\n\nContinue the pipeline with the above context. Objective: " + objective
	}
	return nil
}

// runHub drives ONLY the orchestrator role; it delegates to specialists via
// the DelegateTool (wired by the adapter). Falls back to fanout when no
// orchestrator role is configured.
func (r *Runtime) runHub(ctx context.Context, objective string) error {
	if _, ok := r.cfg.Roles["orchestrator"]; !ok {
		return r.runFanout(ctx, objective)
	}
	return r.driveOne(ctx, "orchestrator", objective)
}

// driveOne acquires a single role agent, runs one turn, and emits the full
// lifecycle (Started → stats → Finished). It always releases the handle.
func (r *Runtime) driveOne(ctx context.Context, role, prompt string) error {
	h, err := r.pool.Acquire(ctx, role)
	if err != nil {
		r.emit(Event{Type: EventAgentFinished, Role: role,
			Payload: map[string]any{"outcome": "acquire_failed", "error": err.Error()}})
		return err
	}
	defer r.pool.Release(h)

	h.Stats.SetStatus(AgentRunning)
	r.emit(Event{
		Type:    EventAgentStarted,
		AgentID: h.ID, Role: h.Role, Model: h.Model,
	})

	// The adapter's observer updates h.Stats during the turn. We bump the turn
	// counter around the call; RunTurn drains steering into the prompt.
	h.Stats.IncTurn()
	runErr := h.RunTurn(ctx, prompt)

	snap := h.Stats.Snapshot()
	r.emit(Event{Type: EventAgentStats, AgentID: h.ID, Role: h.Role,
		Payload: statsPayload(snap)})

	outcome := "ok"
	if runErr != nil {
		h.Stats.SetStatus(AgentCrashed)
		outcome = "crashed"
		r.emit(Event{Type: EventAgentFinished, AgentID: h.ID, Role: h.Role,
			Payload: map[string]any{"outcome": outcome, "error": runErr.Error()}})
		return fmt.Errorf("agent %s (%s): %w", h.ID, h.Role, runErr)
	}
	h.Stats.SetStatus(AgentFinished)
	r.emit(Event{Type: EventAgentFinished, AgentID: h.ID, Role: h.Role,
		Payload: map[string]any{"outcome": outcome}})
	return nil
}

// lastMessageFor returns the latest accumulated message for a role (used by
// pipeline carry). Delegates to MessageFor so it works with or without a store.
func (r *Runtime) lastMessageFor(role string) string {
	return r.MessageFor(role)
}

// RecordAgentMessage lets an adapter forward a streamed assistant chunk as an
// AgentMessage event for a handle (used by pipeline carry and the TUI). It is
// safe to call from the agent's observer goroutine.
func (r *Runtime) RecordAgentMessage(h *AgentHandle, text string) {
	if h == nil || text == "" {
		return
	}
	r.msgMu.Lock()
	r.msgs[h.Role] = append(r.msgs[h.Role], text)
	r.msgMu.Unlock()
	r.emit(Event{Type: EventAgentMessage, AgentID: h.ID, Role: h.Role,
		Payload: map[string]any{"text": text}})
}

// MessageFor returns the full streamed assistant text accumulated for a role
// in this run so far. Used by Delegate to return a sub-agent's answer.
func (r *Runtime) MessageFor(role string) string {
	r.msgMu.Lock()
	defer r.msgMu.Unlock()
	return strings.Join(r.msgs[role], "")
}

// Delegate acquires a role agent, runs a single turn for `task`, releases it,
// and returns the agent's streamed answer. It is the runtime primitive behind
// the hub topology's DelegateTool: the orchestrator agent calls it from within
// its own turn to dispatch work to a specialist.
func (r *Runtime) Delegate(ctx context.Context, role, task string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	h, err := r.pool.Acquire(ctx, role)
	if err != nil {
		return "", err
	}
	defer r.pool.Release(h)

	h.Stats.SetStatus(AgentRunning)
	r.emit(Event{Type: EventAgentStarted, AgentID: h.ID, Role: h.Role, Model: h.Model,
		Payload: map[string]any{"delegated": true}})

	// Reset per-role accumulation so MessageFor returns only this turn's text.
	r.msgMu.Lock()
	r.msgs[role] = nil
	r.msgMu.Unlock()

	h.Stats.IncTurn()
	runErr := h.RunTurn(ctx, task)

	snap := h.Stats.Snapshot()
	r.emit(Event{Type: EventAgentStats, AgentID: h.ID, Role: h.Role, Payload: statsPayload(snap)})

	if runErr != nil {
		h.Stats.SetStatus(AgentCrashed)
		r.emit(Event{Type: EventAgentFinished, AgentID: h.ID, Role: h.Role,
			Payload: map[string]any{"outcome": "crashed", "error": runErr.Error()}})
		return "", fmt.Errorf("delegate %s: %w", role, runErr)
	}
	h.Stats.SetStatus(AgentFinished)
	r.emit(Event{Type: EventAgentFinished, AgentID: h.ID, Role: h.Role,
		Payload: map[string]any{"outcome": "ok"}})
	return r.MessageFor(role), nil
}

// Pool exposes the bounded pool so adapters can build tools (e.g. DelegateTool)
// that need to acquire/release handles directly.
func (r *Runtime) Pool() *BoundedAgentPool { return r.pool }

func statsPayload(s AgentStatsSnapshot) map[string]any {
	return map[string]any{
		"turns":          s.Turns,
		"tokens_in":      s.TokensIn,
		"tokens_out":     s.TokensOut,
		"cache_read":     s.CacheRead,
		"cache_creation": s.CacheCreation,
		"tool_calls":     s.ToolCalls,
		"status":         string(s.Status),
	}
}

func defaultRunID() string {
	return fmt.Sprintf("run-%d", time.Now().UnixNano())
}

// Done returns a channel closed when Run finishes (success or error). Allows
// the TUI / command layer to know when to unsubscribe and clear the active
// runtime holder.
func (r *Runtime) Done() <-chan struct{} {
	return r.doneCh
}

// SteerAgent appends a steering message to a specific live handle (by id).
// Returns true if the handle was found. Safe from any goroutine.
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
// orchestrator role if present). Used by the Summary tab.
func (r *Runtime) SteerAll(text string) {
	for _, h := range r.pool.Live() {
		h.Steer(text)
		r.emit(Event{Type: EventAgentSteered, AgentID: h.ID, Role: h.Role,
			Payload: map[string]any{"from": "broadcast", "text": text}})
	}
}

// SteerOrchestrator targets the orchestrator-role handle only.
func (r *Runtime) SteerOrchestrator(text string) bool {
	for _, h := range r.pool.Live() {
		if h.Role == "orchestrator" {
			h.Steer(text)
			r.emit(Event{Type: EventAgentSteered, AgentID: h.ID, Role: h.Role,
				Payload: map[string]any{"from": "user", "text": text}})
			return true
		}
	}
	return false
}

// AgentRow is one row of the Summary snapshot, used by the TUI table.
type AgentRow struct {
	ID       string
	Role     string
	Model    string
	Status   AgentStatus
	Turns    int
	TokensIn int
	TokensOut int
	ToolCalls int
	Messages  int
}

// Snapshot returns the current live-agent rows for the Summary tab. It is a
// point-in-time copy; mutating it does not affect the run.
func (r *Runtime) Snapshot() []AgentRow {
	handles := r.pool.Live()
	rows := make([]AgentRow, 0, len(handles))
	for _, h := range handles {
		s := h.Stats.Snapshot()
		rows = append(rows, AgentRow{
			ID: h.ID, Role: h.Role, Model: h.Model,
			Status: s.Status, Turns: s.Turns,
			TokensIn: s.TokensIn, TokensOut: s.TokensOut,
			ToolCalls: s.ToolCalls,
		})
	}
	return rows
}

// Objective returns the objective of the current/last run.
func (r *Runtime) Objective() string { return r.objective }

// Topology returns the topology of the current run.
func (r *Runtime) Topology() Topology { return r.topology }

// RunID returns the id assigned to the current run (empty before Run starts).
func (r *Runtime) RunID() string { return r.runID }
