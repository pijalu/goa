// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"time"
)

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
// tool widget for the agent. isDelta reports whether input is a partial
// streaming update for an existing call_id.
func (r *Runtime) RecordAgentToolCall(h *AgentHandle, tool, input, callID string, isDelta bool) {
	if h == nil || tool == "" {
		return
	}
	r.emit(Event{Type: EventAgentToolCall, AgentID: h.ID, Role: h.Role,
		Payload: map[string]any{"tool": tool, "input": input, "call_id": callID, "is_delta": isDelta}})
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
// Delegate is the default form of DelegateWith: it reuses the pooled agent
// for the role so the specialist accumulates context across sequential
// delegations.
func (r *Runtime) Delegate(ctx context.Context, role, task string) (string, error) {
	return r.DelegateWith(ctx, role, task, AcquireOptions{})
}

// DelegateWith is the synchronous, option-carrying form of Delegate. It runs
// one full specialist turn and returns the sub-agent's answer. It is still used
// by tests and by callers that need a blocking result.
func (r *Runtime) DelegateWith(ctx context.Context, role, task string, opts AcquireOptions) (string, error) {
	h, err := r.startDelegate(ctx, role, task, opts)
	if err != nil {
		return "", err
	}
	defer r.pool.Release(h)
	return r.runDelegateTurn(ctx, h, task)
}

// DelegateAsync is the conversation-style hub form of Delegate. It starts the
// specialist turn in a background goroutine and returns a placeholder so the
// orchestrator can end its planning turn without blocking. The runtime waits
// for all async delegations before the synthesis turn. The background goroutine
// uses the runtime's run context, not the tool call context, so it survives
// the end of the orchestrator's turn.
func (r *Runtime) DelegateAsync(ctx context.Context, role, task string, opts AcquireOptions) (string, error) {
	h, err := r.startDelegate(ctx, role, task, opts)
	if err != nil {
		return "", err
	}
	r.cancelMu.Lock()
	runCtx := r.runCtx
	r.cancelMu.Unlock()
	if runCtx == nil {
		runCtx = ctx
	}
	if err := runCtx.Err(); err != nil {
		r.pool.Release(h)
		return "", err
	}
	r.SetLastAction(actionDelegate)
	r.trackPending(h.ID)
	go func() {
		defer r.pool.Release(h)
		_, _ = r.runDelegateTurn(runCtx, h, task)
		r.untrackPending(h.ID)
	}()
	return fmt.Sprintf("[%s] task delegated; result will be synthesized", role), nil
}

// startDelegate acquires a specialist handle and emits its start event. The
// caller must either run the turn (and release the handle) or start it in a
// goroutine that releases when finished.
func (r *Runtime) startDelegate(ctx context.Context, role, task string, opts AcquireOptions) (*AgentHandle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	h, err := r.pool.Acquire(ctx, role, opts)
	if err != nil {
		return nil, err
	}
	h.Stats.SetStatus(AgentRunning)
	r.emit(Event{Type: EventAgentStarted, AgentID: h.ID, Role: h.Role, Model: h.Model,
		Payload: map[string]any{"delegated": true, "provider": h.Provider, "thinking": h.Thinking}})
	h.Stats.IncTurn()
	return h, nil
}

// runDelegateTurn executes one specialist turn and emits its lifecycle
// (stats, finished, errors). It returns the sub-agent's answer or the error.
func (r *Runtime) runDelegateTurn(ctx context.Context, h *AgentHandle, task string) (string, error) {
	runErr := h.RunTurn(ctx, task)

	snap := h.Stats.Snapshot()
	r.emit(Event{Type: EventAgentStats, AgentID: h.ID, Role: h.Role, Payload: statsPayloadWithMeta(snap, h.Thinking)})

	if over, gerr := r.accrueGoalTokens(snap.TokensIn + snap.TokensOut); gerr != nil {
		h.Stats.SetStatus(AgentCrashed)
		r.emit(Event{Type: EventAgentFinished, AgentID: h.ID, Role: h.Role,
			Payload: map[string]any{"outcome": "blocked", "reason": "goal token accounting: " + gerr.Error()}})
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
		return "", fmt.Errorf("delegate %s: %w", h.Role, runErr)
	}
	h.Stats.SetStatus(AgentFinished)
	r.setLastMessage(h.Role, h.Message())
	r.emit(Event{Type: EventAgentFinished, AgentID: h.ID, Role: h.Role,
		Payload: map[string]any{"outcome": "ok", "text": h.Message()}})
	return h.Message(), nil
}

// trackPending records an in-flight async delegation so WaitForDelegations
// can block until all specialists finish.
func (r *Runtime) trackPending(id string) {
	r.pendingMu.Lock()
	if r.pending == nil {
		r.pending = map[string]struct{}{}
	}
	r.pending[id] = struct{}{}
	if r.pendingDone == nil {
		r.pendingDone = make(chan struct{})
	}
	r.pendingMu.Unlock()
}

// untrackPending removes an in-flight async delegation. When the last pending
// delegation finishes, it closes the pendingDone channel.
func (r *Runtime) untrackPending(id string) {
	r.pendingMu.Lock()
	delete(r.pending, id)
	if len(r.pending) == 0 && r.pendingDone != nil {
		close(r.pendingDone)
		r.pendingDone = nil
	}
	r.pendingMu.Unlock()
}

// WaitForDelegations blocks until all async delegations started in this
// runtime have finished. It is safe to call when no delegations are pending.
func (r *Runtime) WaitForDelegations() {
	r.pendingMu.Lock()
	done := r.pendingDone
	r.pendingMu.Unlock()
	if done == nil {
		return
	}
	<-done
}

// Pool exposes the bounded pool so adapters can build tools (e.g. DelegateTool)
// that need to acquire/release handles directly.
func (r *Runtime) Pool() *BoundedAgentPool { return r.pool }
