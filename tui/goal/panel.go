// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"strings"

	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/internal/ansi"
)

// Panel renders a bordered goal status box.
type Panel struct {
	snapshot *goal.GoalSnapshot
}

// NewPanel creates a goal panel.
func NewPanel() *Panel { return &Panel{} }

// SetSnapshot updates the panel state.
func (p *Panel) SetSnapshot(snap *goal.GoalSnapshot) {
	p.snapshot = snap
}

// Render implements Component.
func (p *Panel) Render(width int) []string {
	if p.snapshot == nil {
		return nil
	}
	if width < 10 {
		width = 10
	}
	return p.renderBox(width)
}

// HandleInput is a no-op.
func (p *Panel) HandleInput(string) {}

// Invalidate is a no-op.
func (p *Panel) Invalidate() {}

func (p *Panel) renderBox(width int) []string {
	title := "Goal · " + string(p.snapshot.Status)
	top := "┌─ " + ansi.Bold + p.statusColor(title) + ansi.BoldReset + " " + strings.Repeat("─", max(0, width-len("┌─ ")-len(title)-1))
	if len(top) > width {
		top = top[:width]
	}
	bottom := "└" + strings.Repeat("─", width-2) + "┘"

	contentW := width - 4
	lines := []string{top}
	lines = append(lines, p.blockLine("▌ "+p.snapshot.Objective, contentW))
	if p.snapshot.CompletionCriterion != nil {
		lines = append(lines, p.blockLine("✓ "+*p.snapshot.CompletionCriterion, contentW))
	}
	if p.snapshot.Status != goal.GoalActive {
		lines = append(lines, p.blockLine("Status: "+statusWithReason(p.snapshot), contentW))
	}
	lines = append(lines, p.blockLine("Running: "+goal.FormatElapsed(p.snapshot.WallClockMs), contentW))
	lines = append(lines, p.blockLine("Turns: "+goal.FormatInt(p.snapshot.TurnsUsed), contentW))
	lines = append(lines, p.blockLine("Tokens: "+goal.FormatTokens(p.snapshot.TokensUsed), contentW))
	stop := p.stopLine()
	if stop != "" {
		lines = append(lines, p.blockLine("Stop: "+stop, contentW))
	}
	for len(lines) > 1 && len(lines) < width/10+4 {
		lines = append(lines[:len(lines)-1], p.padLine(contentW), lines[len(lines)-1])
	}
	lines = append(lines, bottom)
	return lines
}

func (p *Panel) blockLine(text string, width int) string {
	wrapped := wrapText(text, width)
	var b strings.Builder
	for i, line := range wrapped {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("│ ")
		b.WriteString(padToWidth(line, width))
		b.WriteString(" │")
	}
	return b.String()
}

func (p *Panel) padLine(width int) string {
	return "│ " + strings.Repeat(" ", width) + " │"
}

func (p *Panel) stopLine() string {
	b := p.snapshot.Budget
	parts := []string{}
	if b.TurnBudget != nil {
		parts = append(parts, "after "+goal.FormatInt(*b.TurnBudget)+" turns")
	}
	if b.TokenBudget != nil {
		parts = append(parts, "after "+goal.FormatTokens(*b.TokenBudget)+" tokens")
	}
	if b.WallClockBudgetMs != nil {
		parts = append(parts, "within "+goal.FormatElapsed(*b.WallClockBudgetMs))
	}
	if len(parts) == 0 {
		return ansi.Faint + "No stop condition" + ansi.Reset
	}
	return strings.Join(parts, "; ")
}

func (p *Panel) statusColor(text string) string {
	switch p.snapshot.Status {
	case goal.GoalActive:
		return ansi.Fg(ansiColorPrimary) + text + ansi.Reset
	case goal.GoalDone:
		return ansi.Fg(ansiColorSuccess) + text + ansi.Reset
	case goal.GoalBlocked:
		return ansi.Fg(ansiColorWarning) + text + ansi.Reset
	default:
		return ansi.Fg(ansiColorDim) + text + ansi.Reset
	}
}

func statusWithReason(snap *goal.GoalSnapshot) string {
	status := string(snap.Status)
	if snap.TerminalReason != nil && *snap.TerminalReason != "" {
		status += " — " + *snap.TerminalReason
	}
	return status
}

func wrapText(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}
	var lines []string
	for len(text) > width {
		idx := strings.LastIndexByte(text[:width], ' ')
		if idx < 0 {
			idx = width
		}
		lines = append(lines, strings.TrimRight(text[:idx], " "))
		text = strings.TrimLeft(text[idx:], " ")
	}
	if text != "" {
		lines = append(lines, text)
	}
	if len(lines) == 0 {
		lines = append(lines, "")
	}
	return lines
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
