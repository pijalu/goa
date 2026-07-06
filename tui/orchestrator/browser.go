// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package orchestrator renders the orchestrator browser: a two-pane overlay for
// browsing all orchestration runs and inspecting their details.
package orchestrator

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/pijalu/goa/core/orchestrator"
	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/tui"
)

// Browser is a two-pane overlay: left pane lists runs, right pane shows details
// of the selected run. It implements tui.Component and tui.Focusable.
type Browser struct {
	mu       sync.Mutex
	runs     []orchestrator.RunSummary
	selected int
	focused  bool
	onClose  func()
	loadedAt time.Time
}

// NewBrowser returns a browser that loads runs from rootDir. The onClose
// callback is invoked when the user presses Escape or 'q'.
func NewBrowser(rootDir string, onClose func()) *Browser {
	b := &Browser{onClose: onClose}
	b.reload(rootDir)
	return b
}

// SetCloseFunc updates the close callback.
func (b *Browser) SetCloseFunc(fn func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.onClose = fn
}

func (b *Browser) reload(rootDir string) {
	runs, _ := orchestrator.ListRuns(rootDir)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.runs = runs
	b.loadedAt = time.Now()
	if b.selected >= len(b.runs) {
		b.selected = len(b.runs) - 1
	}
	if b.selected < 0 {
		b.selected = 0
	}
}

// Render implements tui.Component.
func (b *Browser) Render(width int) []string {
	if width < 20 {
		width = 20
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	leftW := width / 3
	if leftW < 12 {
		leftW = 12
	}
	if leftW > width/2 {
		leftW = width / 2
	}
	rightW := width - leftW - 1
	if rightW < 10 {
		rightW = 10
	}

	lines := []string{b.renderTopBorder(width, leftW)}
	bodyH := b.renderBodyHeight(width)

	leftLines := b.renderLeftPane(leftW, bodyH)
	midLines := b.renderMidBorder(bodyH)
	rightLines := b.renderRightPane(rightW, bodyH)

	for i := 0; i < bodyH; i++ {
		var parts []string
		if i < len(leftLines) {
			parts = append(parts, leftLines[i])
		} else {
			parts = append(parts, strings.Repeat(" ", leftW))
		}
		if i < len(midLines) {
			parts = append(parts, midLines[i])
		} else {
			parts = append(parts, " ")
		}
		if i < len(rightLines) {
			parts = append(parts, rightLines[i])
		} else {
			parts = append(parts, strings.Repeat(" ", rightW))
		}
		line := strings.Join(parts, "")
		lines = append(lines, clip(line, width))
	}

	lines = append(lines, b.renderBottomBorder(width, leftW))
	lines = append(lines, b.renderFooter(width))
	return lines
}

func (b *Browser) renderTopBorder(width, leftW int) string {
	title := "─ Orchestrator Runs "
	left := "┌" + strings.Repeat("─", leftW-2) + "┬"
	fill := width - visibleLen(left) - visibleLen(title) - 1
	if fill < 0 {
		fill = 0
	}
	return left + strings.Repeat("─", fill) + title + "┐"
}

func (b *Browser) renderBodyHeight(width int) int {
	// Conservative: header + border + footer + a few rows; callers clip to terminal.
	h := 8
	if width > 40 {
		h = 12
	}
	return h
}

func (b *Browser) renderMidBorder(height int) []string {
	lines := make([]string, height)
	for i := range lines {
		lines[i] = "│"
	}
	return lines
}

func (b *Browser) renderBottomBorder(width, leftW int) string {
	left := "└" + strings.Repeat("─", leftW-2) + "┴"
	fill := width - visibleLen(left) - 1
	if fill < 0 {
		fill = 0
	}
	return left + strings.Repeat("─", fill) + "┘"
}

func (b *Browser) renderLeftPane(width, height int) []string {
	if width < 2 {
		width = 2
	}
	var lines []string
	inner := width - 2
	if inner < 0 {
		inner = 0
	}
	if len(b.runs) == 0 {
		msg := "no runs"
		lines = append(lines, "│ "+padOrTrunc(msg, inner))
		for i := 1; i < height; i++ {
			lines = append(lines, "│ "+strings.Repeat(" ", inner))
		}
		return lines
	}

	maxRows := height - 1
	start := 0
	if b.selected >= maxRows {
		start = b.selected - maxRows + 1
	}
	for i := start; i < len(b.runs) && len(lines) < maxRows; i++ {
		r := b.runs[i]
		label := r.NameOrID()
		if len(label) > inner-4 {
			label = label[:inner-5] + "…"
		}
		prefix := "  "
		if i == b.selected {
			prefix = ansi.Fg(colPrimary) + "▶ " + ansi.Reset
		}
		status := string(statusLabel(r.Finished))
		line := fmt.Sprintf("%s%s %s", prefix, label, status)
		lines = append(lines, "│ "+padOrTrunc(line, inner))
	}
	for len(lines) < height {
		lines = append(lines, "│ "+strings.Repeat(" ", inner))
	}
	return lines
}

func (b *Browser) renderRightPane(width, height int) []string {
	if width < 2 {
		width = 2
	}
	inner := width - 2
	if inner < 0 {
		inner = 0
	}
	var lines []string
	if len(b.runs) == 0 {
		lines = append(lines, "  No runs found.")
		for len(lines) < height {
			lines = append(lines, strings.Repeat(" ", width))
		}
		return lines
	}

	r := b.runs[b.selected]
	lines = append(lines, "  "+ansi.Bold+r.NameOrID()+ansi.BoldReset)
	lines = append(lines, "  "+padOrTrunc(fmt.Sprintf("topology: %s", r.Topology), inner))
	lines = append(lines, "  "+padOrTrunc(fmt.Sprintf("status:   %s", statusLabel(r.Finished)), inner))
	lines = append(lines, "  "+padOrTrunc(fmt.Sprintf("agents:   %d", r.AgentCount), inner))
	lines = append(lines, "  "+padOrTrunc(fmt.Sprintf("updated:  %s", r.UpdatedAt.Format("2006-01-02 15:04")), inner))
	obj := r.Objective
	if len(obj) > inner-12 {
		obj = obj[:inner-13] + "…"
	}
	lines = append(lines, "  objective: "+obj)
	for len(lines) < height-2 {
		lines = append(lines, strings.Repeat(" ", width))
	}
	lines = append(lines, "  "+ansi.Faint+"resume: /orchestrate:resume:id="+r.NameOrID()+ansi.Reset)
	lines = append(lines, "  "+ansi.Faint+"delete: /orchestrate:delete:id="+r.NameOrID()+ansi.Reset)
	lines = append(lines, "  "+ansi.Faint+"q: close"+ansi.Reset)
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	return lines
}

func (b *Browser) renderFooter(width int) string {
	if width < 2 {
		width = 2
	}
	loaded := b.loadedAt.Format("15:04:05")
	text := fmt.Sprintf("loaded %s · %d run(s)", loaded, len(b.runs))
	return padOrTrunc(text, width)
}

func statusLabel(finished bool) string {
	if finished {
		return "finished"
	}
	return "running"
}

func padOrTrunc(s string, n int) string {
	v := visibleLen(s)
	if v > n {
		return s[:n]
	}
	return s + strings.Repeat(" ", n-v)
}

// HandleInput processes navigation keys.
func (b *Browser) HandleInput(data string) {
	switch {
	case data == tui.KeyEscape, data == tui.KeyCtrlC, data == "q":
		b.mu.Lock()
		fn := b.onClose
		b.mu.Unlock()
		if fn != nil {
			fn()
		}
	case data == tui.KeyUp:
		b.mu.Lock()
		if b.selected > 0 {
			b.selected--
		}
		b.mu.Unlock()
	case data == tui.KeyDown:
		b.mu.Lock()
		if b.selected < len(b.runs)-1 {
			b.selected++
		}
		b.mu.Unlock()
	}
}

// SetFocused implements tui.Focusable.
func (b *Browser) SetFocused(focused bool) { b.focused = focused }

// Focused implements tui.Focusable.
func (b *Browser) Focused() bool { return b.focused }

// Invalidate is a no-op (state is pull-based).
func (b *Browser) Invalidate() {}
