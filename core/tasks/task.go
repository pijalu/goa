// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tasks

import "time"

// Status represents the lifecycle state of a background task.
type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// Task represents a unit of background work that may outlive a single turn.
type Task struct {
	ID          string    `json:"id"`
	Type        string    `json:"type"`
	Owner       string    `json:"owner"`
	Description string    `json:"description"`
	Status      Status    `json:"status"`
	Result      string    `json:"result,omitempty"`
	Error       string    `json:"error,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// IsTerminal reports whether the task has reached a terminal state.
func (t *Task) IsTerminal() bool {
	switch t.Status {
	case StatusCompleted, StatusFailed, StatusCancelled:
		return true
	default:
		return false
	}
}
