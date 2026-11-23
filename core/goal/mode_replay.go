// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

// Replay loads all events from the store and rebuilds state.
func (m *GoalMode) Replay() error {
	records, err := m.store.Replay()
	if err != nil {
		return err
	}
	for _, record := range records {
		switch record.Type {
		case GoalEventCreate:
			m.RestoreCreate(record)
		case GoalEventUpdate:
			m.RestoreUpdate(record)
		case GoalEventClear:
			m.RestoreClear(record)
		}
	}
	m.NormalizeAfterReplay()
	return nil
}

// RestoreCreate rebuilds initial state from a goal.create record.
func (m *GoalMode) RestoreCreate(record GoalEventRecord) {
	state := goalStage{
		status:       GoalActive,
		turnsUsed:    0,
		tokensUsed:   0,
		wallClockMs:  0,
		budgetLimits: GoalBudgetLimits{},
		updatedAt:    record.Timestamp,
	}
	if record.GoalID != nil {
		state.goalID = *record.GoalID
	}
	if record.Kind != nil && *record.Kind == GoalKindUnblock {
		state.kind = GoalKindUnblock
	}
	if record.Name != nil {
		state.name = *record.Name
	}
	if record.ManagedBy != nil {
		state.managedBy = *record.ManagedBy
	}
	if record.Objective != nil {
		state.objective = *record.Objective
	}
	if record.CompletionCriterion != nil {
		state.completionCriterion = normalizeCompletionCriterion(record.CompletionCriterion)
	}
	if record.VerifyCommand != nil {
		state.verifyCommand = NormalizeOptionalText(record.VerifyCommand)
	}
	if record.Handoff != nil {
		state.handoff = NormalizeOptionalText(record.Handoff)
	}
	if record.FreshContext != nil {
		state.freshContext = *record.FreshContext
	}
	if record.Team != nil {
		state.team = *record.Team
	}
	m.state = &state
}

// RestoreUpdate applies a partial patch from a goal.update record.
func (m *GoalMode) RestoreUpdate(record GoalEventRecord) {
	state := m.state
	if state == nil {
		return
	}
	if record.Status != nil {
		state.status = GoalStatus(*record.Status)
		state.wallClockResumedAt = nil
		if state.status != GoalActive {
			state.terminalReason = record.Reason
			state.terminalExpectation = record.Expectation
		} else {
			state.terminalReason = nil
			state.terminalExpectation = nil
		}
	}
	if record.TurnsUsed != nil {
		state.turnsUsed = *record.TurnsUsed
	}
	if record.TokensUsed != nil {
		state.tokensUsed = *record.TokensUsed
	}
	if record.WallClockMs != nil {
		state.wallClockMs = *record.WallClockMs
		state.wallClockResumedAt = nil
	}
	if record.BudgetLimits != nil {
		state.budgetLimits = *record.BudgetLimits
	}
	if record.PauseAfterComplete != nil {
		state.pauseAfterComplete = *record.PauseAfterComplete
	}
	if record.VerifyCommand != nil {
		state.verifyCommand = NormalizeOptionalText(record.VerifyCommand)
		state.verifyFailures = 0
	}
	if record.Todos != nil {
		state.todos = append([]GoalTodoItem(nil), record.Todos...)
	}
}

// RestoreClear clears internal state.
func (m *GoalMode) RestoreClear(_ GoalEventRecord) {
	m.state = nil
}

// NormalizeAfterReplay demotes active to paused on session resume and removes
// stray complete statuses.
func (m *GoalMode) NormalizeAfterReplay() {
	state := m.state
	if state == nil {
		return
	}
	state.wallClockResumedAt = nil

	if state.status == GoalDone {
		m.clearInternal(GoalActorRuntime, emitOption{Emit: false, Track: false})
		return
	}

	if state.status == GoalActive {
		reason := "Paused after agent resume"
		m.applyStatus(state, GoalPaused)
		state.terminalReason = &reason
		m.persistState(state, persistOptions{Silent: true})
		m.appendStatusUpdate(state, GoalActorRuntime, GoalReasonInput{Reason: &reason})
	}
}
