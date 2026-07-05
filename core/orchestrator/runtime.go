// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"context"
	"errors"
	"fmt"
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

// runHub runs the "orchestrator" role first (if configured), then fanout over
// the rest. A full hub uses DelegateTool; this is the cap-respecting fallback.
func (r *Runtime) runHub(ctx context.Context, objective string) error {
	if _, ok := r.cfg.Roles["orchestrator"]; ok {
		if err := r.driveOne(ctx, "orchestrator", objective); err != nil {
			return err
		}
	}
	return r.runFanout(ctx, objective)
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

// lastMessageFor is a best-effort carry for pipeline mode. The adapter emits
// AgentMessage events with the streamed text; we replay the snapshot to find
// the latest for a role.
func (r *Runtime) lastMessageFor(role string) string {
	if r.store == nil {
		return ""
	}
	events, _ := r.store.Replay()
	var last string
	for _, e := range events {
		if e.Role == role && e.Type == EventAgentMessage {
			if t, ok := e.Payload["text"].(string); ok {
				last = t
			}
		}
	}
	return last
}

// RecordAgentMessage lets an adapter forward a streamed assistant chunk as an
// AgentMessage event for a handle (used by pipeline carry and the TUI). It is
// safe to call from the agent's observer goroutine.
func (r *Runtime) RecordAgentMessage(h *AgentHandle, text string) {
	if h == nil || text == "" {
		return
	}
	r.emit(Event{Type: EventAgentMessage, AgentID: h.ID, Role: h.Role,
		Payload: map[string]any{"text": text}})
}

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
