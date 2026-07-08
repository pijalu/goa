// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/metrics"
)

// AgentContent renders the active tab's content for the persistent multi-agent
// run view. It owns NO mutable state: it holds a *MultiAgentView pointer (set
// by the app when a run becomes active, cleared when the view is dropped) and
// reads via the view's accessor methods. Render returns nil when no view is
// attached, so the component is invisible outside orchestration mode.
//
// Tabs are reduced to Conversation (default) and Stats. The conversation itself
// renders in the main chat viewport via the agent stream registry, so this
// component returns nil for the Conversation tab.
type AgentContent struct {
	view *MultiAgentView
}

// NewAgentContent returns a content component with no view attached.
func NewAgentContent() *AgentContent { return &AgentContent{} }

// SetView attaches (nil detaches) the shared view. Called on the command loop.
func (c *AgentContent) SetView(v *MultiAgentView) { c.view = v }

// View returns the attached view (nil when none).
func (c *AgentContent) View() *MultiAgentView { return c.view }

// Render implements tui.Component. Returns nil when no run is active so the
// chat viewport can render normally. The Conversation tab content is rendered
// by the main chat viewport via agent-scoped streams, so this component returns
// nil for that tab.
func (c *AgentContent) Render(width int) []string {
	if c.view == nil || !c.view.Active() {
		return nil
	}
	if width < 20 {
		width = 20
	}
	tab, ok := c.view.ActiveTab()
	if !ok {
		return nil
	}
	if tab.Kind == TabConversation || tab.Kind == TabAgent {
		return nil
	}
	lines := c.renderStats(width)
	return append(lines, fit(navHintLine(), width))
}

// navHintLine is the faint one-line hint shown at the bottom of every tab so
// the user can discover tab navigation without reading the docs.
func navHintLine() string {
	return ansi.Faint + "  Ctrl+x tabs · /orchestrate:tab:<n>" + ansi.Reset
}

// HandleInput is a no-op (display only).
func (c *AgentContent) HandleInput(string) {}

// Invalidate is a no-op (state is pull-based from the view).
func (c *AgentContent) Invalidate() {}

func (c *AgentContent) renderStats(width int) []string {
	v := c.view
	out := []string{fit(c.headerLine(), width)}
	if obj := v.MetaValue("objective"); obj != "" {
		out = append(out, fit("  "+ansi.Faint+"objective: "+obj+ansi.Reset, width))
	}
	out = append(out, RenderStatsTable(v.Rows(), width)...)
	in, outT, cacheRead, cacheCreation, turns := v.AggregateTokens()
	// Aggregate cache-hit percentage across all agents. "-" when no agent
	// reported any cache activity, matching the per-row placeholder.
	cacheLabel := "-"
	if cacheRead+cacheCreation > 0 {
		cacheLabel = fmt.Sprintf("%.0f%%", metrics.CacheHitPct(cacheRead, cacheCreation, in))
	}
	footer := ansi.Faint + fmt.Sprintf("  Σ in=%d out=%d CH=%s · turns=%d", in, outT, cacheLabel, turns) + ansi.Reset
	out = append(out, fit(footer, width))
	return out
}

// headerLine renders the Stats-tab header: source · topology · status.
func (c *AgentContent) headerLine() string {
	parts := []string{c.view.Source()}
	if top := c.view.MetaValue("topology"); top != "" {
		parts = append(parts, top)
	}
	parts = append(parts, viewStatusLabel(c.view))
	return ansi.Bold + strings.Join(parts, " · ") + ansi.BoldReset
}

// viewStatusLabel returns the colored run-state word for the header.
func viewStatusLabel(v *MultiAgentView) string {
	switch {
	case v.Failed():
		return ansi.Fg(colDanger) + "failed" + ansi.Reset
	case v.Finished():
		return ansi.Fg(colSuccess) + "complete" + ansi.Reset
	default:
		return ansi.Fg(colPrimary) + "running" + ansi.Reset
	}
}
