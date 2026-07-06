// SPDX-License-Identifier-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"fmt"

	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/tui"
)

// AgentTabPicker is a capturing overlay that lists the run's tabs as a numbered
// menu and lets the user jump by number, navigate with arrows, and confirm with
// enter (esc cancels). It owns no run state — it reads the shared
// *MultiAgentView — so newly-started agents appear live while it is open.
//
// It implements tui.Component and tui.Focusable and is shown via ShowOverlay
// (the same mechanism as the run Browser). The host wires SetCloseFunc (hide
// the overlay) and SetPickFunc (select the tab).
type AgentTabPicker struct {
	view     *MultiAgentView
	selected int
	focused  bool
	onClose  func()
	onPick   func(key string)
}

// NewAgentTabPicker returns a picker over the given view.
func NewAgentTabPicker(view *MultiAgentView) *AgentTabPicker {
	return &AgentTabPicker{view: view}
}

// SetCloseFunc sets the overlay-close callback (called on confirm and cancel).
func (p *AgentTabPicker) SetCloseFunc(fn func()) { p.onClose = fn }

// SetPickFunc sets the tab-select callback (called with the chosen tab Key, or
// "" on cancel).
func (p *AgentTabPicker) SetPickFunc(fn func(key string)) { p.onPick = fn }

// Render draws the numbered tab menu.
func (p *AgentTabPicker) Render(width int) []string {
	if p.view == nil {
		return nil
	}
	if width < 24 {
		width = 24
	}
	tabs := p.view.Tabs()
	activeKey := activeTabKey(p.view)
	if p.selected >= len(tabs) {
		p.selected = len(tabs) - 1
	}
	if p.selected < 0 {
		p.selected = 0
	}

	out := []string{clip(ansi.Bold+"Switch tab:"+ansi.BoldReset, width)}
	for i, tab := range tabs {
		out = append(out, clip(p.renderRow(i, tab, activeKey), width))
	}
	out = append(out, clip(ansi.Faint+"  ↑↓ select · 1-9 jump · enter · esc"+ansi.Reset, width))
	return out
}

func (p *AgentTabPicker) renderRow(i int, tab AgentTab, activeKey string) string {
	cursor := "  "
	if i == p.selected {
		cursor = ansi.Bold + ansi.Fg(colPrimary) + "▶ " + ansi.Reset + ansi.BoldReset
	}
	label := tab.Label
	marker := ""
	if tab.Key == activeKey {
		marker = ansi.Faint + " ●" + ansi.Reset
	}
	body := cursor + fmt.Sprintf("%d  %s%s", i+1, label, marker)
	if i == p.selected {
		body = cursor + ansi.Bold + fmt.Sprintf("%d  %s", i+1, label) + ansi.BoldReset + marker
	}
	return body
}

// HandleInput processes digit-jump, arrows, enter, and cancel.
func (p *AgentTabPicker) HandleInput(data string) {
	tabs := p.view.Tabs()
	if d := pickDigit(data, len(tabs)); d >= 0 {
		p.pick(tabs[d].Key)
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
		if p.selected < len(tabs)-1 {
			p.selected++
		}
	case tui.KeyEnter:
		if p.selected >= 0 && p.selected < len(tabs) {
			p.pick(tabs[p.selected].Key)
		}
	}
}

func (p *AgentTabPicker) pick(key string) {
	if fn := p.onPick; fn != nil {
		fn(key)
	}
	p.close()
}

func (p *AgentTabPicker) cancel() {
	if fn := p.onPick; fn != nil {
		fn("")
	}
	p.close()
}

func (p *AgentTabPicker) close() {
	if fn := p.onClose; fn != nil {
		fn()
	}
}

// SetFocused implements tui.Focusable.
func (p *AgentTabPicker) SetFocused(focused bool) { p.focused = focused }

// Focused implements tui.Focusable.
func (p *AgentTabPicker) Focused() bool { return p.focused }

// Invalidate is a no-op (state is read live from the view).
func (p *AgentTabPicker) Invalidate() {}

// pickDigit returns the 0-based tab index for a single digit key '1'-'9', or -1
// if the key is not a valid in-range digit. Extracted to keep HandleInput flat.
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

func activeTabKey(v *MultiAgentView) string {
	if tab, ok := v.ActiveTab(); ok {
		return tab.Key
	}
	return ""
}
