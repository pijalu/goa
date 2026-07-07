// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"text/template"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/prompts"
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
	cfg      config.OrchestratorConfig
	pool     *BoundedAgentPool
	store    EventStore
	topology Topology
	runID    string
	rootDir  string

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
	goalMu     sync.Mutex // guards the goal field
	goalCallMu sync.Mutex // serializes goal API calls (single-driver design)
	telemetry  Telemetry  // optional; nil-safe via telemetryOr
	name       string     // friendly alias, e.g. "happy.hare"

	cancelMu sync.Mutex
	cancel   context.CancelFunc // cancels the run context; set by Run

	// lastByRole remembers the most recent finished turn's streamed text per
	// role so MessageFor(role) (pipeline carry, tests) works WITHOUT the old
	// per-role accumulator that two concurrent delegate(coder) calls clobbered.
	// The source of truth for a single delegation's answer is the handle's own
	// accumulator (AgentHandle.AppendMessage / Message), not this map.
	lastMsgMu  sync.Mutex
	lastByRole map[string]string

	subMu sync.Mutex
	subs  []chan Event

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
		cfg:       cfg,
		pool:      pool,
		store:     store,
		topology:  top,
		rootDir:   rootDir,
		bus:       make(chan Event, 256),
		doneCh:    make(chan struct{}),
		lastByRole: map[string]string{},
		newID:     defaultRunID,
		telemetry: nopTelemetry{},
		sink:      newDurableSink(store),
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
	r.cancelMu.Unlock()
	defer func() {
		cancel()
		r.cancelMu.Lock()
		r.cancel = nil
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
		carry = r.lastMessageFor(role) + "\n\n" + r.renderPrompt("pipeline_carry", map[string]any{
			"Objective": objective,
		})
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
	if err := r.driveOne(ctx, "orchestrator", objective); err != nil {
		return err
	}
	// After the orchestrator's delegation turn, run a guaranteed synthesis
	// turn that inlines every specialist's output. The delegate tool result
	// already surfaces to the orchestrator within its first turn; this is a
	// robustness guarantee for models that stop after delegating without
	// summarizing. No-op when no specialist reported output.
	return r.synthesize(ctx, objective)
}

// synthesize runs a final orchestrator turn with a prompt that inlines every
// specialist's reported output, so the user sees a visible synthesis even when
// a model stops after delegating. Hub-only; no-op when no specialist reported
// output OR when the orchestrator already produced a final message in its
// delegation turn (the model followed the hub_orchestrator prompt's synthesis
// step, so a forced second turn would be redundant).
func (r *Runtime) synthesize(ctx context.Context, objective string) error {
	if strings.TrimSpace(r.MessageFor("orchestrator")) != "" {
		return nil
	}
	specialists := r.collectSpecialistOutputs()
	if specialists == "" {
		return nil
	}
	prompt := r.renderPrompt("hub_synthesis", map[string]any{
		"Objective":   objective,
		"Specialists": specialists,
	})
	if prompt == "" {
		return nil
	}
	return r.acquireAndRun(ctx, "orchestrator", prompt)
}

// collectSpecialistOutputs formats the final message of every non-orchestrator
// role that reported output, in deterministic (sorted) role order, for the
// synthesis prompt.
func (r *Runtime) collectSpecialistOutputs() string {
	roles := r.managedRoles()
	sort.Strings(roles)
	var b strings.Builder
	for _, role := range roles {
		msg := strings.TrimSpace(r.MessageFor(role))
		if msg == "" {
			continue
		}
		fmt.Fprintf(&b, "## %s\n%s\n\n", role, msg)
	}
	return strings.TrimSpace(b.String())
}

// driveOne acquires a single role agent, runs one turn, and emits the full
// lifecycle (Started → stats → Finished). It always releases the handle.
func (r *Runtime) driveOne(ctx context.Context, role, prompt string) error {
	// Resume: skip non-orchestrator roles that already finished in the prior
	// run (fanout/pipeline). The orchestrator role is always re-driven (it is
	// the hub entry point); hub specialist delegations are dynamic and are
	// not skipped.
	if role != "orchestrator" {
		if agentID, msg, ok := r.resumeFinishedRole(role); ok {
			r.resumeSkip(role, agentID, msg)
			return nil
		}
	}
	return r.acquireAndRun(ctx, role, r.renderRolePrompt(role, prompt))
}

// renderRolePrompt renders the topology-specific prompt for a role's turn.
func (r *Runtime) renderRolePrompt(role, prompt string) string {
	switch r.topology {
	case TopologyHub:
		if role == "orchestrator" {
			return r.renderPrompt("hub_orchestrator", map[string]any{"Objective": prompt})
		}
		return r.renderPrompt("fanout_role", map[string]any{"Objective": prompt})
	case TopologyPipeline:
		return r.renderPrompt("pipeline_role", map[string]any{"Objective": prompt})
	default:
		return r.renderPrompt("fanout_role", map[string]any{"Objective": prompt})
	}
}

// acquireAndRun acquires a role agent, emits Started, runs one turn with the
// given (already-rendered) prompt, and emits Stats + Finished. It always
// releases the handle. Shared by driveOne and the hub synthesis turn so both
// follow the identical lifecycle (goal-token accounting, outcome events).
func (r *Runtime) acquireAndRun(ctx context.Context, role, renderedPrompt string) error {
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
		Payload: map[string]any{"provider": h.Provider, "thinking": h.Thinking},
	})

	// The adapter's observer updates h.Stats during the turn. We bump the turn
	// counter around the call; RunTurn drains steering into the prompt.
	h.Stats.IncTurn()

	if renderedPrompt == "" {
		renderedPrompt = role
	}
	runErr := h.RunTurn(ctx, renderedPrompt)

	snap := h.Stats.Snapshot()
	r.emit(Event{Type: EventAgentStats, AgentID: h.ID, Role: h.Role,
		Payload: statsPayloadWithMeta(snap, h.Thinking)})

	if over, gerr := r.accrueGoalTokens(snap.TokensIn + snap.TokensOut); gerr != nil {
		return fmt.Errorf("goal token accounting: %w", gerr)
	} else if over {
		reason := "aggregate token budget exhausted"
		h.Stats.SetStatus(AgentCrashed)
		r.emit(Event{Type: EventAgentFinished, AgentID: h.ID, Role: h.Role,
			Payload: map[string]any{"outcome": "blocked", "reason": reason}})
		return errors.New(reason)
	}

	outcome := "ok"
	if runErr != nil {
		h.Stats.SetStatus(AgentCrashed)
		outcome = "crashed"
		r.emit(Event{Type: EventAgentFinished, AgentID: h.ID, Role: h.Role,
			Payload: map[string]any{"outcome": outcome, "error": runErr.Error()}})
		return fmt.Errorf("agent %s (%s): %w", h.ID, h.Role, runErr)
	}
	h.Stats.SetStatus(AgentFinished)
	r.setLastMessage(h.Role, h.Message())
	r.emit(Event{Type: EventAgentFinished, AgentID: h.ID, Role: h.Role,
		Payload: map[string]any{"outcome": outcome, "text": h.Message()}})
	return nil
}

// EmitLiveStats emits an in-flight EventAgentStats for h from its current
// Stats snapshot, but no more often than minInterval per handle. It lets the
// TUI show live token counts during a long turn instead of freezing until
// turn end. The event goes to live subscribers only (not the durable store).
// Safe to call from the agent's observer goroutine; returns whether it emitted.
func (r *Runtime) EmitLiveStats(h *AgentHandle, minInterval time.Duration) bool {
	if h == nil {
		return false
	}
	now := time.Now().UnixNano()
	last := h.statsEmitUnix.Load()
	if now-last < int64(minInterval) {
		return false
	}
	h.statsEmitUnix.Store(now)
	snap := h.Stats.Snapshot()
	evt := Event{Type: EventAgentStats, AgentID: h.ID, Role: h.Role,
		Payload: statsPayloadWithMeta(snap, h.Thinking)}
	if r.runID != "" {
		evt.RunID = r.runID
	}
	r.emitLive(evt)
	return true
}

// lastMessageFor returns the latest accumulated message for a role (used by
// pipeline carry). Delegates to MessageFor so it works with or without a store.
func (r *Runtime) lastMessageFor(role string) string {
	return r.MessageFor(role)
}

// RecordAgentMessage lets an adapter forward a streamed assistant chunk as an
// AgentMessage event for a handle (used by pipeline carry and the TUI). It is
// safe to call from the agent's observer goroutine. The chunk is accumulated
// on the handle (not a shared per-role buffer) so concurrent delegations of
// the same role stay isolated.
func (r *Runtime) RecordAgentMessage(h *AgentHandle, text string) {
	if h == nil || text == "" {
		return
	}
	h.AppendMessage(text)
	r.emit(Event{Type: EventAgentMessage, AgentID: h.ID, Role: h.Role,
		Payload: map[string]any{"text": text}})
}

// RecordAgentThinking emits a display-only thinking chunk event. It does NOT
// accumulate into r.msgs; thinking is transient UI state.
func (r *Runtime) RecordAgentThinking(h *AgentHandle, text string) {
	if h == nil || text == "" {
		return
	}
	r.emit(Event{Type: EventAgentThinking, AgentID: h.ID, Role: h.Role,
		Payload: map[string]any{"text": text}})
}

// RecordAgentToolCall emits a tool-call event so the TUI can render a running
// tool widget for the agent.
func (r *Runtime) RecordAgentToolCall(h *AgentHandle, tool, input, callID string) {
	if h == nil || tool == "" {
		return
	}
	r.emit(Event{Type: EventAgentToolCall, AgentID: h.ID, Role: h.Role,
		Payload: map[string]any{"tool": tool, "input": input, "call_id": callID}})
}

// RecordAgentToolCall emits a tool-result event so the TUI can finalize the
// corresponding tool widget.
func (r *Runtime) RecordAgentToolResult(h *AgentHandle, callID, text string, ok bool) {
	if h == nil {
		return
	}
	r.emit(Event{Type: EventAgentToolResult, AgentID: h.ID, Role: h.Role,
		Payload: map[string]any{"call_id": callID, "text": text, "ok": ok}})
}

// MessageFor returns the most recent finished turn's streamed text for a
// role (pipeline carry, fanout tests). It is NOT the source of truth for a
// specific delegation's answer — Delegate returns the handle's own Message()
// — so it is safe under concurrent same-role delegations.
func (r *Runtime) MessageFor(role string) string {
	r.lastMsgMu.Lock()
	defer r.lastMsgMu.Unlock()
	return r.lastByRole[role]
}

// setLastMessage records a finished turn's text for a role, feeding
// MessageFor. Called under the handle's owner after its turn completes.
func (r *Runtime) setLastMessage(role, msg string) {
	r.lastMsgMu.Lock()
	r.lastByRole[role] = msg
	r.lastMsgMu.Unlock()
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
		Payload: map[string]any{"delegated": true, "provider": h.Provider, "thinking": h.Thinking}})

	h.Stats.IncTurn()
	runErr := h.RunTurn(ctx, task)

	snap := h.Stats.Snapshot()
	r.emit(Event{Type: EventAgentStats, AgentID: h.ID, Role: h.Role, Payload: statsPayloadWithMeta(snap, h.Thinking)})

	if over, gerr := r.accrueGoalTokens(snap.TokensIn + snap.TokensOut); gerr != nil {
		return "", fmt.Errorf("goal token accounting: %w", gerr)
	} else if over {
		h.Stats.SetStatus(AgentCrashed)
		r.emit(Event{Type: EventAgentFinished, AgentID: h.ID, Role: h.Role,
			Payload: map[string]any{"outcome": "blocked", "reason": "aggregate token budget exhausted"}})
		return "", errors.New("aggregate token budget exhausted")
	}

	if runErr != nil {
		h.Stats.SetStatus(AgentCrashed)
		r.emit(Event{Type: EventAgentFinished, AgentID: h.ID, Role: h.Role,
			Payload: map[string]any{"outcome": "crashed", "error": runErr.Error()}})
		return "", fmt.Errorf("delegate %s: %w", role, runErr)
	}
	h.Stats.SetStatus(AgentFinished)
	r.setLastMessage(role, h.Message())
	r.emit(Event{Type: EventAgentFinished, AgentID: h.ID, Role: h.Role,
		Payload: map[string]any{"outcome": "ok", "text": h.Message()}})
	return h.Message(), nil
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

// statsPayloadWithMeta extends the base stats payload with the agent's
// effective thinking level so the TUI stats table can render the "think"
// column from a single agent_stats event.
func statsPayloadWithMeta(s AgentStatsSnapshot, thinking string) map[string]any {
	p := statsPayload(s)
	p["thinking"] = thinking
	return p
}

// renderPrompt executes an embedded orchestrator prompt template by name.
// If the prompt or template execution fails, it returns an empty string so the
// caller can fall back to the original prompt text.
func (r *Runtime) renderPrompt(name string, data map[string]any) string {
	tpl, err := prompts.LoadOrchestratePrompt(name)
	if err != nil {
		return ""
	}
	t, err := template.New(name).Parse(tpl)
	if err != nil {
		return ""
	}
	var buf strings.Builder
	if err := t.Execute(&buf, data); err != nil {
		return ""
	}
	return buf.String()
}

// SetTelemetry attaches a tracker for lifecycle events (nil → no-op).
func (r *Runtime) SetTelemetry(t Telemetry) {
	if t == nil {
		t = nopTelemetry{}
	}
	r.telemetry = t
}

// SetGoalBinder binds the run to a goal. Must be called before Run. When set,
// the runtime accrues aggregate token usage, blocks on budget exhaustion, and
// marks the goal complete on a successful finish.
func (r *Runtime) SetGoalBinder(gb GoalBinder) {
	r.goalMu.Lock()
	r.goal = gb
	r.goalMu.Unlock()
}

// SetGoalID records the goal id associated with the binder. It is emitted in
// the run_started event so the run snapshot can later recover it.
func (r *Runtime) SetGoalID(id string) { r.goalID = id }

// GoalBound reports whether the run has a goal binder attached.
func (r *Runtime) GoalBound() bool {
	r.goalMu.Lock()
	defer r.goalMu.Unlock()
	return r.goal != nil
}

// accrueGoalTokens reports a turn's token usage to the bound goal and returns
// (overBudget, err). It is a no-op when no goal is bound.
func (r *Runtime) accrueGoalTokens(tokens int) (bool, error) {
	r.goalMu.Lock()
	gb := r.goal
	r.goalMu.Unlock()
	if gb == nil || tokens <= 0 {
		return false, nil
	}
	r.goalCallMu.Lock()
	defer r.goalCallMu.Unlock()
	return gb.RecordTokens(tokens)
}

// finalizeGoal marks the bound goal complete (ok) or blocked (!ok). No-op when
// unbound.
func (r *Runtime) finalizeGoal(ok bool, reason string) {
	r.goalMu.Lock()
	gb := r.goal
	r.goalMu.Unlock()
	if gb == nil {
		return
	}
	r.goalCallMu.Lock()
	defer r.goalCallMu.Unlock()
	if ok {
		_ = gb.Complete(reason)
	} else {
		_ = gb.Block(reason)
	}
}

func defaultRunID() string {
	return internal.PrefixedHexID("run", 4)
}

// Stop tears down the durable sink, flushing any buffered events to disk.
// It should be called when the runtime is no longer needed (after Run
// returns and any final snapshot/Replay). It is idempotent and does not
// cancel an in-flight run — use Cancel for that. Stopping the sink keeps a
// long-lived process from leaking a writer goroutine per run (R7).
func (r *Runtime) Stop() {
	if r.sink != nil {
		r.sink.close()
	}
}

// Cancel requests the running orchestration to stop. It is safe to call
// multiple times and from any goroutine. If the run has already finished,
// Cancel is a no-op.
func (r *Runtime) Cancel() {
	r.cancelMu.Lock()
	cancel := r.cancel
	r.cancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
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
	ID            string
	Role          string
	Model         string
	Provider      string
	Thinking      string
	Status        AgentStatus
	Turns         int
	TokensIn      int
	TokensOut     int
	CacheRead     int
	CacheCreation int
	ToolCalls     int
	Messages      int
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
			Provider: h.Provider, Thinking: h.Thinking,
			Status: s.Status, Turns: s.Turns,
			TokensIn: s.TokensIn, TokensOut: s.TokensOut,
			CacheRead: s.CacheRead, CacheCreation: s.CacheCreation,
			ToolCalls: s.ToolCalls,
		})
	}
	return rows
}

// Objective returns the objective of the current/last run.
func (r *Runtime) Objective() string { return r.objective }

// Topology returns the topology of the current run.
func (r *Runtime) Topology() Topology { return r.topology }

// SetName sets the friendly human-readable alias for this run. Must be
// called before Run. The name is emitted in the run_started event and
// exposed by Name().
func (r *Runtime) SetName(name string) { r.name = name }

// Resume continues a prior run. Persistence is re-rooted to store (the
// existing run's event log, so new events append with continuing seq) and the
// run id is forced to snap.RunID so the resumed run is the same run, not a
// fork. During fanout/pipeline, roles that already reached a successful
// terminal state in snap are skipped (their snapshotted answer is carried
// forward) so completed work is not redone. Hub topology re-drives the
// orchestrator because delegations are dynamic.
//
// store must already have been Replay'd (so its internal seq continues from
// the existing log); snap is the replayed snapshot.
func (r *Runtime) Resume(store EventStore, snap *RunSnapshot) {
	if r.sink != nil {
		r.sink.close()
	}
	r.store = store
	r.sink = newDurableSink(store)
	if snap == nil {
		return
	}
	r.resume = snap
	runID := snap.RunID
	r.SetIDGenerator(func() string { return runID })
}

// resumeFinishedRole returns the snapshotted answer for role if that role
// already finished successfully in the resume snapshot (ok=true). Used to
// skip idempotent re-runs of completed fanout/pipeline stages.
func (r *Runtime) resumeFinishedRole(role string) (string, string, bool) {
	if r.resume == nil {
		return "", "", false
	}
	for _, a := range r.resume.Agents {
		if a.Role == role && a.Status == AgentFinished {
			return a.ID, strings.Join(a.Messages, ""), true
		}
	}
	return "", "", false
}

// resumeSkip emits a synthetic resumed lifecycle for a role whose work is
// carried forward from the prior run, without driving a new turn.
func (r *Runtime) resumeSkip(role, agentID, msg string) {
	r.setLastMessage(role, msg)
	r.emit(Event{Type: EventAgentStarted, AgentID: agentID, Role: role,
		Payload: map[string]any{"resumed": true}})
	r.emit(Event{Type: EventAgentFinished, AgentID: agentID, Role: role,
		Payload: map[string]any{"outcome": "resumed", "text": msg}})
}

// Name returns the friendly alias for this run (empty if not set).
func (r *Runtime) Name() string { return r.name }

// NameOrID returns the friendly name if set, otherwise the internal run ID.
func (r *Runtime) NameOrID() string {
	if r.name != "" {
		return r.name
	}
	return r.runID
}

// RunID returns the id assigned to the current run (empty before Run starts).
func (r *Runtime) RunID() string { return r.runID }
