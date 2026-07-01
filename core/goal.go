// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pijalu/goa/core/goal"
)

// GoalStatus is kept for backward compatibility.
type GoalStatus = goal.GoalStatus

const (
	GoalActive  = goal.GoalActive
	GoalPaused  = goal.GoalPaused
	GoalBlocked = goal.GoalBlocked
	GoalDone    = goal.GoalDone
)

// Goal is a backward-compatible view of a goal.
type Goal struct {
	ID        string      `json:"id"`
	Objective string      `json:"objective"`
	Status    GoalStatus  `json:"status"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
	Events    []GoalEvent `json:"events,omitempty"`
}

// GoalEvent is a backward-compatible event record.
type GoalEvent struct {
	Timestamp time.Time  `json:"timestamp"`
	Status    GoalStatus `json:"status"`
	Note      string     `json:"note,omitempty"`
}

// GoalDependencies holds the dependencies needed by GoalManager.
type GoalDependencies struct {
	Publisher  goal.EventPublisher
	Telemetry  goal.Telemetry
	ReminderFn goal.ReminderFunc
}

// GoalManager persists and manages goals.
// It wraps a single-goal GoalMode for the active goal while preserving
// a session history of past goals for inspection.
type GoalManager struct {
	mu        sync.Mutex
	Mode      *goal.GoalMode
	Queue     *GoalQueueStore
	dir       string
	history   []Goal
	replayErr error // captured during construction; see ReplayError
}

// NewGoalManager creates a goal manager that persists to dir/goals/. It replays
// any persisted goal events best-effort during construction so existing callers
// keep working; the replay error (if any) is captured and exposed via
// ReplayError, and also forwarded to the injected Telemetry. Callers that care
// about correctness should call Restore explicitly and handle the returned
// error. See CORE-BUG-5.
func NewGoalManager(dir string, deps ...GoalDependencies) *GoalManager {
	var dep GoalDependencies
	if len(deps) > 0 {
		dep = deps[0]
	}

	sessionDir := dir
	store := goal.NewFileEventStore(filepath.Join(sessionDir, "goal-events.jsonl"))
	mode := goal.NewGoalMode(store, dep.Publisher, dep.Telemetry, dep.ReminderFn)

	queue := NewGoalQueueStore(filepath.Join(sessionDir, "upcoming-goals.json"))

	m := &GoalManager{
		Mode:  mode,
		Queue: queue,
		dir:   filepath.Join(dir, "goals"),
	}
	// Best-effort replay at construction so existing callers keep working; the
	// error is captured/surfaced below rather than silently swallowed.
	m.replayErr = mode.Replay()
	if m.replayErr != nil && dep.Telemetry != nil {
		dep.Telemetry.Track("goal_replay_failed", map[string]any{
			"error": m.replayErr.Error(),
		})
	}
	return m
}

// Restore replays persisted goal events and rebuilds the active-goal state.
// Prefer this over relying on the best-effort replay done in NewGoalManager:
// it returns the error so a corrupted/unreadable event log is surfaced to the
// caller instead of silently erasing the user's active goal.
func (m *GoalManager) Restore() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	err := m.Mode.Replay()
	m.replayErr = err
	return err
}

// ReplayError returns the error (if any) captured by the best-effort replay
// performed during construction or the most recent Restore call.
func (m *GoalManager) ReplayError() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.replayErr
}

// NewGoalManagerWithMode creates a manager with an explicit GoalMode.
func NewGoalManagerWithMode(dir string, mode *goal.GoalMode) *GoalManager {
	queue := NewGoalQueueStore(filepath.Join(dir, "upcoming-goals.json"))
	return &GoalManager{
		Mode:  mode,
		Queue: queue,
		dir:   filepath.Join(dir, "goals"),
	}
}

// CreateGoal creates a new goal with the given objective.
func (m *GoalManager) CreateGoal(objective string) (*Goal, error) {
	if strings.TrimSpace(objective) == "" {
		return nil, fmt.Errorf("goal objective must not be empty")
	}
	if len(objective) > 4000 {
		return nil, fmt.Errorf("goal objective too long (%d chars, max 4000)", len(objective))
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// If there is an active goal, pause and archive it before starting a new one.
	if active := m.Mode.GetActiveGoal(); active != nil {
		reason := "paused by new goal"
		paused, _ := m.Mode.PauseGoal(goal.GoalReasonInput{Reason: &reason}, goal.GoalActorUser)
		m.archiveSnapshot(paused, "paused by new goal")
	}

	snap, err := m.Mode.CreateGoal(goal.CreateGoalInput{Objective: objective, Replace: true}, goal.GoalActorUser)
	if err != nil {
		return nil, err
	}

	return m.snapshotToGoal(snap), nil
}

// ActiveGoal returns the currently active goal, or nil.
func (m *GoalManager) ActiveGoal() *Goal {
	snap := m.Mode.GetActiveGoal()
	if snap == nil {
		return nil
	}
	return m.snapshotToGoal(*snap)
}

// GetGoal returns a goal by ID, or nil.
// For backward compatibility, it searches the current goal and the history.
func (m *GoalManager) GetGoal(id string) *Goal {
	m.mu.Lock()
	defer m.mu.Unlock()

	current := m.Mode.GetGoal().Goal
	if current != nil && current.GoalID == id {
		return m.snapshotToGoal(*current)
	}
	for i := range m.history {
		if m.history[i].ID == id {
			g := m.history[i]
			return &g
		}
	}
	return nil
}

// ListGoals returns all known goals (history + current if any).
func (m *GoalManager) ListGoals() []Goal {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make([]Goal, len(m.history))
	copy(result, m.history)
	if current := m.Mode.GetGoal().Goal; current != nil {
		result = append(result, *m.snapshotToGoal(*current))
	}
	return result
}

// PauseGoal pauses the active goal.
func (m *GoalManager) PauseGoal(id string) error {
	current := m.Mode.GetGoal().Goal
	if current == nil || current.GoalID != id {
		return fmt.Errorf("goal %q not found or not active", id)
	}
	_, err := m.Mode.PauseGoal(goal.GoalReasonInput{Reason: ptrString("paused by user")}, goal.GoalActorUser)
	return err
}

// ResumeGoal resumes a paused or blocked goal.
func (m *GoalManager) ResumeGoal(id string) error {
	current := m.Mode.GetGoal().Goal
	if current == nil || current.GoalID != id {
		return fmt.Errorf("goal %q not found", id)
	}
	_, err := m.Mode.ResumeGoal(goal.GoalReasonInput{}, goal.GoalActorUser)
	return err
}

// CancelGoal removes the goal entirely.
func (m *GoalManager) CancelGoal(id string) error {
	current := m.Mode.GetGoal().Goal
	if current == nil || current.GoalID != id {
		return fmt.Errorf("goal %q not found", id)
	}
	_, err := m.Mode.CancelGoal(goal.GoalActorUser)
	return err
}

// CompleteGoal marks a goal as complete.
func (m *GoalManager) CompleteGoal(id string) error {
	current := m.Mode.GetGoal().Goal
	if current == nil || current.GoalID != id {
		return fmt.Errorf("goal %q not found", id)
	}
	_, err := m.Mode.MarkComplete(goal.GoalReasonInput{Reason: ptrString("completed")}, goal.GoalActorUser)
	return err
}

// ActiveGoalPrompt returns an XML snippet for system prompt injection,
// or empty string if no goal is active.
func (m *GoalManager) ActiveGoalPrompt() string {
	current := m.Mode.GetGoal().Goal
	if current == nil {
		return ""
	}
	return fmt.Sprintf("<active_goal>\n  <objective>%s</objective>\n  <status>%s</status>\n</active_goal>",
		goal.EscapeUntrustedText(current.Objective), current.Status)
}

// Save persists all goals to the goals directory.
// With the new event-sourced engine, this is a no-op; persistence is continuous.
func (m *GoalManager) Save() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := os.MkdirAll(m.dir, 0755); err != nil {
		return err
	}
	current := m.Mode.GetGoal().Goal
	if current != nil {
		data, err := json.MarshalIndent(m.snapshotToGoal(*current), "", "  ")
		if err != nil {
			return err
		}
		path := filepath.Join(m.dir, current.GoalID+".json")
		if err := os.WriteFile(path, data, 0644); err != nil {
			return err
		}
	}
	return nil
}

// Load reads all persisted goals from the goals directory.
// With the new event-sourced engine, replay happens automatically on creation.
func (m *GoalManager) Load() error {
	return nil
}

func (m *GoalManager) archiveSnapshot(snap goal.GoalSnapshot, note string) {
	m.history = append(m.history, Goal{
		ID:        snap.GoalID,
		Objective: snap.Objective,
		Status:    snap.Status,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Events: []GoalEvent{{
			Timestamp: time.Now(),
			Status:    snap.Status,
			Note:      note,
		}},
	})
}

func (m *GoalManager) snapshotToGoal(snap goal.GoalSnapshot) *Goal {
	return &Goal{
		ID:        snap.GoalID,
		Objective: snap.Objective,
		Status:    snap.Status,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Events: []GoalEvent{{
			Timestamp: time.Now(),
			Status:    snap.Status,
			Note:      "created",
		}},
	}
}

func ptrString(s string) *string { return &s }
