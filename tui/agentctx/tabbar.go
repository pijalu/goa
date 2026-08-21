// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentctx

import (
	"strconv"
	"strings"

	"github.com/pijalu/goa/internal/ansi"
)

// tabActiveColor matches the orchestration tab strip's primary color so both
// bars read as the same UI element.
const tabActiveColor = "#58a6ff"

// AgentTabBar renders the 1-line per-delegation tab strip that sits
// IMMEDIATELY ABOVE the input editor during multi-agent runs. It owns no
// mutable state: it reads the shared *AgentViewRegistry (pull-based), so it
// never needs invalidation beyond the normal frame request.
//
// Layout:  main │ coder·dlg-03 │ coder·dlg-07            [active/total]
//
// The active tab is bold+colored; separators are faint; the [n/total]
// indicator is right-justified. The bar renders NIL (invisible, zero rows)
// while fewer than two views are registered — the single-agent layout is
// byte-identical to before the multi-agent work (the T1 invariant). Badge
// glyphs (✱/▲) land in T5; the state already lives on the registry.
type AgentTabBar struct {
	reg *AgentViewRegistry
}

// NewAgentTabBar returns a bar reading the given registry (nil allowed;
// SetRegistry attaches one later).
func NewAgentTabBar(reg *AgentViewRegistry) *AgentTabBar { return &AgentTabBar{reg: reg} }

// SetRegistry attaches (nil detaches) the registry. Called on the command loop.
func (b *AgentTabBar) SetRegistry(reg *AgentViewRegistry) { b.reg = reg }

// Registry returns the attached registry (nil when none).
func (b *AgentTabBar) Registry() *AgentViewRegistry { return b.reg }

// Render implements tui.Component. Returns a single line, or nil when fewer
// than two views exist.
func (b *AgentTabBar) Render(width int) []string {
	if b.reg == nil || b.reg.Len() < 2 {
		return nil
	}
	if width < 10 {
		width = 10
	}
	return []string{ansi.Truncate(b.renderLine(width), width)}
}

// HandleInput is a no-op: tab navigation is handled at the app layer,
// keeping this component render-only.
func (b *AgentTabBar) HandleInput(string) {}

// Invalidate is a no-op (state is pull-based from the registry).
func (b *AgentTabBar) Invalidate() {}

// renderLine builds the visible tab strip: labels left-justified, the
// [n/total] indicator right-justified.
func (b *AgentTabBar) renderLine(width int) string {
	sep := ansi.Faint + " │ " + ansi.Reset
	left := strings.Join(b.tabLabels(), sep)
	indicator := ansi.Faint + "[" + strconv.Itoa(b.reg.ActiveIndex()+1) + "/" + strconv.Itoa(b.reg.Len()) + "]" + ansi.Reset
	pad := width - ansi.Width(left) - ansi.Width(indicator)
	if pad < 1 {
		pad = 1
	}
	return left + strings.Repeat(" ", pad) + indicator
}

// tabLabels styles each view's label; the active one is bold+colored.
func (b *AgentTabBar) tabLabels() []string {
	ids := b.reg.IDs()
	active := b.reg.ActiveIndex()
	out := make([]string, len(ids))
	for i, id := range ids {
		label := TabLabel(id)
		if i == active {
			out[i] = ansi.Bold + ansi.Fg(tabActiveColor) + label + ansi.Reset
			continue
		}
		out[i] = label
	}
	return out
}

// TabLabel maps a registry id to its tab-strip label. The main agent is
// "main". A minted delegation id dlg-<role>-<NN> (see
// multiagent.DelegateTool.mintDelegationID) renders as "<role>·dlg-<NN>" —
// e.g. dlg-coder-03 → coder·dlg-03 — so two concurrent same-role delegations
// stay distinguishable by their sequence. Any other id renders unchanged.
func TabLabel(id string) string {
	if id == MainAgentID {
		return MainAgentID
	}
	rest, ok := strings.CutPrefix(id, "dlg-")
	if !ok {
		return id
	}
	role, seq, ok := splitDelegationID(rest)
	if !ok {
		return id
	}
	return role + "·dlg-" + seq
}

// splitDelegationID splits "<role>-<NN>" into (role, zero-padded seq). The
// role may itself contain dashes; the sequence is the LAST dash-separated
// numeric field.
func splitDelegationID(s string) (role, seq string, ok bool) {
	i := strings.LastIndex(s, "-")
	if i <= 0 || i == len(s)-1 {
		return "", "", false
	}
	seq = s[i+1:]
	n, err := strconv.Atoi(seq)
	if err != nil || n < 0 {
		return "", "", false
	}
	return s[:i], pad2(n), true
}

// pad2 renders n with at least two digits (the minted %02d shape).
func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}
