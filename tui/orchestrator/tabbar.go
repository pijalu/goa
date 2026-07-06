// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"strings"

	"github.com/pijalu/goa/internal/ansi"
)

// AgentTabBar renders the 1-line tab strip that sits immediately above the
// input editor during a multi-agent run. It owns NO mutable state: it reads the
// shared *MultiAgentView. Render returns nil when no run is active, so the bar
// is invisible (and costs no row) outside orchestration mode.
//
// Layout:  source: Stats │ orchestrator │ coder │ All      [active/total]
// The active tab is bold+colored; separators are faint; the [n/total]
// indicator is right-justified. The `source:` prefix lets the same component
// read correctly when reused for pipeline/swarm sources.
type AgentTabBar struct {
	view *MultiAgentView
}

// NewAgentTabBar returns a bar with no view attached.
func NewAgentTabBar() *AgentTabBar { return &AgentTabBar{} }

// SetView attaches (nil detaches) the shared view. Called on the command loop.
func (b *AgentTabBar) SetView(v *MultiAgentView) { b.view = v }

// View returns the attached view (nil when none).
func (b *AgentTabBar) View() *MultiAgentView { return b.view }

// Render implements tui.Component. Returns a single line, or nil when inactive.
func (b *AgentTabBar) Render(width int) []string {
	if b.view == nil || !b.view.Active() {
		return nil
	}
	if width < 10 {
		width = 10
	}
	return []string{clip(b.renderLine(width), width)}
}

// HandleInput is a no-op: navigation is handled at the app layer via hotkeys
// and the /orchestrate:tab command, keeping this component render-only.
func (b *AgentTabBar) HandleInput(string) {}

// Invalidate is a no-op (state is pull-based from the view).
func (b *AgentTabBar) Invalidate() {}

// renderLine builds the visible tab strip. Complexity stays well under the TUI
// render budget by delegating label styling to a helper.
func (b *AgentTabBar) renderLine(width int) string {
	v := b.view
	sep := ansi.Faint + " │ " + ansi.Reset
	parts := tabLabels(v)
	left := ansi.Bold + v.Source() + ":" + ansi.BoldReset + " " + strings.Join(parts, sep)
	indicator := ansi.Faint + "[" + v.TabIndex() + "]" + ansi.Reset
	pad := width - visibleLen(left) - visibleLen(indicator)
	if pad < 1 {
		pad = 1
	}
	return left + strings.Repeat(" ", pad) + indicator
}

// tabLabels styles each tab label; the active one is bold+colored.
func tabLabels(v *MultiAgentView) []string {
	tabs := v.Tabs()
	active := v.ActiveIndex()
	out := make([]string, len(tabs))
	for i, t := range tabs {
		if i == active {
			out[i] = ansi.Bold + ansi.Fg(colPrimary) + t.Label + ansi.Reset
			continue
		}
		out[i] = t.Label
	}
	return out
}
