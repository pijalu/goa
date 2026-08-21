// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"context"
	"errors"
	"sort"
	"sync"
	"sync/atomic"

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
	promptDir string // user prompt override directory; empty means embedded only

	// resume, when set by Resume(), records the snapshot of a prior run so
	// fanout/pipeline skip roles that already finished successfully and the
	// run continues under the same run-id/event-log. Hub re-drives the
	// orchestrator (delegations are dynamic, so prior specialist results are
	// not reused).
	resume *RunSnapshot

	emitMu sync.Mutex
	bus    chan Event
	closed atomic.Bool

	// sink persists events off the streaming hot path (R1). nil when no store
	// is configured (in-memory runs); emit then skips durable persistence.
	sink *durableSink

	doneCh chan struct{}

	objective  string
	goal       GoalBinder // optional; when set, the run is goal-bound
	goalID     string     // goal id for the bound goal
	planID     string     // plan id when the run is plan-bound
	goalMu     sync.Mutex // guards the goal field
	goalCallMu sync.Mutex // serializes goal API calls (single-driver design)
	telemetry  Telemetry  // optional; nil-safe via telemetryOr
	name       string     // friendly alias, e.g. "happy.hare"

	cancelMu sync.Mutex
	cancel   context.CancelFunc // cancels the run context; set by Run
	runCtx   context.Context    // context passed to Run, used by async delegations

	// lastByRole remembers the most recent finished turn's streamed text per
	// role so MessageFor(role) (pipeline carry, tests) works WITHOUT the old
	// per-role accumulator that two concurrent delegate(coder) calls clobbered.
	// The source of truth for a single delegation's answer is the handle's own
	// accumulator (AgentHandle.AppendMessage / Message), not this map.
	lastMsgMu  sync.Mutex
	lastByRole map[string]string

	// pending tracks async delegations started by the hub orchestrator so the
	// runtime can wait for them before the next orchestrator turn.
	pendingMu   sync.Mutex
	pending     map[string]struct{}
	pendingDone chan struct{}

	// conversationLoop state for the persistent hub orchestrator.
	loopMu          sync.Mutex
	loopActive      bool
	lastAction      toolAction
	pendingUser     bool
	pendingQuestion string
	resumeCh        chan struct{}

	// orchSteer buffers user answers (and other steering) for the orchestrator
	// when no orchestrator handle is live (e.g., while the loop is paused).
	orchSteerMu sync.Mutex
	orchSteer   []string

	subMu sync.Mutex
	subs  []chan Event

	// newID generates a unique run id. Override in tests for determinism.
	newID func() string
}

// toolAction records the highest-priority action the orchestrator took in its
// last turn. The loop uses this to decide whether to pause, run specialists, or
// finish.
type toolAction int

const (
	actionNone toolAction = iota
	actionDelegate
	actionRework
	actionAskUser
)

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
		cfg:        cfg,
		pool:       pool,
		store:      store,
		topology:   top,
		rootDir:    rootDir,
		bus:        make(chan Event, 256),
		doneCh:     make(chan struct{}),
		lastByRole: map[string]string{},
		pending:    map[string]struct{}{},
		newID:      defaultRunID,
		telemetry:  nopTelemetry{},
		sink:       newDurableSink(store),
	}, nil
}

// Events returns a subscription channel for live TUI consumption. The channel
// is closed when the run finishes. Receivers must be prepared for non-blocking
// drops if they fall behind (bus is buffered; overflow drops to keep the run
// progressing — the on-disk store is the source of truth).
func (r *Runtime) Events() <-chan Event { return r.bus }

// SetIDGenerator overrides the run-id generator (tests).
func (r *Runtime) SetIDGenerator(fn func() string) { r.newID = fn }

// emit persists the event (best-effort, off the hot path via the durable
// sink) and fans it out to live subscribers. It never blocks the run: the
// durable submit is non-blocking (overflow is counted, not waited on), and
// the live send is non-blocking. This is the R1 invariant: emitting an event
// — which happens per streamed token — must not stall the agent's reader.
func (r *Runtime) emit(evt Event) {
	if r.runID != "" && evt.RunID == "" {
		evt.RunID = r.runID
	}
	if r.sink != nil {
		r.sink.submit(evt)
	} else if r.store != nil {
		_ = r.store.Append(evt)
	}
	r.emitLive(evt)
}

// emitLive fans an event out to live subscribers (bus + Subscribe channels)
// WITHOUT persisting it. Used for high-frequency in-flight updates (live token
// stats) that must reach the TUI but should not bloat the durable event log —
// the turn-end EventAgentStats is the persisted record.
func (r *Runtime) emitLive(evt Event) {
	r.emitMu.Lock()
	defer r.emitMu.Unlock()
	if r.closed.Load() {
		return
	}
	select {
	case r.bus <- evt:
	default:
	}
	r.fanout(evt)
}

// fanout delivers a copy of the event to every Subscribe() consumer without
// blocking the run; slow consumers drop (the on-disk store is the source of
// truth). Callers other than emit do not need the lock.
func (r *Runtime) fanout(evt Event) {
	r.subMu.Lock()
	defer r.subMu.Unlock()
	for _, ch := range r.subs {
		select {
		case ch <- evt:
		default:
		}
	}
}

// Subscribe returns a fresh channel receiving every emitted event for the
// remainder of this run. Used by the TUI panel forwarder; the command's chat
// forwarder continues to use Events() (the legacy single bus). Closing the
// runtime's bus at run end does not close subscriber channels — consumers
// should also select on Done().
func (r *Runtime) Subscribe() <-chan Event {
	ch := make(chan Event, 64)
	r.subMu.Lock()
	r.subs = append(r.subs, ch)
	r.subMu.Unlock()
	return ch
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
	runCtx, cancel := context.WithCancel(ctx)
	r.cancelMu.Lock()
	r.cancel = cancel
	r.runCtx = runCtx
	r.cancelMu.Unlock()
	defer func() {
		cancel()
		r.cancelMu.Lock()
		r.cancel = nil
		r.runCtx = nil
		r.cancelMu.Unlock()
	}()

	r.runID = r.newID()
	r.objective = objective
	if r.store == nil {
		// Still set runID on emitted events (handled in emit).
	}

	payload := map[string]any{
		"objective": objective,
		"topology":  string(r.topology),
	}
	if r.name != "" {
		payload["name"] = r.name
	}
	if r.goalID != "" {
		payload["goal_id"] = r.goalID
	}
	if r.planID != "" {
		payload["plan_id"] = r.planID
	}
	r.emit(Event{
		Type:    EventRunStarted,
		Payload: payload,
	})
	r.telemetry.Track(TelemetryRunStarted, map[string]any{
		"topology": string(r.topology),
		"roles":    len(r.cfg.Roles),
		"goal":     r.GoalBound(),
	})

	var err error
	switch r.topology {
	case TopologyPipeline:
		err = r.runPipeline(ctx, objective)
	case TopologyHub:
		// Hub without DelegateTool behaves like fanout over the non-orchestrator
		// roles; the orchestrator role (if any) runs first to "plan".
		err = r.runHub(runCtx, objective)
	default:
		err = r.runFanout(runCtx, objective)
	}

	r.emit(Event{Type: EventRunFinished, Payload: map[string]any{"ok": err == nil}})
	r.telemetry.Track(TelemetryRunFinished, map[string]any{"ok": err == nil})
	r.finalizeGoal(err == nil, runFinishReason(err))
	r.closeBus()
	// Drain the durable sink so the on-disk event log is complete (including
	// the run_finished event) before callers snapshot/replay/export it.
	r.Stop()
	close(r.doneCh)
	return err
}

func runFinishReason(err error) string {
	if err != nil {
		return "run failed: " + err.Error()
	}
	return "all agents finished"
}

// managedRoles returns the configured roles that should actually run as
// sub-agents (everything except the reserved "orchestrator" role), in
// deterministic (sorted) order so pipeline stages and fanout acquisition are
// reproducible across runs (Go map iteration order is random).
func (r *Runtime) managedRoles() []string {
	var roles []string
	for name := range r.cfg.Roles {
		if name == "orchestrator" {
			continue
		}
		roles = append(roles, name)
	}
	sort.Strings(roles)
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
		carry = r.lastMessageFor(role) + "\n\n" + r.renderPrompt("pipeline_carry", map[string]any{
			"Objective": objective,
		})
	}
	return nil
}

// runHub drives the orchestrator role in a persistent conversation loop. The
// orchestrator may delegate to specialists, ask the user questions, or output a
// final answer; the loop continues until a final answer is produced. Falls
// back to fanout when no orchestrator role is configured.
func (r *Runtime) runHub(ctx context.Context, objective string) error {
	if _, ok := r.cfg.Roles["orchestrator"]; !ok {
		return r.runFanout(ctx, objective)
	}
	return r.runOrchestratorLoop(ctx, objective)
}

// runOrchestratorLoop runs the orchestrator role repeatedly, feeding specialist
// outputs and user answers back as new context until the orchestrator outputs
// a final answer. It is the user <-> orchestrator <-> sub-agent(s) conversation
// model.
