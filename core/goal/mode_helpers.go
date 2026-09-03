// SPDX-License-Identifier: GPL-3.0-or-later
package goal

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

func (m *GoalMode) queuedGoalNames() map[string]bool {
	taken := make(map[string]bool)
	if m.namePool != nil {
		for name := range m.namePool.UsedNames() {
			taken[name] = true
		}
	}
	if m.state != nil && m.state.name != "" {
		taken[m.state.name] = true
	}
	return taken
}
func (m *GoalMode) toSnapshot(state *goalStage) GoalSnapshot {
	now := time.Now()
	wall := LiveWallClockMs(*state, now)
	return GoalSnapshot{GoalID: state.goalID, Name: state.name, ManagedBy: state.managedBy, Kind: state.kind, Objective: state.objective, CompletionCriterion: state.completionCriterion, VerifyCommand: state.verifyCommand, FreshContext: state.freshContext, Team: state.team, PauseAfterComplete: state.pauseAfterComplete, Handoff: state.handoff, Todos: append([]GoalTodoItem(nil), state.todos...), Status: state.status, TurnsUsed: state.turnsUsed, TokensUsed: state.tokensUsed, WallClockMs: wall, Budget: ComputeBudgetReport(state.budgetLimits, state.turnsUsed, state.tokensUsed, wall), TerminalReason: state.terminalReason, TerminalExpectation: state.terminalExpectation}
}
func (m *GoalMode) requireState() (*goalStage, error) {
	if m.state == nil {
		return nil, errors.New("no active goal")
	}
	return m.state, nil
}

type persistOptions struct {
	Silent bool
	Change *GoalChange
}

func (m *GoalMode) persistState(state *goalStage, opts persistOptions) {
	state.updatedAt = time.Now()
	m.state = state
	if opts.Silent {
		return
	}
	var snap *GoalSnapshot
	if state != nil {
		s := m.toSnapshot(state)
		snap = &s
	}
	m.emitGoalUpdated(snap, opts.Change)
}
func (m *GoalMode) emitGoalUpdated(snapshot *GoalSnapshot, change *GoalChange) {
	m.publisher.Publish(snapshot, change)
}
func (m *GoalMode) statsOf(state *goalStage) *GoalChangeStats {
	return &GoalChangeStats{TurnsUsed: state.turnsUsed, TokensUsed: state.tokensUsed, WallClockMs: LiveWallClockMs(*state, time.Now())}
}
func normalizeCompletionCriterion(value *string) *string { return NormalizeOptionalText(value) }
func NormalizeOptionalText(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
func NormalizeHandover(value *string) (*string, error) {
	v := NormalizeOptionalText(value)
	if v != nil && len(*v) > MaxHandoverLength {
		return nil, fmt.Errorf("goal handover cannot exceed %d characters", MaxHandoverLength)
	}
	return v, nil
}
func ptrStatus(s GoalStatus) *GoalStatus { return &s }
func ptrString(s string) *string         { return &s }
func mergeMaps(a, b map[string]any) map[string]any {
	out := make(map[string]any, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}
