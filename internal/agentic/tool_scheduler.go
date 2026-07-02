// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"fmt"
	"sync"

	"github.com/pijalu/goa/internal/toolaccess"
)

// ToolCallTask describes one tool call to execute.
type ToolCallTask struct {
	Name    string
	Input   string
	CallID  string
	Access  toolaccess.Access
	Execute func(ctx context.Context) (ToolResult, error)
}

// ToolCallResult holds the outcome of one tool execution.
type ToolCallResult struct {
	Name     string
	CallID   string
	Output   string
	Err      error
	StopTurn bool
}

// ToolScheduler manages concurrent tool execution with conflict detection.
// Tools with non-conflicting resource accesses run in parallel.
// Tools with conflicting accesses are serialized.
// Results are returned in submission order via Collect().
type ToolScheduler struct {
	mu      sync.Mutex
	active  []*scheduledTask
	pending []*scheduledTask
	tasks   []*scheduledTask // all tasks in submission order
	ctx     context.Context
	cancel  context.CancelFunc
}

type scheduledTask struct {
	*ToolCallTask
	result ToolCallResult
	done   chan struct{}
	once   sync.Once // ensures result is set and done closed exactly once
}

// NewToolScheduler creates a scheduler bound to the given context.
func NewToolScheduler(ctx context.Context) *ToolScheduler {
	ctx, cancel := context.WithCancel(ctx)
	return &ToolScheduler{
		ctx:    ctx,
		cancel: cancel,
	}
}

// Add queues a tool call for execution and returns immediately.
// Results are collected in submission order via Collect().
func (s *ToolScheduler) Add(task *ToolCallTask) {
	st := &scheduledTask{
		ToolCallTask: task,
		done:         make(chan struct{}),
	}
	s.tasks = append(s.tasks, st)

	s.mu.Lock()
	blocked := s.isBlockedLocked(task)
	if blocked {
		s.pending = append(s.pending, st)
		s.mu.Unlock()
		// Only pending tasks need a cancellation watcher: a blocked task
		// would otherwise wait for its conflicting active tasks to drain,
		// so without a watcher it could not fail fast on cancellation.
		// Tasks that start immediately are cancelled via their execution
		// goroutine (which receives s.ctx), so they need no extra watcher.
		s.watchCancellation(st)
		return
	}
	s.mu.Unlock()
	s.start(st)
}

// watchCancellation fails a task fast when the scheduler context is
// cancelled before the task gets a chance to run. Uses sync.Once so that
// only one goroutine (this watcher or the execution goroutine) closes
// st.done and sets st.result.
func (s *ToolScheduler) watchCancellation(st *scheduledTask) {
	go func() {
		select {
		case <-s.ctx.Done():
			st.once.Do(func() {
				st.result = ToolCallResult{
					Name: st.Name, CallID: st.CallID,
					Err: s.ctx.Err(),
				}
				close(st.done)
			})
		case <-st.done:
		}
	}()
}

// isBlockedLocked reports whether task conflicts, assuming the mutex is held.
func (s *ToolScheduler) isBlockedLocked(task *ToolCallTask) bool {
	for _, t := range s.active {
		if toolaccess.Conflict(task.Access, t.Access) {
			return true
		}
	}
	for _, t := range s.pending {
		if toolaccess.Conflict(task.Access, t.Access) {
			return true
		}
	}
	return false
}

// start launches a task in a background goroutine.
func (s *ToolScheduler) start(st *scheduledTask) {
	s.mu.Lock()
	s.active = append(s.active, st)
	s.mu.Unlock()

	go func() {
		result, err := s.runTask(st)
		if err != nil && s.ctx.Err() != nil {
			err = s.ctx.Err()
		}
		if err != nil {
			result.Error = err
		}
		st.once.Do(func() {
			st.result = ToolCallResult{
				Name:     st.Name,
				CallID:   st.CallID,
				Output:   result.Output,
				Err:      result.Error,
				StopTurn: result.StopTurn,
			}
			close(st.done)
		})
		s.finish(st)
	}()
}

// runTask executes a tool task and recovers from panics so a single tool bug
// cannot hang Collect() or break the agent turn.
func (s *ToolScheduler) runTask(st *scheduledTask) (ToolResult, error) {
	defer func() {
		if r := recover(); r != nil {
			st.once.Do(func() {
				st.result = ToolCallResult{
					Name:   st.Name,
					CallID: st.CallID,
					Err:    fmt.Errorf("tool panic: %v", r),
				}
				close(st.done)
			})
		}
	}()
	return st.Execute(s.ctx)
}

// finish removes a completed task from active and starts unblocked pending tasks.
func (s *ToolScheduler) finish(completed *scheduledTask) {
	unblocked := s.removeAndFindUnblocked(completed)
	for _, t := range unblocked {
		s.start(t)
	}
}

// removeAndFindUnblocked removes completed from active and starts as many
// pending tasks as can run concurrently without conflict.
func (s *ToolScheduler) removeAndFindUnblocked(completed *scheduledTask) []*scheduledTask {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.removeActive(completed)
	return s.collectUnblocked()
}

func (s *ToolScheduler) removeActive(completed *scheduledTask) {
	for i, t := range s.active {
		if t == completed {
			s.active = append(s.active[:i], s.active[i+1:]...)
			return
		}
	}
}

// collectUnblocked selects pending tasks that can start right now.
//
// A task is runnable when it does not conflict with any currently active
// task. Among runnable tasks we greedily pick the ones that also do not
// conflict with each other, so conflicting tasks are still serialized but
// at least one task always makes progress when nothing is active.
//
// The previous implementation marked any task conflicting with another
// pending task as blocked, which deadlocked when every pending task
// shared a category (e.g. three bash calls all in the "shell" category):
// with no active task to drain the conflict set, none of them could start.
func (s *ToolScheduler) collectUnblocked() []*scheduledTask {
	var unblocked, remaining []*scheduledTask
	picked := make(map[*scheduledTask]bool, len(s.pending))

	for _, t := range s.pending {
		if s.conflictsWithAny(t.Access, s.active) {
			remaining = append(remaining, t)
			continue
		}
		if s.conflictsWithAnyKey(t.Access, picked) {
			remaining = append(remaining, t)
			continue
		}
		picked[t] = true
		unblocked = append(unblocked, t)
	}
	s.pending = remaining
	return unblocked
}

// conflictsWithAny reports whether access conflicts with any task in the slice.
func (s *ToolScheduler) conflictsWithAny(access toolaccess.Access, tasks []*scheduledTask) bool {
	for _, t := range tasks {
		if toolaccess.Conflict(access, t.Access) {
			return true
		}
	}
	return false
}

// conflictsWithAnyKey reports whether access conflicts with any task in the map.
func (s *ToolScheduler) conflictsWithAnyKey(access toolaccess.Access, tasks map[*scheduledTask]bool) bool {
	for t := range tasks {
		if toolaccess.Conflict(access, t.Access) {
			return true
		}
	}
	return false
}

// Collect waits for all queued tasks to complete and returns results
// in submission order (provider order). Call once after all Add() calls.
func (s *ToolScheduler) Collect() []ToolCallResult {
	for _, st := range s.tasks {
		<-st.done
	}

	results := make([]ToolCallResult, len(s.tasks))
	for i, st := range s.tasks {
		results[i] = st.result
	}
	return results
}

// Shutdown cancels all queued and active tasks.
func (s *ToolScheduler) Shutdown() {
	s.cancel()
}
