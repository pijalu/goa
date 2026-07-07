// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"fmt"

	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/metrics"
)

// RenderStatsTable renders the enhanced agent rows as an aligned table with the
// exact column set requested for the tabbed-run Stats tab:
//
//	role   (provider) model   think   in   out   CH
//
// CH is the cache-hit percentage for the agent (rendered as "-" when the agent
// has no cache activity). It is shared by AgentContent's Stats tab and any
// caller wanting the bare table. Every line is fitted to exactly `width`
// visible columns so the panel background spans the full width.
func RenderStatsTable(rows []AgentEnhancedRow, width int) []string {
	if width < 30 {
		width = 30
	}
	out := []string{fit(statsHeaderLine(), width)}
	if len(rows) == 0 {
		out = append(out, fit("  "+ansi.Faint+"no agents yet"+ansi.Reset, width))
		return out
	}
	for _, r := range rows {
		out = append(out, fit(statsRowLine(r), width))
	}
	return out
}

func statsHeaderLine() string {
	return ansi.Faint + fmt.Sprintf("  %-13s %-24s %-5s %6s %6s %6s",
		"role", "(provider) model", "think", "in", "out", "CH") + ansi.Reset
}

func statsRowLine(r AgentEnhancedRow) string {
	return fmt.Sprintf("  %-13s %-24s %-5s %6d %6d %6s",
		truncField(rowLabel(r), 13),
		truncField(providerModel(r.Provider, r.Model), 24),
		thinkField(r.Thinking),
		r.TokensIn, r.TokensOut, cacheField(r.CacheRead, r.CacheCreation, r.TokensIn))
}

// rowLabel returns the disambiguated display label, falling back to Role.
func rowLabel(r AgentEnhancedRow) string {
	if r.Label != "" {
		return r.Label
	}
	return r.Role
}

// providerModel renders the combined "(provider) model" cell.
func providerModel(provider, model string) string {
	switch {
	case provider == "" && model == "":
		return "-"
	case provider == "":
		return model
	default:
		return "(" + provider + ") " + model
	}
}

func thinkField(t string) string {
	if t == "" {
		return ansi.Faint + "-" + ansi.Reset
	}
	return t
}

// cacheField renders the per-agent cache-hit percentage. It returns "-" when
// the agent reported no cache activity at all (no reads and no writes); a
// genuine 0% (writes but no reads) is shown as "0%" since it is meaningful.
func cacheField(cacheRead, cacheCreation, tokensIn int) string {
	if cacheRead+cacheCreation <= 0 {
		return ansi.Faint + "-" + ansi.Reset
	}
	return fmt.Sprintf("%.0f%%", metrics.CacheHitPct(cacheRead, cacheCreation, tokensIn))
}
