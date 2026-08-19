// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/tui"
)

func computeCost(promptN, predictedN, cacheRead, cacheWrite int, pricing *config.PricingConfig) float64 {
	if pricing == nil {
		return 0
	}
	cost := float64(promptN)/1e6*pricing.InputPer1M +
		float64(predictedN)/1e6*pricing.OutputPer1M +
		float64(cacheRead)/1e6*pricing.CacheReadPer1M +
		float64(cacheWrite)/1e6*pricing.CacheWritePer1M
	return cost
}

func formatTokenCount(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 1000000 {
		// Show as K with one decimal
		k := float64(n) / 1000
		return fmt.Sprintf("%.1fK", k)
	}
	m := float64(n) / 1000000
	return fmt.Sprintf("%.1fM", m)
}

func formatFooterStats(s sessionStats) string {
	parts := buildFooterStatParts(s)
	return strings.Join(parts, " ")
}

// formatFooterStatsPlain returns the same textual stats as formatFooterStats
// but with any ANSI escape sequences removed so the output is suitable for
// --plain headless mode or other consumers that must not receive color codes.
func formatFooterStatsPlain(s sessionStats) string {
	parts := buildFooterStatParts(s)
	for i, p := range parts {
		parts[i] = ansi.Strip(p)
	}
	return strings.Join(parts, " ")
}

func buildFooterStatParts(s sessionStats) []string {
	var parts []string
	if s.PromptN > 0 {
		parts = append(parts, "\u2191"+formatTokenCount(s.PromptN))
	}
	if s.PredictedN > 0 {
		parts = append(parts, "\u2193"+formatTokenCount(s.PredictedN))
	}
	if s.SpeedTokPerSec > 0 {
		parts = append(parts, fmt.Sprintf("%.1f tok/s", s.SpeedTokPerSec))
	}
	// Cache hit percentage: CH:<avg>%▸<last>% where <avg> is the rolling
	// average of the last 10 cache-hit observations and <last> is the most
	// recent per-completion rate. See CacheHitPct for the formula; each
	// element carries its own previous baseline for delta coloring.
	if s.LastCacheHit.Seen {
		parts = append(parts, formatLastCacheHitPart(s.LastCacheHit))
	}
	// Cache-miss counter, next to CH and only when at least one kind is
	// non-zero (a miss means the established cache was bypassed —
	// compression, TTL expiry, prefix churn).
	if part := formatCacheMissPartIfAny(s); part != "" {
		parts = append(parts, part)
	}
	if s.ToolCalls > 0 {
		parts = append(parts, formatToolCallPart(s.ToolCalls, s.ToolCallLevel))
	}
	if s.ShowCost && s.CostUSD > 0 {
		parts = append(parts, fmt.Sprintf("$%.4f", s.CostUSD))
	}
	if s.ContextMax > 0 {
		parts = append(parts, formatContextUsage(footerContextTokens(s), s.ContextMax))
	}
	// Show compression counters when non-zero.
	if s.MicroCompacts > 0 || s.Compacts > 0 {
		parts = append(parts, fmt.Sprintf("c:%dm-%d", s.MicroCompacts, s.Compacts))
	}
	return parts
}

// footerContextTokens resolves the token figure the footer's occupancy display
// renders: the projected next-request cost when recorded, else the estimate
// (CX8/P20 — occupancy displays read the projection; the fallback only applies
// before any provider usage has been recorded, when they are equal anyway).
func footerContextTokens(s sessionStats) int {
	if s.ContextProjected > 0 {
		return s.ContextProjected
	}
	return s.ContextEstimate
}

// formatContextUsage renders context usage as "52.3%/128k". The
// auto-detected-window and soft-compression-layer parenthetical ("(auto+micro)")
// was removed from the status bar as noise (user request); ContextAutoMax is
// still tracked for other surfaces.
func formatContextUsage(estimate, max int) string {
	if max <= 0 {
		return "?"
	}
	pct := float64(estimate) / float64(max) * 100
	value := fmt.Sprintf("%.1f%%/%s", pct, formatTokenCount(max))
	color := tui.TheTheme.ColorHex("status_bar_fg")
	switch {
	case pct > 90:
		color = tui.TheTheme.ColorHex("token_critical")
	case pct > 70:
		color = tui.TheTheme.ColorHex("token_warning")
	}
	return ansi.Fg(color) + value + ansi.Reset
}

// Cache-hit evolution thresholds, in percentage points of delta from the
// previous value. Colors only shift on significant changes (>=5pt drop):
// minor fluctuations stay green to avoid alarm fatigue.
const (
	cacheHitGrowDelta = 1.0  // >= this: growing (bold green)
	cacheHitDropDelta = -5.0 // <= this: significant drop (red); between 0 and this: stable (green)
)

// formatLastCacheHitPart renders the cache hit rate segment of the status
// bar: CH:<avg>%▸<last>% where <avg> is the rolling average of the last
// cacheHitWindowSize observations and <last> is the most recent one.
//
// Each element is colored independently based on its evolution from its
// own previous baseline (significant changes only — >=5pt drop for red):
//   - Growing (>=+1pt):           bold green (#3fb950)
//   - Stable / minor change:      green (#3fb950) — any delta > -5pts
//   - Significant drop (>=5pts):  red (#f85149)
//
// The first observation (no previous baseline) renders as stable green.
func formatLastCacheHitPart(t CacheHitTrend) string {
	avg := t.AvgPct()
	avgPrev := t.AvgPrevPct()
	avgColor := cacheHitColorFor(avg, avgPrev, t.HasPrev)
	lastColor := cacheHitColorFor(t.Pct, t.PrevPct, t.HasPrev)
	return fmt.Sprintf("%sCH:%.1f%%%s%s▸%.1f%%%s",
		avgColor, avg, ansi.Reset,
		lastColor, t.Pct, ansi.Reset)
}

// cacheHitColorFor resolves the SGR prefix (color + optional bold) for a
// cache-hit element (avg or last) based on its delta from the previous
// baseline. hasPrev=false renders as stable green (no baseline).
//
// Color scheme (per the bug report: emphasize significant changes, not minor
// fluctuations):
//   - Growing (>=+1pt):        bold green (#3fb950)
//   - Stable / minor change:   green (#3fb950) — any delta > -5pts
//   - Significant drop:        red (#f85149) — delta <= -5pts
func cacheHitColorFor(pct, prevPct float64, hasPrev bool) string {
	const (
		green = "#3fb950"
		red   = "#f85149"
	)
	delta := pct - prevPct
	switch {
	case !hasPrev:
		// No baseline yet — first observation reads as stable.
		return ansi.Fg(green)
	case delta >= cacheHitGrowDelta:
		return ansi.Bold + ansi.Fg(green)
	case delta > cacheHitDropDelta:
		return ansi.Fg(green)
	default:
		return ansi.Fg(red)
	}
}

// CM part colors. Both values reuse hexes already established in this
// file/theme: the red is the cache-hit drop / token_critical red, the
// orange is the historical CM warning color.
const (
	cacheMissFullColor    = "#f85149"
	cacheMissPartialColor = "#d29922"
)

// formatCacheMissPartIfAny renders the CM part, or "" when both miss kinds
// are zero (a clean session keeps the part hidden).
func formatCacheMissPartIfAny(s sessionStats) string {
	if s.CacheMissesFull == 0 && s.CacheMissesPartial == 0 {
		return ""
	}
	return formatCacheMissPart(s.CacheMissesFull, s.CacheMissesPartial, s.CacheMissedTokens)
}

// formatCacheMissPart renders the cache-miss counter split by failure mode:
// CM:X|Y·N where X counts FULL misses (red — the entire prefix was
// recomputed) and Y counts PARTIAL misses (warning orange — a suffix was
// recomputed). N is the exact token damage summed over the counted misses,
// rendered dim. A zero count is omitted per kind (CM:2 full-only, CM:|3
// partial-only); the caller hides the whole part when both are zero.
func formatCacheMissPart(full, partial int, missedTokens int64) string {
	var b strings.Builder
	// The label rides the color of the first rendered count.
	labelColor := cacheMissPartialColor
	if full > 0 {
		labelColor = cacheMissFullColor
	}
	b.WriteString(ansi.Fg(labelColor))
	b.WriteString("CM:")
	if full > 0 {
		fmt.Fprintf(&b, "%d", full)
	}
	if partial > 0 {
		if full > 0 {
			b.WriteString(ansi.Reset)
			b.WriteString(ansi.Fg(cacheMissPartialColor))
		}
		b.WriteString("|")
		fmt.Fprintf(&b, "%d", partial)
	}
	b.WriteString(ansi.Reset)
	if missedTokens > 0 {
		b.WriteString(ansi.Faint + "·" + groupThousands(missedTokens) + ansi.Reset)
	}
	return b.String()
}

// groupThousands renders n with comma thousands separators (145312 →
// "145,312") so the CM token damage reads at a glance.
func groupThousands(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	digits := fmt.Sprintf("%d", n)
	var b strings.Builder
	first := len(digits) % 3
	if first > 0 {
		b.WriteString(digits[:first])
	}
	for i := first; i < len(digits); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(digits[i : i+3])
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// formatToolCallPart renders the TC:N display with color coding:
//   - green (token_completion):   all good
//   - orange (token_warning):     duplicate/repeat detected
//   - red (token_critical):       budget exceeded, force-stopped
func formatToolCallPart(count int, level ToolCallLevel) string {
	colorHex := tui.TheTheme.ColorHex("status_bar_fg")
	switch level {
	case ToolCallWarning:
		colorHex = tui.TheTheme.ColorHex("token_warning")
	case ToolCallStopped:
		colorHex = tui.TheTheme.ColorHex("token_critical")
	}
	return ansi.Fg(colorHex) + fmt.Sprintf("TC:%d", count) + ansi.Reset
}
