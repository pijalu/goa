// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/ansi"
)

// AgentContent renders the active tab's content for the persistent multi-agent
// run view. It owns NO mutable state: it holds a *MultiAgentView pointer (set
// by the app when a run becomes active, cleared when the view is dropped) and
// reads via the view's accessor methods. Render returns nil when no view is
// attached, so the component is invisible outside orchestration mode.
//
// Tabs:
//   - Stats: enhanced agent table + source header + aggregate footer.
//   - Agent: that agent's streamed transcript (content/thinking/markers).
//   - All:   every agent transcript interleaved in first-seen order.
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
// chat viewport can render normally.
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
	var lines []string
	switch tab.Kind {
	case TabStats:
		lines = c.renderStats(width)
	case TabAgent:
		lines = c.renderAgent(tab.Key, width)
	case TabAll:
		lines = c.renderAll(width)
	}
	return append(lines, clip(navHintLine(), width))
}

// navHintLine is the faint one-line hint shown at the bottom of every tab so
// the user can discover tab navigation without reading the docs.
func navHintLine() string {
	return ansi.Faint + "  Ctrl+z / Ctrl+x switch tabs · /orchestrate:tab:<n>" + ansi.Reset
}

// HandleInput is a no-op (display only).
func (c *AgentContent) HandleInput(string) {}

// Invalidate is a no-op (state is pull-based from the view).
func (c *AgentContent) Invalidate() {}

func (c *AgentContent) renderStats(width int) []string {
	v := c.view
	out := []string{clip(c.headerLine(), width)}
	if obj := v.MetaValue("objective"); obj != "" {
		out = append(out, clip("  "+ansi.Faint+"objective: "+obj+ansi.Reset, width))
	}
	out = append(out, RenderStatsTable(v.Rows(), width)...)
	in, outT, ch, turns := v.AggregateTokens()
	footer := ansi.Faint + fmt.Sprintf("  Σ in=%d out=%d CH=%d · turns=%d", in, outT, ch, turns) + ansi.Reset
	out = append(out, clip(footer, width))
	return out
}

func (c *AgentContent) renderAgent(agentID string, width int) []string {
	v := c.view
	out := []string{clip(c.agentHeader(agentID), width)}
	log := v.LogFor(agentID)
	if log == nil {
		return out
	}
	for _, line := range log.Lines() {
		out = append(out, clip("  "+styleLogLine(line), width))
	}
	return out
}

func (c *AgentContent) renderAll(width int) []string {
	v := c.view
	out := []string{clip(c.allHeader(), width)}
	for _, log := range v.OrderedLogs() {
		prefix := ansi.Faint + "[" + roleLabel(log.Role, log.AgentID) + "] " + ansi.Reset
		for _, line := range log.Lines() {
			out = append(out, clip(prefix+styleLogLine(line), width))
		}
	}
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

func (c *AgentContent) agentHeader(agentID string) string {
	role := agentID
	if l := c.view.LogFor(agentID); l != nil && l.Role != "" {
		role = l.Role
	}
	return ansi.Bold + c.view.Source() + " · " + role + ansi.BoldReset
}

func (c *AgentContent) allHeader() string {
	return ansi.Bold + c.view.Source() + " · all" + ansi.BoldReset
}

// styleLogLine renders one transcript line with kind-appropriate styling:
// content normal, thinking/marker faint.
func styleLogLine(line AgentLogLine) string {
	switch line.Kind {
	case LogContent:
		return line.Text
	case LogThinking, LogMarker:
		return ansi.Faint + line.Text + ansi.Reset
	}
	return line.Text
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

// roleLabel returns a stable human-readable label for a log entry.
func roleLabel(role, agentID string) string {
	if role != "" {
		return role
	}
	return agentID
}
