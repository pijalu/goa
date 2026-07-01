// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"strings"

	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/internal/ansi"
)

// Bubble renders the current goal objective as a collapsible line above the
// input editor. It mirrors the spec layout:
//
//	────────────────────────────────────────────────────
//	⟐ [model-name] Create a html page that renders a fire
//	────────────────────────────────────────────────────
//
// Concurrency: the commandLoop is the sole owner of Bubble state; every
// method runs on the commandLoop. No mutex is required.
type Bubble struct {
	snapshot       *goal.GoalSnapshot
	collapsed      bool
	separatorColor string
}

// NewBubble creates a goal bubble component.
func NewBubble() *Bubble { return &Bubble{} }

// SetSeparatorColor sets the ANSI hex color used for the separator lines.
func (b *Bubble) SetSeparatorColor(color string) { b.separatorColor = color }

// SetSnapshot updates the displayed goal.
func (b *Bubble) SetSnapshot(snap *goal.GoalSnapshot) { b.snapshot = snap }

// Snapshot returns the current snapshot.
func (b *Bubble) Snapshot() *goal.GoalSnapshot { return b.snapshot }

// ToggleCollapse switches the collapsed state.
func (b *Bubble) ToggleCollapse() { b.collapsed = !b.collapsed }

// Collapsed reports whether the bubble is collapsed.
func (b *Bubble) Collapsed() bool { return b.collapsed }

// HandleInput toggles collapse on Ctrl+G.
func (b *Bubble) HandleInput(data string) {
	if data == "ctrl+g" {
		b.ToggleCollapse()
	}
}

// Invalidate is a no-op.
func (b *Bubble) Invalidate() {}

// Render implements Component.
func (b *Bubble) Render(width int) []string {
	if b.snapshot == nil || b.snapshot.Status != goal.GoalActive {
		return nil
	}
	if width < 10 {
		width = 10
	}
	return b.renderLines(width)
}

func (b *Bubble) renderLines(width int) []string {
	sep := b.coloredSeparator(width)
	if b.collapsed {
		collapsed := b.collapsedText(width)
		return []string{sep, collapsed, sep}
	}

	full := b.fullText(width)
	lines := []string{sep}
	lines = append(lines, full...)
	lines = append(lines, sep)
	return lines
}

func (b *Bubble) coloredSeparator(width int) string {
	color := b.separatorColor
	if color == "" {
		color = "#888888"
	}
	return ansi.Fg(color) + strings.Repeat("─", width) + ansi.Reset
}

func (b *Bubble) collapsedText(width int) string {
	marker := "⟐ "
	label := "goal hidden"
	if b.snapshot.Name != "" {
		label = "[" + b.snapshot.Name + "] "
	}
	if b.snapshot.Objective != "" {
		rest := b.snapshot.Objective
		max := width - ansi.Width(marker+label) - 4
		if max > 0 && ansi.Width(rest) > max {
			rest = ansi.Truncate(rest, max-3) + "..."
		}
		label += rest
	}
	line := marker + label
	pad := width - ansi.Width(line)
	if pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	return line
}

func (b *Bubble) fullText(width int) []string {
	marker := "⟐ "
	prefix := ""
	if b.snapshot.Name != "" {
		prefix = "[" + b.snapshot.Name + "] "
	}
	text := marker + prefix + b.snapshot.Objective
	return ansi.Wrap(text, width)
}
