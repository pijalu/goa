// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package orchestrator renders the live orchestrator Summary panel: a bordered
// table of managed agents (role/model/status/turns/tokens) plus the run header.
// It is a pure tui.Component whose state is updated by ApplyEvent/SetRows and
// read by Render. State is mutex-protected so an off-loop event forwarder can
// update it safely while the compositor renders on the commandLoop.
package orchestrator

import (
	"fmt"
	"strings"
	"sync"

	"github.com/pijalu/goa/core/orchestrator"
	"github.com/pijalu/goa/internal/ansi"
)

const (
	colPrimary = "#58a6ff"
	colSuccess = "#3fb950"
	colWarning = "#d29922"
	colDim     = "#8b949e"
	colDanger  = "#f85149"
)

// Row is a renderable agent row (a UI-side copy of orchestrator.AgentRow).
type Row struct {
	ID        string
	Role      string
	Model     string
	Status    string
	Turns     int
	TokensIn  int
	TokensOut int
	ToolCalls int
}

// Panel renders the orchestrator Summary as a bordered table.
type Panel struct {
	mu        sync.Mutex
	topology  string
	objective string
	rows      []Row
	finished  bool
	failed    bool
}

// NewPanel returns an empty panel.
func NewPanel() *Panel { return &Panel{} }

// SetHeader records the run header (called when the run starts).
func (p *Panel) SetHeader(topology, objective string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.topology = topology
	p.objective = objective
	p.finished = false
	p.failed = false
}

// SetRows replaces the agent rows (thread-safe).
func (p *Panel) SetRows(rows []Row) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rows = append([]Row(nil), rows...)
}

// MarkFinished marks the run complete (ok controls success/failure styling).
func (p *Panel) MarkFinished(ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.finished = true
	p.failed = !ok
}

// ApplyEvent updates panel state from one orchestrator event. It is the
// single entry point used by event-driven tests (filmstrip) and the app
// forwarder. Unknown event types are ignored.
func (p *Panel) ApplyEvent(ev orchestrator.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch ev.Type {
	case orchestrator.EventRunStarted:
		if v, ok := ev.Payload["topology"].(string); ok {
			p.topology = v
		}
		if v, ok := ev.Payload["objective"].(string); ok {
			p.objective = v
		}
	case orchestrator.EventRunFinished:
		p.finished = true
		if v, ok := ev.Payload["ok"].(bool); ok {
			p.failed = !v
		}
	case orchestrator.EventAgentStarted:
		p.upsert(Row{ID: ev.AgentID, Role: ev.Role, Model: ev.Model, Status: "running"})
	case orchestrator.EventAgentFinished:
		status := "finished"
		if v, ok := ev.Payload["outcome"].(string); ok && v != "ok" {
			status = v
		}
		p.upsert(Row{ID: ev.AgentID, Role: ev.Role, Status: status})
	}
}

// upsert inserts or updates a row by ID, merging non-zero fields. Caller holds p.mu.
func (p *Panel) upsert(r Row) {
	for i := range p.rows {
		if p.rows[i].ID == r.ID {
			if r.Role != "" {
				p.rows[i].Role = r.Role
			}
			if r.Model != "" {
				p.rows[i].Model = r.Model
			}
			if r.Status != "" {
				p.rows[i].Status = r.Status
			}
			return
		}
	}
	p.rows = append(p.rows, r)
}

// Render implements tui.Component.
func (p *Panel) Render(width int) []string {
	if width < 20 {
		width = 20
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	title := p.titleText()
	top := "┌─ " + ansi.Bold + title + ansi.BoldReset + strings.Repeat("─", dashCount(width, "┌─ ", title))
	lines := []string{clip(top, width)}
	if p.objective != "" {
		lines = append(lines, clip("│ "+ansi.Faint+p.objective+ansi.Reset, width))
	}
	lines = append(lines, p.tableLines(width)...)
	bottom := "└" + strings.Repeat("─", width-2) + "┘"
	lines = append(lines, clip(bottom, width))
	return lines
}

func (p *Panel) titleText() string {
	state := ansi.Fg(colPrimary) + "running" + ansi.Reset
	if p.finished {
		if p.failed {
			state = ansi.Fg(colDanger) + "failed" + ansi.Reset
		} else {
			state = ansi.Fg(colSuccess) + "complete" + ansi.Reset
		}
	}
	top := "orchestration"
	if p.topology != "" {
		top += " · " + p.topology
	}
	return top + " · " + state
}

// tableLines renders the agent rows as a compact aligned table.
func (p *Panel) tableLines(width int) []string {
	if len(p.rows) == 0 {
		return []string{clip("│ "+ansi.Faint+"no agents yet"+ansi.Reset, width)}
	}
	header := fmt.Sprintf("│ %-12s %-10s %-9s %5s %5s", "role", "model", "status", "in", "out")
	out := []string{clip(header, width)}
	for _, r := range p.rows {
		line := fmt.Sprintf("│ %-12s %-10s %-9s %5d %5d",
			truncField(r.Role, 12), truncField(r.Model, 10), statusField(r.Status), r.TokensIn, r.TokensOut)
		out = append(out, clip(line, width))
	}
	return out
}

// HandleInput is a no-op (the panel is display-only).
func (p *Panel) HandleInput(string) {}

// Invalidate is a no-op (state is pull-based).
func (p *Panel) Invalidate() {}

func statusField(s string) string {
	switch s {
	case "running":
		return ansi.Fg(colPrimary) + s + ansi.Reset
	case "finished":
		return ansi.Fg(colSuccess) + s + ansi.Reset
	case "crashed", "blocked":
		return ansi.Fg(colDanger) + s + ansi.Reset
	case "":
		return ansi.Faint + "pending" + ansi.Reset
	default:
		return ansi.Fg(colWarning) + s + ansi.Reset
	}
}

func truncField(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// dashCount returns the number of ─ chars to fill the top border line.
func dashCount(width int, prefix, title string) int {
	used := len(prefix) + visibleLen(title)
	want := width - used - 1
	if want < 0 {
		return 0
	}
	return want
}

// visibleLen approximates visible length by stripping ANSI escapes. Good enough
// for border math; the line is clipped to width afterward regardless.
func visibleLen(s string) int {
	out := 0
	inEsc := false
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
			continue
		}
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		out++
	}
	return out
}

// clip truncates a line (which may contain ANSI) to width visible columns.
func clip(line string, width int) string {
	if visibleLen(line) <= width {
		return line
	}
	// Best-effort: byte-truncate; rare path (very narrow terminals).
	if len(line) > width {
		return line[:width]
	}
	return line
}
