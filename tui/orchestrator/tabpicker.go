// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"fmt"

	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/tui"
)

// SteerTargetPicker is a capturing overlay that lists the active run's steering
// targets ("all", orchestrator, each agent) as a numbered menu. The user can
// jump by number, navigate with arrows, and confirm with enter (esc cancels).
// It owns no run state — it reads the shared *MultiAgentView — so newly-started
// agents appear live while it is open.
//
// It implements tui.Component and tui.Focusable and is shown via ShowOverlay
// (the same mechanism as the run Browser). The host wires SetCloseFunc (hide
// the overlay) and SetPickFunc (select the target).
type SteerTargetPicker struct {
	view     *MultiAgentView
	selected int
	focused  bool
	onClose  func()
	onPick   func(target string)
}

// NewSteerTargetPicker returns a picker over the given view.
func NewSteerTargetPicker(view *MultiAgentView) *SteerTargetPicker {
	return &SteerTargetPicker{view: view}
}

// SetCloseFunc sets the overlay-close callback (called on confirm and cancel).
func (p *SteerTargetPicker) SetCloseFunc(fn func()) { p.onClose = fn }

// SetPickFunc sets the target-select callback (called with the chosen target
// key, or "" on cancel).
func (p *SteerTargetPicker) SetPickFunc(fn func(target string)) { p.onPick = fn }

// Render draws the numbered target menu.
func (p *SteerTargetPicker) Render(width int) []string {
	if p.view == nil {
		return nil
	}
	if width < 24 {
		width = 24
	}
	targets := p.view.SteerTargets()
	labels := p.targetLabels()
	current := p.view.SteerTarget()
	if p.selected >= len(targets) {
		p.selected = len(targets) - 1
	}
	if p.selected < 0 {
		p.selected = 0
	}

	out := []string{clip(ansi.Bold+"Steer target:"+ansi.BoldReset, width)}
	for i, label := range labels {
		out = append(out, clip(p.renderRow(i, label, targets[i] == current), width))
	}
	out = append(out, clip(ansi.Faint+"  ↑↓ select · 1-9 jump · enter · esc"+ansi.Reset, width))
	return out
}

func (p *SteerTargetPicker) targetLabels() []string {
	targets := p.view.SteerTargets()
	labels := make([]string, len(targets))
	for i, t := range targets {
		if t == "all" {
			labels[i] = "all"
			continue
		}
		if l := p.view.LogFor(t); l != nil && l.Role != "" {
			labels[i] = l.Role
			continue
		}
		labels[i] = t
	}
	return labels
}

func (p *SteerTargetPicker) renderRow(i int, label string, active bool) string {
	cursor := "  "
	if i == p.selected {
		cursor = ansi.Bold + ansi.Fg(colPrimary) + "▶ " + ansi.Reset + ansi.BoldReset
	}
	marker := ""
	if active {
		marker = ansi.Faint + " ●" + ansi.Reset
	}
	body := cursor + fmt.Sprintf("%d  %s", i+1, label)
	if i == p.selected {
		body = cursor + ansi.Bold + fmt.Sprintf("%d  %s", i+1, label) + ansi.BoldReset + marker
	} else {
		body += marker
	}
	return body
}

// HandleInput processes digit-jump, arrows, enter, and cancel.
func (p *SteerTargetPicker) HandleInput(data string) {
	targets := p.view.SteerTargets()
	if d := pickDigit(data, len(targets)); d >= 0 {
		p.pick(targets[d])
		return
	}
	switch data {
	case tui.KeyEscape, tui.KeyCtrlC:
		p.cancel()
	case tui.KeyUp:
		if p.selected > 0 {
			p.selected--
		}
	case tui.KeyDown:
		if p.selected < len(targets)-1 {
			p.selected++
		}
	case tui.KeyEnter:
		if p.selected >= 0 && p.selected < len(targets) {
			p.pick(targets[p.selected])
		}
	}
}

func (p *SteerTargetPicker) pick(target string) {
	if fn := p.onPick; fn != nil {
		fn(target)
	}
	p.close()
}

func (p *SteerTargetPicker) cancel() {
	if fn := p.onPick; fn != nil {
		fn("")
	}
	p.close()
}

func (p *SteerTargetPicker) close() {
	if fn := p.onClose; fn != nil {
		fn()
	}
}

// SetFocused implements tui.Focusable.
func (p *SteerTargetPicker) SetFocused(focused bool) { p.focused = focused }

// Focused implements tui.Focusable.
func (p *SteerTargetPicker) Focused() bool { return p.focused }

// Invalidate is a no-op (state is read live from the view).
func (p *SteerTargetPicker) Invalidate() {}

// pickDigit returns the 0-based target index for a single digit key '1'-'9',
// or -1 if the key is not a valid in-range digit. Extracted to keep
// HandleInput flat.
func pickDigit(data string, count int) int {
	if len(data) != 1 || data[0] < '1' || data[0] > '9' {
		return -1
	}
	idx := int(data[0] - '1')
	if idx >= count {
		return -1
	}
	return idx
}

// AgentTabPicker is kept as an alias for compatibility with existing callers
// while the UI is being simplified.
type AgentTabPicker = SteerTargetPicker

// NewAgentTabPicker is kept as an alias for compatibility with existing
// callers.
func NewAgentTabPicker(view *MultiAgentView) *AgentTabPicker {
	return NewSteerTargetPicker(view)
}
