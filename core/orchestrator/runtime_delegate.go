// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"strings"
	"text/template"

	"github.com/pijalu/goa/prompts"
)

func statsPayload(s AgentStatsSnapshot) map[string]any {
	return map[string]any{
		"turns":             s.Turns,
		"tokens_in":         s.TokensIn,
		"tokens_out":        s.TokensOut,
		"cache_read":        s.CacheRead,
		"cache_creation":    s.CacheCreation,
		"tool_calls":        s.ToolCalls,
		"status":            string(s.Status),
		"context_estimate":  s.ContextEstimate,
		"context_projected": s.ContextProjected,
		"context_max":       s.ContextMax,
		"context_auto_max":  s.ContextAutoMax,
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
	tpl, err := prompts.LoadOrchestratePrompt(name, r.promptDir)
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

// SetPromptDir configures the directory where user-provided orchestrator
// prompt overrides are loaded from. Empty uses the embedded prompts.
func (r *Runtime) SetPromptDir(dir string) {
	r.promptDir = dir
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

// SetPlanID records the plan id associated with the run. It is emitted in the
// run_started event so the run snapshot can later recover it. Must be called
// before Run.
func (r *Runtime) SetPlanID(id string) { r.planID = id }

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
