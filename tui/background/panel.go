// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package background renders a live status panel for background tasks
// managed by the bg_exec tool. The panel appears only when there are tasks
// and polls a snapshot function on every render so the view stays current
// without the manager needing to know about TUI details.
package background

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/pijalu/goa/internal/ansi"
)

// Task is a UI-side snapshot of a background task.
type Task struct {
	ID      string
	Command string
	Status  string
	PID     int
}

// Snapshot returns the current set of background tasks. It is called on the
// TUI commandLoop during Render, so it must be fast and non-blocking.
type Snapshot func() []Task

// Panel renders a compact live status panel for background tasks.
type Panel struct {
	mu       sync.Mutex
	snapshot Snapshot
}

// padToWidth pads a string with spaces so its visible width equals width.
func padToWidth(s string, width int) string {
	w := ansi.Width(s)
	if w < width {
		return s + strings.Repeat(" ", width-w)
	}
	return s
}

// NewPanel creates a panel bound to the given snapshot function.
func NewPanel(snapshot Snapshot) *Panel {
	return &Panel{snapshot: snapshot}
}

// SetSnapshot replaces the snapshot function. Safe for concurrent use.
func (p *Panel) SetSnapshot(snapshot Snapshot) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.snapshot = snapshot
}

// Render implements tui.Component. Returns nil when no tasks are active so the
// panel takes no vertical space.
func (p *Panel) Render(width int) []string {
	if width < 10 {
		width = 10
	}
	p.mu.Lock()
	snapshot := p.snapshot
	p.mu.Unlock()
	if snapshot == nil {
		return nil
	}
	tasks := snapshot()
	if len(tasks) == 0 {
		return nil
	}
	return p.render(width, tasks)
}

func (p *Panel) render(width int, tasks []Task) []string {
	running := 0
	for _, t := range tasks {
		if t.Status == "running" {
			running++
		}
	}

	title := "Background (" + strconv.Itoa(running) + "/" + strconv.Itoa(len(tasks)) + ")"
	top := "┌─ " + ansi.Bold + title + ansi.BoldReset + " "
	top = padToWidth(top, width-1) + "┐"
	lines := []string{top}

	maxShown := 3
	shown := tasks
	if len(tasks) > maxShown {
		shown = tasks[:maxShown]
	}
	for _, t := range shown {
		lines = append(lines, p.renderTaskLine(width, t))
	}
	if len(tasks) > maxShown {
		more := "│ … and " + strconv.Itoa(len(tasks)-maxShown) + " more"
		lines = append(lines, padToWidth(more, width))
	}
	lines = append(lines, "└"+strings.Repeat("─", width-2)+"┘")
	return lines
}

func (p *Panel) renderTaskLine(width int, t Task) string {
	pid := ""
	if t.PID > 0 {
		pid = fmt.Sprintf("pid %d", t.PID)
	}
	status := t.Status
	if status == "running" {
		status = ansi.Fg("#58a6ff") + status + ansi.Reset
	} else if status == "error" || status == "killed" {
		status = ansi.Fg("#f85149") + status + ansi.Reset
	}
	line := fmt.Sprintf("│ %s %s", t.ID, t.Command)
	if pid != "" {
		line += " (" + pid + ")"
	}
	if status != "" {
		line += " — " + status
	}
	line = ansi.Truncate(line, width-1) + "│"
	return padToWidth(line, width)
}

// HandleInput is a no-op (the panel is display-only).
func (p *Panel) HandleInput(string) {}

// Invalidate is a no-op (state is pull-based).
func (p *Panel) Invalidate() {}
