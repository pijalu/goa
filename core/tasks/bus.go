// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tasks

import (
	"sync"
	"time"

	"github.com/pijalu/goa/internal/event"
)

// Bus manages a collection of background tasks.
type Bus struct {
	mu       sync.RWMutex
	tasks    map[string]*Task
	store    Store
	eventBus *event.Bus
}

// NewBus creates a task bus with the given store and optional event bus.
func NewBus(store Store, bus *event.Bus) *Bus {
	return &Bus{
		tasks:    make(map[string]*Task),
		store:    store,
		eventBus: bus,
	}
}

// Register creates a pending task and returns it.
func (b *Bus) Register(id, taskType, owner, description string) *Task {
	now := time.Now()
	t := &Task{
		ID:          id,
		Type:        taskType,
		Owner:       owner,
		Description: description,
		Status:      StatusPending,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	b.mu.Lock()
	b.tasks[id] = t
	b.mu.Unlock()
	b.persist(t)
	b.emit(t)
	return t
}

// Start marks a task as running.
func (b *Bus) Start(id string) *Task {
	return b.update(id, StatusRunning, "", "")
}

// Complete marks a task as completed with a result.
func (b *Bus) Complete(id, result string) *Task {
	return b.update(id, StatusCompleted, result, "")
}

// Fail marks a task as failed with an error message.
func (b *Bus) Fail(id, err string) *Task {
	return b.update(id, StatusFailed, "", err)
}

// Cancel marks a task as cancelled.
func (b *Bus) Cancel(id string) *Task {
	return b.update(id, StatusCancelled, "", "")
}

// Get returns a task by ID.
func (b *Bus) Get(id string) (*Task, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	t, ok := b.tasks[id]
	return t, ok
}

// List returns all tasks.
func (b *Bus) List() []*Task {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]*Task, 0, len(b.tasks))
	for _, t := range b.tasks {
		out = append(out, t)
	}
	return out
}

// Active returns non-terminal tasks.
func (b *Bus) Active() []*Task {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var out []*Task
	for _, t := range b.tasks {
		if !t.IsTerminal() {
			out = append(out, t)
		}
	}
	return out
}

func (b *Bus) update(id string, status Status, result, err string) *Task {
	b.mu.Lock()
	t, ok := b.tasks[id]
	if !ok {
		b.mu.Unlock()
		return nil
	}
	t.Status = status
	t.Result = result
	t.Error = err
	t.UpdatedAt = time.Now()
	b.mu.Unlock()
	b.persist(t)
	b.emit(t)
	return t
}

func (b *Bus) persist(t *Task) {
	if b.store != nil {
		_ = b.store.Save(*t)
	}
}

func (b *Bus) emit(t *Task) {
	if b.eventBus == nil {
		return
	}
	select {
	case b.eventBus.Chat <- event.ChatEvent{
		TaskUpdate: &event.TaskUpdate{
			TaskID:      t.ID,
			Status:      string(t.Status),
			Description: t.Description,
			Result:      t.Result,
		},
	}:
	default:
	}
}
