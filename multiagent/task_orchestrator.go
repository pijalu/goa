// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package multiagent

import (
	"context"
	"fmt"

	gorole "github.com/pijalu/goa/internal/role"
)

// Task represents a single unit of work in a task orchestration session.
type Task struct {
	ID          string
	Description string
	Status      TaskStatus
	Assignee    string // which agent role owns it
}

// TaskStatus represents the current state of a task.
type TaskStatus int

const (
	TaskPending TaskStatus = iota
	TaskPlanning
	TaskCoding
	TaskTesting
	TaskReviewing
	TaskComplete
	TaskFailed
)

func (s TaskStatus) String() string {
	switch s {
	case TaskPending:
		return "pending"
	case TaskPlanning:
		return "planning"
	case TaskCoding:
		return "coding"
	case TaskTesting:
		return "testing"
	case TaskReviewing:
		return "reviewing"
	case TaskComplete:
		return "complete"
	case TaskFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// TaskOrchestrator coordinates a sequence of tasks across agent roles.
// Extends ForegroundOrchestrator with task-level tracking and lifecycle.
type TaskOrchestrator struct {
	*ForegroundOrchestrator
	tasks   []Task
	current int // index of the current task
}

// NewTaskOrchestrator creates a TaskOrchestrator.
func NewTaskOrchestrator(orch *ForegroundOrchestrator) *TaskOrchestrator {
	return &TaskOrchestrator{
		ForegroundOrchestrator: orch,
		tasks:                  nil,
		current:                -1,
	}
}

// Tasks returns the current task list.
func (to *TaskOrchestrator) Tasks() []Task {
	return to.tasks
}

// Current returns the index of the current task, or -1 if none.
func (to *TaskOrchestrator) Current() int {
	return to.current
}

// Progress returns (current+1, total) for display.
func (to *TaskOrchestrator) Progress() (int, int) {
	return to.current + 1, len(to.tasks)
}

// RunSequential runs all tasks in order. Each task goes through
// planning → coding → testing → reviewing phases.
func (to *TaskOrchestrator) emitTaskStatus(task Task, index, total int) {
	to.emit("orchestrator", "user", fmt.Sprintf("TASK_STATUS:%s|%s|%s|%d|%d", task.ID, task.Description, task.Status.String(), index, total))
}

func (to *TaskOrchestrator) RunSequential(ctx context.Context, tasks []Task) error {
	to.tasks = tasks
	to.current = 0

	for to.current < len(tasks) {
		if to.Stopped() {
			return fmt.Errorf("orchestrator stopped at task %d/%d", to.current+1, len(tasks))
		}
		done, err := to.runOneTask(ctx, tasks)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
	return nil
}

func (to *TaskOrchestrator) runOneTask(ctx context.Context, tasks []Task) (bool, error) {
	task := &to.tasks[to.current]
	total := len(tasks)

	to.emit("orchestrator", "system",
		fmt.Sprintf("Task %d/%d: %s", to.current+1, total, task.Description))
	to.emitTaskStatus(*task, to.current, total)

	if skipped := to.handleSteering(task, total); skipped {
		return false, nil
	}

	phases := []struct {
		role   string
		status TaskStatus
		prefix string
	}{
		{gorole.Planner, TaskPlanning, "Planning: "},
		{gorole.Coder, TaskCoding, "Implementing: "},
		{"tester", TaskTesting, "Testing: "},
		{gorole.Reviewer, TaskReviewing, "Reviewing: "},
	}
	for _, p := range phases {
		if err := to.runTaskPhase(ctx, task, p.role, p.status, p.prefix); err != nil {
			task.Status = TaskFailed
			to.emitTaskStatus(*task, to.current, total)
			return false, fmt.Errorf("task %d phase %s failed: %w", to.current+1, p.role, err)
		}
	}

	task.Status = TaskComplete
	to.emitTaskStatus(*task, to.current, total)
	to.emit("orchestrator", "system",
		fmt.Sprintf("Task %d/%d complete: %s", to.current+1, total, task.Description))
	to.current++

	if to.current >= total {
		to.emit("orchestrator", "user",
			fmt.Sprintf("All %d tasks complete.", total))
		return true, nil
	}
	return false, nil
}

func (to *TaskOrchestrator) handleSteering(task *Task, total int) bool {
	text, ok := to.checkSteering()
	if !ok {
		return false
	}
	switch text {
	case "/skip":
		task.Status = TaskComplete
		to.emitTaskStatus(*task, to.current, total)
		to.current++
		return true
	case "/retry":
		// Restart current task — no-op, just continue
	default:
		to.emit("orchestrator", gorole.Planner, "Steering: "+text)
	}
	return false
}

func (to *TaskOrchestrator) runTaskPhase(ctx context.Context, task *Task, role string, status TaskStatus, prefix string) error {
	task.Status = status
	to.emitTaskStatus(*task, to.current, len(to.tasks))
	to.emit("orchestrator", role, prefix+task.Description)

	agent, err := to.pool.GetOrCreate(role)
	if err != nil {
		return err
	}
	prompt := fmt.Sprintf("%s%s", prefix, task.Description)
	if err := agent.Run(ctx, prompt); err != nil {
		return err
	}
	return nil
}

// ParseTasks splits a user-provided comma-separated string into tasks.
// Each task gets an auto-generated ID (task-N) and a Pending status.
func ParseTasks(input string) []Task {
	descriptions := splitTaskDescriptions(input)
	tasks := make([]Task, len(descriptions))
	for i, desc := range descriptions {
		tasks[i] = Task{
			ID:          fmt.Sprintf("task-%d", i+1),
			Description: desc,
			Status:      TaskPending,
			Assignee:    "",
		}
	}
	return tasks
}

// splitTaskDescriptions splits a comma-separated task list into individual descriptions.
func splitTaskDescriptions(input string) []string {
	var result []string
	current := ""
	for _, ch := range input {
		if ch == ',' {
			trimmed := trimSpace(current)
			if trimmed != "" {
				result = append(result, trimmed)
			}
			current = ""
		} else {
			current += string(ch)
		}
	}
	trimmed := trimSpace(current)
	if trimmed != "" {
		result = append(result, trimmed)
	}
	return result
}

// trimSpace removes leading and trailing whitespace.
func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	if start >= end {
		return ""
	}
	return s[start:end]
}

// CollectLastMessage returns the last recorded output from a sub-agent.
// The output must first be recorded via recordOutput() after each Run call.
func (o *ForegroundOrchestrator) CollectLastMessage(agentName string) string {
	if o == nil {
		return ""
	}
	o.outputMu.Lock()
	defer o.outputMu.Unlock()
	return o.lastOutputs[agentName]
}

// RecordOutput stores the most recent output from a sub-agent for later
// retrieval via CollectLastMessage. Call this after each agent.Run() returns.
func (o *ForegroundOrchestrator) RecordOutput(agentName, output string) {
	if o == nil {
		return
	}
	o.outputMu.Lock()
	defer o.outputMu.Unlock()
	if o.lastOutputs == nil {
		o.lastOutputs = make(map[string]string)
	}
	o.lastOutputs[agentName] = output
}
