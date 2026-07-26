// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/pijalu/goa/internal"
)

const maxObjectiveLength = 4000

func generateGoalID() string {
	return internal.PrefixedHexID("goal", 8)
}

// EventPublisher emits goal state changes to consumers such as the TUI.
type EventPublisher interface {
	Publish(snapshot *GoalSnapshot, change *GoalChange)
}

// GoalMode is the single durable owner of the current goal.
//
// Concurrency: state is read by command goroutines (status/pause/resume) and
// mutated by the background GoalDriver goroutine (IncrementTurn,
// RecordTokenUsage, PauseActiveGoal, …). mu guards every access to m.state
// (and the namePool reference). Exported lifecycle methods take the lock for
// their whole body; the unexported helpers they call (persistState, toSnapshot,
// applyStatus, …) assume the lock is already held and never re-acquire it, so
// there is no re-entrant deadlock.
type GoalMode struct {
	mu         sync.RWMutex
	state      *goalStage
	store      EventStore
	telemetry  Telemetry
	publisher  EventPublisher
	reminderFn ReminderFunc
	namePool   NamePool
	doneGate   DoneGateMode
	// verifier runs recorded verify commands at completion time (nil =
	// command verification unavailable). verifyCommandsEnabled gates it.
	verifier              CommandVerifier
	verifyCommandsEnabled bool
	// judge independently audits confirmed completions (nil = no judge).
	judge GoalJudge
	// maxVerifyFailures caps the consecutive machine-verification failure
	// streak before the goal is auto-blocked; <= 0 means no cap.
	maxVerifyFailures int
	// defaultTurnBudget, when > 0, gives every newly created goal a hard
	// turn ceiling unless one is set explicitly later.
	defaultTurnBudget int
}

// NamePool returns the set of friendly names already in use, so newly created
// goals can pick a unique alias. It is optional; when nil, only the active
// goal's own name (if any) is considered taken.
type NamePool interface {
	UsedNames() map[string]bool
}

// NewGoalMode creates a goal mode engine bound to the given dependencies.
func NewGoalMode(store EventStore, publisher EventPublisher, telemetry Telemetry, reminderFn ReminderFunc) *GoalMode {
	if telemetry == nil {
		telemetry = noopTelemetry{}
	}
	if publisher == nil {
		publisher = noopPublisher{}
	}
	return &GoalMode{
		store:                 store,
		publisher:             publisher,
		telemetry:             telemetry,
		reminderFn:            reminderFn,
		doneGate:              DefaultDoneGate,
		verifyCommandsEnabled: true,
		maxVerifyFailures:     DefaultMaxVerifyFailures,
	}
}

// SetNamePool wires a source of already-used friendly names (typically the
// goal queue) so CreateGoal can generate a collision-free alias.
func (m *GoalMode) SetNamePool(pool NamePool) { m.namePool = pool }

// SetDoneGate configures how strictly model-initiated completion is checked
// before the goal may close (see DoneGateMode). It only affects future
// RequestComplete calls.
func (m *GoalMode) SetDoneGate(gate DoneGateMode) { m.doneGate = gate }

// SetVerifier wires the command executor used for machine verification of
// recorded verify commands. enabled=false disables command verification
// without removing the executor.
func (m *GoalMode) SetVerifier(v CommandVerifier, enabled bool) {
	m.verifier = v
	m.verifyCommandsEnabled = enabled
}

// SetJudge wires the independent completion judge (nil disables judging).
func (m *GoalMode) SetJudge(j GoalJudge) { m.judge = j }

// SetMaxVerifyFailures sets the consecutive-failure cap that auto-blocks a
// goal for user review. Values <= 0 disable escalation.
func (m *GoalMode) SetMaxVerifyFailures(n int) { m.maxVerifyFailures = n }

// SetDefaultTurnBudget gives newly created goals a hard turn ceiling when
// n > 0; 0 means no implicit budget.
func (m *GoalMode) SetDefaultTurnBudget(n int) { m.defaultTurnBudget = n }

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

// CreateGoal creates a new active goal.
func (m *GoalMode) CreateGoal(input CreateGoalInput, actor GoalActor) (GoalSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	objective := strings.TrimSpace(input.Objective)
	if objective == "" {
		return GoalSnapshot{}, errors.New("goal objective cannot be empty")
	}
	if len(objective) > maxObjectiveLength {
		return GoalSnapshot{}, fmt.Errorf("goal objective cannot exceed %d characters", maxObjectiveLength)
	}

	if m.state != nil {
		if !input.Replace {
			return GoalSnapshot{}, errors.New("a goal already exists; use replace to start a new one")
		}
		m.clearInternal(GoalActorSystem, emitOption{Emit: true, Track: true})
	}

	completionCriterion := normalizeCompletionCriterion(input.CompletionCriterion)
	verifyCommand := NormalizeOptionalText(input.VerifyCommand)
	handoff := NormalizeOptionalText(input.Handoff)
	now := time.Now()
	nowMs := now.UnixMilli()
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = internal.FriendlyNameUnique(m.queuedGoalNames())
	}
	budgetLimits := GoalBudgetLimits{}
	if m.defaultTurnBudget > 0 {
		budget := m.defaultTurnBudget
		budgetLimits.TurnBudget = &budget
	}
	state := &goalStage{
		goalID:              generateGoalID(),
		name:                name,
		managedBy:           input.ManagedBy,
		objective:           objective,
		completionCriterion: completionCriterion,
		verifyCommand:       verifyCommand,
		freshContext:        input.FreshContext,
		handoff:             handoff,
		status:              GoalActive,
		turnsUsed:           0,
		tokensUsed:          0,
		wallClockMs:         0,
		wallClockResumedAt:  &nowMs,
		updatedAt:           now,
		budgetLimits:        budgetLimits,
	}

	m.persistState(state, persistOptions{})
	m.appendGoalUpdateRecord(GoalEventRecord{
		Type:                GoalEventCreate,
		Timestamp:           now,
		GoalID:              &state.goalID,
		Name:                &state.name,
		ManagedBy:           &state.managedBy,
		Objective:           &state.objective,
		CompletionCriterion: state.completionCriterion,
		VerifyCommand:       state.verifyCommand,
		Handoff:             state.handoff,
		FreshContext:        &state.freshContext,
	})
	m.telemetry.Track(TelemetryGoalCreated, map[string]any{
		"actor":   string(actor),
		"replace": input.Replace,
	})
	return m.toSnapshot(state), nil
}

// PauseGoal pauses an active goal.
func (m *GoalMode) PauseGoal(input GoalReasonInput, actor GoalActor) (GoalSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.requireState()
	if err != nil {
		return GoalSnapshot{}, err
	}
	if state.status == GoalPaused {
		return m.toSnapshot(state), nil
	}
	if state.status != GoalActive {
		return GoalSnapshot{}, fmt.Errorf("cannot pause a goal in status %q", state.status)
	}
	m.applyStatus(state, GoalPaused)
	state.terminalReason = input.Reason
	state.terminalExpectation = input.Expectation
	m.persistState(state, persistOptions{Change: &GoalChange{
		Kind:        GoalChangeLifecycle,
		Status:      ptrStatus(GoalPaused),
		Reason:      input.Reason,
		Expectation: input.Expectation,
		Actor:       &actor,
	}})
	m.appendStatusUpdate(state, actor, input)
	return m.toSnapshot(state), nil
}

// PauseActiveGoal parks an active goal without throwing if it already stopped.
func (m *GoalMode) PauseActiveGoal(input GoalReasonInput, actor GoalActor) (*GoalSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state
	if state == nil || state.status != GoalActive {
		return nil, nil
	}
	m.applyStatus(state, GoalPaused)
	state.terminalReason = input.Reason
	state.terminalExpectation = input.Expectation
	m.persistState(state, persistOptions{Change: &GoalChange{
		Kind:        GoalChangeLifecycle,
		Status:      ptrStatus(GoalPaused),
		Reason:      input.Reason,
		Expectation: input.Expectation,
		Actor:       &actor,
	}})
	m.appendStatusUpdate(state, actor, input)
	snap := m.toSnapshot(state)
	return &snap, nil
}

// ResumeGoal resumes a paused or blocked goal.
func (m *GoalMode) ResumeGoal(input GoalReasonInput, actor GoalActor) (GoalSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.requireState()
	if err != nil {
		return GoalSnapshot{}, err
	}
	if state.status == GoalActive {
		return m.toSnapshot(state), nil
	}
	if state.status != GoalPaused && state.status != GoalBlocked {
		return GoalSnapshot{}, fmt.Errorf("cannot resume a goal in status %q", state.status)
	}
	state.terminalReason = nil
	state.terminalExpectation = nil
	m.applyStatus(state, GoalActive)
	m.persistState(state, persistOptions{Change: &GoalChange{
		Kind:   GoalChangeLifecycle,
		Status: ptrStatus(GoalActive),
		Reason: input.Reason,
		Actor:  &actor,
	}})
	m.appendStatusUpdate(state, actor, input)
	return m.toSnapshot(state), nil
}

// CancelGoal discards the current goal entirely.
func (m *GoalMode) CancelGoal(actor GoalActor) (GoalSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cancelGoalLocked(actor)
}

// cancelGoalLocked discards the current goal; caller must hold m.mu.
func (m *GoalMode) cancelGoalLocked(actor GoalActor) (GoalSnapshot, error) {
	state, err := m.requireState()
	if err != nil {
		return GoalSnapshot{}, err
	}
	snapshot := m.toSnapshot(state)
	m.clearInternal(actor, emitOption{Emit: true, Track: true})
	if actor == GoalActorUser && m.reminderFn != nil {
		m.reminderFn(BuildCancellationReminder())
	}
	return snapshot, nil
}

// CancelGoalByID discards the current goal only if its ID matches the given
// ID. It returns an error if there is no active goal or if the IDs differ.
func (m *GoalMode) CancelGoalByID(goalID string, actor GoalActor) (GoalSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.requireState()
	if err != nil {
		return GoalSnapshot{}, err
	}
	if state.goalID != goalID {
		return GoalSnapshot{}, fmt.Errorf("active goal ID mismatch")
	}
	return m.cancelGoalLocked(actor)
}

// MarkBlocked marks the goal blocked.
func (m *GoalMode) MarkBlocked(input GoalReasonInput, actor GoalActor) (*GoalSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.markBlockedLocked(input, actor)
}

// markBlockedLocked blocks the active goal; caller must hold m.mu.
func (m *GoalMode) markBlockedLocked(input GoalReasonInput, actor GoalActor) (*GoalSnapshot, error) {
	state := m.state
	if state == nil || state.status != GoalActive {
		return nil, nil
	}
	m.applyStatus(state, GoalBlocked)
	state.terminalReason = input.Reason
	state.terminalExpectation = input.Expectation
	m.persistState(state, persistOptions{Change: &GoalChange{
		Kind:        GoalChangeLifecycle,
		Status:      ptrStatus(GoalBlocked),
		Reason:      input.Reason,
		Expectation: input.Expectation,
		Actor:       &actor,
	}})
	m.appendStatusUpdate(state, actor, input)
	snap := m.toSnapshot(state)
	return &snap, nil
}

// MarkComplete announces completion and clears the record. It performs no
// done-gate check: callers that want the gate (the model-facing tool) must
// use RequestComplete instead.
func (m *GoalMode) MarkComplete(input GoalReasonInput, actor GoalActor) (*GoalSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.markCompleteLocked(input, actor)
}

// markCompleteLocked completes and clears the active goal; caller holds m.mu.
func (m *GoalMode) markCompleteLocked(input GoalReasonInput, actor GoalActor) (*GoalSnapshot, error) {
	state := m.state
	if state == nil || state.status != GoalActive {
		return nil, nil
	}
	m.applyStatus(state, GoalDone)
	state.terminalReason = input.Reason
	snapshot := m.toSnapshot(state)
	m.appendStatusUpdate(state, actor, input)
	m.emitGoalUpdated(&snapshot, &GoalChange{
		Kind:   GoalChangeCompletion,
		Status: ptrStatus(GoalDone),
		Reason: input.Reason,
		Stats:  m.statsOf(state),
		Actor:  &actor,
	})
	m.clearInternal(actor, emitOption{Emit: true, Track: false})
	return &snapshot, nil
}

// PauseOnInterrupt parks an active goal when its live turn is aborted.
func (m *GoalMode) PauseOnInterrupt(reason string) (*GoalSnapshot, error) {
	return m.PauseActiveGoal(GoalReasonInput{Reason: &reason}, GoalActorUser)
}

// CleanupExpired clears the active goal if it is in a terminal state and has
// not been updated for at least retentionDays. A retentionDays value of 0 or
// negative means "keep forever".
func (m *GoalMode) CleanupExpired(retentionDays int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if retentionDays <= 0 {
		return nil
	}
	state := m.state
	if state == nil || state.status == GoalActive {
		return nil
	}
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	if state.updatedAt.Before(cutoff) {
		m.clearInternal(GoalActorRuntime, emitOption{Emit: true, Track: true})
	}
	return nil
}

// AddGoalTodo appends a task to the current goal's managed todo list and
// persists it. The list is surfaced to the model each turn so a multi-step
// goal self-tracks. Returns the created item.
func (m *GoalMode) AddGoalTodo(title string, actor GoalActor) (GoalTodoItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.requireState()
	if err != nil {
		return GoalTodoItem{}, err
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return GoalTodoItem{}, errors.New("todo title cannot be empty")
	}
	item := GoalTodoItem{ID: nextTodoID(state.todos), Title: title, Status: TodoPending}
	state.todos = append(state.todos, item)
	m.persistTodosLocked(state, actor)
	return item, nil
}

// UpdateGoalTodo sets the status of an existing todo item (pending |
// in_progress | done) and persists the list.
func (m *GoalMode) UpdateGoalTodo(id, status string, actor GoalActor) (GoalSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.requireState()
	if err != nil {
		return GoalSnapshot{}, err
	}
	normalized := normalizeTodoStatus(status)
	for i := range state.todos {
		if state.todos[i].ID == id {
			state.todos[i].Status = normalized
			m.persistTodosLocked(state, actor)
			return m.toSnapshot(state), nil
		}
	}
	return GoalSnapshot{}, fmt.Errorf("todo %q not found", id)
}

// persistTodosLocked records the todo list to the event log and publishes a
// silent state update. Caller must hold the lock.
func (m *GoalMode) persistTodosLocked(state *goalStage, actor GoalActor) {
	m.persistState(state, persistOptions{Silent: true})
	a := string(actor)
	m.appendGoalUpdateRecord(GoalEventRecord{
		Type:      GoalEventUpdate,
		Timestamp: time.Now(),
		Todos:     append([]GoalTodoItem(nil), state.todos...),
		Actor:     &a,
	})
}

// RunVerifyCommand executes the current goal's recorded verify command on
// demand (the /goal:verify audit surface). It returns the command output and
// whether it passed. The execution runs without holding the mode lock.
func (m *GoalMode) RunVerifyCommand(ctx context.Context) (string, bool, error) {
	m.mu.RLock()
	state := m.state
	verifier := m.verifier
	enabled := m.verifyCommandsEnabled
	var command *string
	if state != nil {
		command = state.verifyCommand
	}
	m.mu.RUnlock()

	if state == nil {
		return "", false, errors.New("no current goal")
	}
	if command == nil {
		return "", false, errors.New("no verify command recorded for this goal")
	}
	if verifier == nil || !enabled {
		return "", false, errors.New("command verification is disabled")
	}
	output, ok := verifier.Verify(ctx, *command)
	return output, ok, nil
}

// EventLog returns the raw goal event records from the store (the
// /goal:log audit surface). Returns nil when no store is wired.
func (m *GoalMode) EventLog() ([]GoalEventRecord, error) {
	if m.store == nil {
		return nil, nil
	}
	return m.store.Replay()
}

// GetGoal returns the current goal snapshot.
func (m *GoalMode) GetGoal() GoalToolResult {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.state == nil {
		return GoalToolResult{Goal: nil}
	}
	snap := m.toSnapshot(m.state)
	return GoalToolResult{Goal: &snap}
}

// GetActiveGoal returns the snapshot only when status is active.
func (m *GoalMode) GetActiveGoal() *GoalSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.state == nil || m.state.status != GoalActive {
		return nil
	}
	snap := m.toSnapshot(m.state)
	return &snap
}

// RecordTokenUsage accrues token count while active.
func (m *GoalMode) RecordTokenUsage(delta int) (*GoalSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state
	if state == nil || state.status != GoalActive {
		return nil, nil
	}
	if delta < 0 {
		delta = 0
	}
	state.tokensUsed += delta
	m.persistState(state, persistOptions{Silent: true})
	m.appendGoalUpdateRecord(GoalEventRecord{
		Type:       GoalEventUpdate,
		Timestamp:  time.Now(),
		TokensUsed: &state.tokensUsed,
	})
	snap := m.toSnapshot(state)
	return &snap, nil
}

// IncrementTurn increments the continuation turn counter while active.
func (m *GoalMode) IncrementTurn() (*GoalSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.state
	if state == nil || state.status != GoalActive {
		return nil, nil
	}
	state.turnsUsed++
	m.persistState(state, persistOptions{})
	m.appendGoalUpdateRecord(GoalEventRecord{
		Type:      GoalEventUpdate,
		Timestamp: time.Now(),
		TurnsUsed: &state.turnsUsed,
	})
	m.telemetry.Track(TelemetryGoalContinued, map[string]any{
		"turns_used": state.turnsUsed,
	})
	snap := m.toSnapshot(state)
	return &snap, nil
}

// SetBudgetLimits updates budget limits (merged, not replaced).
func (m *GoalMode) SetBudgetLimits(limits GoalBudgetLimits, actor GoalActor) (GoalSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.requireState()
	if err != nil {
		return GoalSnapshot{}, err
	}
	if limits.TokenBudget != nil {
		state.budgetLimits.TokenBudget = limits.TokenBudget
	}
	if limits.TurnBudget != nil {
		state.budgetLimits.TurnBudget = limits.TurnBudget
	}
	if limits.WallClockBudgetMs != nil {
		state.budgetLimits.WallClockBudgetMs = limits.WallClockBudgetMs
	}
	m.persistState(state, persistOptions{})
	m.appendGoalUpdateRecord(GoalEventRecord{
		Type:         GoalEventUpdate,
		Timestamp:    time.Now(),
		BudgetLimits: &state.budgetLimits,
	})
	m.telemetry.Track(TelemetryGoalBudgetSet, map[string]any{
		"actor": string(actor),
	})
	return m.toSnapshot(state), nil
}

func (m *GoalMode) applyStatus(state *goalStage, status GoalStatus) {
	now := time.Now().UnixMilli()
	if state.status == GoalActive && state.wallClockResumedAt != nil {
		elapsed := now - *state.wallClockResumedAt
		if elapsed > 0 {
			state.wallClockMs += elapsed
		}
		state.wallClockResumedAt = nil
	}
	if status == GoalActive {
		state.wallClockResumedAt = &now
	} else {
		// Any transition out of active abandons a pending done-gate
		// verification: a pause/blocked after a challenge must not let the
		// next complete request skip the challenge. The machine-verification
		// failure streak resets with it.
		state.pendingVerification = false
		state.verifyFailures = 0
	}
	state.status = status
}

func (m *GoalMode) clearInternal(actor GoalActor, opts ...emitOption) {
	opt := emitOption{Emit: true, Track: true}
	if len(opts) > 0 {
		opt = opts[0]
	}
	if m.state == nil {
		return
	}
	m.state = nil
	if opt.Emit {
		m.emitGoalUpdated(nil, nil)
	}
	m.appendGoalUpdateRecord(GoalEventRecord{
		Type:      GoalEventClear,
		Timestamp: time.Now(),
	})
	if opt.Track {
		m.telemetry.Track(TelemetryGoalCleared, map[string]any{
			"actor": string(actor),
		})
	}
}

type emitOption struct {
	Emit  bool
	Track bool
}

func (m *GoalMode) appendStatusUpdate(state *goalStage, actor GoalActor, input GoalReasonInput) {
	now := time.Now()
	wallClock := LiveWallClockMs(*state, now)
	m.appendGoalUpdateRecord(GoalEventRecord{
		Type:        GoalEventUpdate,
		Timestamp:   now,
		Status:      ptrString(string(state.status)),
		Reason:      input.Reason,
		Expectation: input.Expectation,
		Actor:       ptrString(string(actor)),
		WallClockMs: &wallClock,
	})
	m.telemetry.Track(TelemetryGoalStatusChanged, mergeMaps(map[string]any{
		"actor":         string(actor),
		"status":        string(state.status),
		"turns_used":    state.turnsUsed,
		"tokens_used":   state.tokensUsed,
		"wall_clock_ms": wallClock,
	}, BudgetTelemetryProperties(state.budgetLimits)))
}

func (m *GoalMode) appendGoalUpdateRecord(record GoalEventRecord) {
	if m.store == nil {
		return
	}
	_ = m.store.Append(record)
}

// queuedGoalNames returns the set of friendly names to avoid when generating a
// new one: any names already used by queued goals (via namePool) plus this
// mode's current active-goal name.
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
	return GoalSnapshot{
		GoalID:              state.goalID,
		Name:                state.name,
		ManagedBy:           state.managedBy,
		Objective:           state.objective,
		CompletionCriterion: state.completionCriterion,
		VerifyCommand:       state.verifyCommand,
		FreshContext:        state.freshContext,
		Handoff:             state.handoff,
		Todos:               append([]GoalTodoItem(nil), state.todos...),
		Status:              state.status,
		TurnsUsed:           state.turnsUsed,
		TokensUsed:          state.tokensUsed,
		WallClockMs:         LiveWallClockMs(*state, now),
		Budget:              ComputeBudgetReport(state.budgetLimits, state.turnsUsed, state.tokensUsed, LiveWallClockMs(*state, now)),
		TerminalReason:      state.terminalReason,
		TerminalExpectation: state.terminalExpectation,
	}
}

// requireState returns the current goal stage or an error when no goal is
// active. It must never panic: the callers (PauseGoal/ResumeGoal/CancelGoal/
// SetBudgetLimits) are reachable from user commands, so a missing goal must be
// reported as an error rather than crashing the process. See CORE-BUG-10.
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
	return &GoalChangeStats{
		TurnsUsed:   state.turnsUsed,
		TokensUsed:  state.tokensUsed,
		WallClockMs: LiveWallClockMs(*state, time.Now()),
	}
}

func normalizeCompletionCriterion(value *string) *string {
	return NormalizeOptionalText(value)
}

// NormalizeOptionalText trims a free-text pointer field and maps blank to nil
// so stored JSON omits it.
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

func ptrStatus(s GoalStatus) *GoalStatus {
	return &s
}

func ptrString(s string) *string {
	return &s
}

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

type noopTelemetry struct{}

func (noopTelemetry) Track(string, map[string]any) {}

type noopPublisher struct{}

func (noopPublisher) Publish(*GoalSnapshot, *GoalChange) {}
