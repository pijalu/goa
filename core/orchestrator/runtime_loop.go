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
)

func (r *Runtime) runOrchestratorLoop(ctx context.Context, objective string) error {
	r.loopMu.Lock()
	r.loopActive = true
	r.loopMu.Unlock()
	defer func() {
		r.loopMu.Lock()
		r.loopActive = false
		r.pendingUser = false
		r.pendingQuestion = ""
		r.lastAction = actionNone
		r.loopMu.Unlock()
	}()

	prompt := r.renderRolePrompt("orchestrator", objective)
	if prompt == "" {
		prompt = objective
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		// Reset the action recorder for this turn.
		r.loopMu.Lock()
		r.lastAction = actionNone
		r.loopMu.Unlock()

		if err := r.acquireAndRun(ctx, "orchestrator", prompt); err != nil {
			return err
		}

		action, _ := r.loopAction()
		if action == actionAskUser {
			if err := r.waitForUserAnswer(ctx); err != nil {
				return err
			}
			prompt = r.buildSpecialistResultsPrompt()
			continue
		}

		if action == actionDelegate || action == actionRework {
			// Wait for all async specialists to finish before the next turn.
			r.WaitForDelegations()
			prompt = r.buildSpecialistResultsPrompt()
			continue
		}

		// No tools called: the orchestrator's output is the final answer.
		return nil
	}
}

// loopAction returns the highest-priority action recorded during the last
// orchestrator turn.
func (r *Runtime) loopAction() (toolAction, string) {
	r.loopMu.Lock()
	defer r.loopMu.Unlock()
	return r.lastAction, r.pendingQuestion
}

// waitForUserAnswer blocks until the user answers via SteerOrchestrator or the
// run context is cancelled. It emits a loop_state event so observers know the
// orchestrator is paused.
func (r *Runtime) waitForUserAnswer(ctx context.Context) error {
	r.loopMu.Lock()
	r.pendingUser = true
	if r.resumeCh == nil {
		r.resumeCh = make(chan struct{})
	}
	ch := r.resumeCh

	// Race-safety: an answer may already be buffered if SteerOrchestrator was
	// called between AskUser (which created the channel) and this wait. In
	// that case the channel was closed and set to nil, and a new one would be
	// created above; consume the buffered answer and resume immediately.
	r.orchSteerMu.Lock()
	if len(r.orchSteer) > 0 {
		r.orchSteer = r.orchSteer[:0]
		r.orchSteerMu.Unlock()
		r.pendingUser = false
		r.loopMu.Unlock()
		return nil
	}
	r.orchSteerMu.Unlock()
	r.loopMu.Unlock()

	r.emit(Event{Type: EventLoopState, Role: "orchestrator",
		Payload: map[string]any{"state": "paused_for_user"}})

	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// buildSpecialistResultsPrompt builds the prompt for the next orchestrator turn
// after specialist outputs are available. Hub orchestrator agents are created
// fresh every turn (no conversation history is carried), so the prompt must be
// self-contained: it restates the objective alongside the specialist outputs
// and the tool guidance. Without the objective a fresh orchestrator loses the
// plot after the first delegation and its confused reply is mistaken for the
// final answer (e2e T1: the reviewer delegation was silently skipped).
func (r *Runtime) buildSpecialistResultsPrompt() string {
	specialists := r.collectSpecialistOutputs()
	section := "No specialist outputs yet."
	if specialists != "" {
		// Keep the exact "Specialist outputs:" marker — loop drivers (and
		// their tests) branch on it to detect synthesis turns.
		section = "Specialist outputs:\n" + specialists
	}
	if p := r.renderPrompt("hub_continuation", map[string]any{
		"Objective":   r.objective,
		"Specialists": section,
	}); p != "" {
		return p
	}
	// Inline fallback when the template is unavailable: keep the prompt
	// self-contained (objective + outputs + guidance).
	return "Current objective: " + r.objective + "\n\n" + section +
		"\n\nContinue toward the objective: delegate remaining sub-tasks, rework unsatisfactory output, ask the user, or provide the final answer."
}

// AskUser records that the orchestrator asked the user a question. The loop
// will pause after the current turn and wait for the user to answer.
func (r *Runtime) AskUser(question string) {
	r.loopMu.Lock()
	r.pendingQuestion = question
	r.pendingUser = true
	r.lastAction = actionAskUser
	if r.resumeCh == nil {
		r.resumeCh = make(chan struct{})
	}
	r.loopMu.Unlock()
	r.emit(Event{Type: EventAskUser, Role: "orchestrator",
		Payload: map[string]any{"question": question}})
}

// ReworkAsync starts a specialist revision in the background and returns a
// placeholder. It behaves like DelegateAsync but records the action as a
// rework so the loop can report it accurately.
func (r *Runtime) ReworkAsync(ctx context.Context, role, feedback string, opts AcquireOptions) (string, error) {
	task := fmt.Sprintf("Revise your previous output based on this feedback: %s", feedback)
	out, err := r.DelegateAsync(ctx, role, task, opts)
	if err != nil {
		return "", err
	}
	r.loopMu.Lock()
	r.lastAction = actionRework
	r.loopMu.Unlock()
	return out, nil
}

// SetLastAction records the highest-priority action taken in the current turn.
// Tools call this so the loop knows what to do after the turn ends.
func (r *Runtime) SetLastAction(a toolAction) {
	r.loopMu.Lock()
	defer r.loopMu.Unlock()
	if a > r.lastAction {
		r.lastAction = a
	}
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
// releases the handle. Shared by driveOne and the hub conversation loop so both
// follow the identical lifecycle (goal-token accounting, outcome events).
// For the orchestrator role, pending user steering is drained into the handle
// before the turn starts so answers survive across loop iterations.
func (r *Runtime) acquireAndRun(ctx context.Context, role, renderedPrompt string) error {
	h, err := r.pool.Acquire(ctx, role, AcquireOptions{})
	if err != nil {
		r.emit(Event{Type: EventAgentFinished, Role: role,
			Payload: map[string]any{"outcome": "acquire_failed", "error": err.Error()}})
		return err
	}
	defer r.pool.Release(h)

	// For the orchestrator role, drain any pending user steering that arrived
	// while no handle was live (e.g., during an ask_user pause or between
	// turns). This keeps the conversation alive across loop iterations.
	if role == "orchestrator" {
		r.orchSteerMu.Lock()
		for _, text := range r.orchSteer {
			h.Steer(text)
			r.emit(Event{Type: EventAgentSteered, AgentID: h.ID, Role: h.Role,
				Payload: map[string]any{"from": "user", "text": text}})
		}
		r.orchSteer = nil
		r.orchSteerMu.Unlock()
	}

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
