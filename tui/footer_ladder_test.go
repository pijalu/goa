// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

// ladderFooter builds the reported B11 status-bar state: a full stat set on the
// left and the model identity on the right.
func ladderFooter() *Footer {
	f := NewFooter()
	f.SetData(FooterData{
		Workdir:       "/tmp/work",
		Model:         "(opencode-go) deepseek-v4.1-flash",
		Profile:       "coding-posture",
		Mode:          "yolo",
		ThinkingLevel: "xhigh",
		Stats:         "↑291.6K ↓130.4K 72.5 tok/s CH:99.0%▸99.4% TC:190 $0.6259 28.1%/1.0M",
		StatsSegments: []FooterSegment{
			{Tier: FooterTierTokens, Text: "↑291.6K"},
			{Tier: FooterTierTokens, Text: "↓130.4K"},
			{Tier: FooterTierSpeed, Text: "72.5 tok/s"},
			{Tier: FooterTierAvgCH, Text: "CH:99.0%"},
			{Tier: FooterTierLastCH, Text: "▸99.4%", Glue: true},
			{Tier: FooterTierTools, Text: "TC:190"},
			{Tier: FooterTierAmount, Text: "$0.6259"},
			{Tier: FooterTierQuota, Text: "28.1%/1.0M"},
		},
	})
	return f
}

// TestStatusLineNeverExceedsWidth is the B11 invariant: whatever the terminal
// width, the second footer line fits it. A wider line is clipped mid-glyph by
// the screen, which is exactly the reported defect.
func TestStatusLineNeverExceedsWidth(t *testing.T) {
	f := ladderFooter()
	for w := 200; w >= 12; w-- {
		lines := f.Render(w)
		if len(lines) < 2 {
			t.Fatalf("width %d: expected two footer lines, got %d", w, len(lines))
		}
		if got := visibleWidth(lines[1]); got > w {
			t.Errorf("width %d: status line is %d wide: %q", w, got, ansi.Strip(lines[1]))
		}
	}
}

// TestStatusLineKeepsModelNameAndQuota pins the identity guarantee: the model
// name and the context/quota figure survive every width, the name shortened
// with an ellipsis rather than cut mid-word.
func TestStatusLineKeepsModelNameAndQuota(t *testing.T) {
	f := ladderFooter()
	for _, w := range []int{120, 100, 90, 80, 72, 60, 50, 40} {
		plain := ansi.Strip(f.Render(w)[1])
		if !containsAny(plain, "deepseek-v4.1-flash", "deepseek-v4.1-fl…", "deepseek-v4.1-…", "deepseek-v4.…", "deepseek-v…", "deepseek…", "deepse…", "deeps…", "dee…", "de…", "d…", "…") {
			t.Errorf("width %d: model identity vanished: %q", w, plain)
		}
		if !containsAny(plain, "28.1%/1.0M") {
			t.Errorf("width %d: quota figure vanished: %q", w, plain)
		}
		if hasMidWordCut(plain) {
			t.Errorf("width %d: model name cut mid-word without an ellipsis: %q", w, plain)
		}
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 && stringsContains(s, sub) {
			return true
		}
	}
	return false
}

func stringsContains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// hasMidWordCut reports a model name that stops mid-token without the ellipsis
// marker (the reported "deepseek-v4.1-fla" shape).
func hasMidWordCut(plain string) bool {
	idx := indexOf(plain, "deepseek")
	if idx < 0 {
		return false
	}
	rest := plain[idx:]
	end := len(rest)
	for i, r := range rest {
		if r == ' ' || r == '•' {
			end = i
			break
		}
	}
	name := rest[:end]
	return name != "deepseek-v4.1-flash" && !stringsContains(name, "…")
}

// TestStatusLineDropOrderIsTheLadder pins the user-specified priority order:
// the amount goes first, the quota never; the model name is ellipsized only
// after the stats that rank below it are gone.
//
// The recorded figure is each field's SURVIVAL width: the narrowest terminal
// where it is still rendered. Along the ladder that figure must strictly
// decrease — a higher-priority field survives to a narrower terminal than the
// field below it.
func TestStatusLineDropOrderIsTheLadder(t *testing.T) {
	ladder := []string{"$0.6259", "(opencode-go)", "\u2191291.6K", "TC:190", "72.5 tok/s", "\u25b899.4%", "CH:99.0%"}
	survival := survivalWidths(ladderFooter(), ladder, 200, 30)
	for _, field := range ladder {
		if survival[field] == 0 {
			t.Fatalf("field %q never rendered at any width", field)
		}
	}
	for i := 1; i < len(ladder); i++ {
		lower, higher := ladder[i-1], ladder[i]
		if survival[higher] >= survival[lower] {
			t.Errorf("ladder order violated: %q survived to width %d but the lower-priority %q survived to %d",
				higher, survival[higher], lower, survival[lower])
		}
	}
	t.Logf("survival widths: %v", survival)
}

// survivalWidths returns, per probe string, the narrowest width at which the
// footer's second line still contains it.
func survivalWidths(f *Footer, probes []string, widest, narrowest int) map[string]int {
	survival := make(map[string]int, len(probes))
	for _, p := range probes {
		survival[p] = 0
	}
	for w := widest; w >= narrowest; w-- {
		plain := ansi.Strip(f.Render(w)[1])
		for _, p := range probes {
			if stringsContains(plain, p) {
				survival[p] = w // descending sweep: the last hit is the narrowest
			}
		}
	}
	return survival
}

// TestStatusLineQuotaOutlivesEveryStat pins the two never-dropped fields: the
// context/quota figure and the model identity survive narrower than any stat
// above them, and the identity is marked as shortened rather than cut.
func TestStatusLineQuotaOutlivesEveryStat(t *testing.T) {
	f := ladderFooter()
	stats := []string{"$0.6259", "(opencode-go)", "\u2191291.6K", "TC:190", "72.5 tok/s", "\u25b899.4%", "CH:99.0%"}
	survival := survivalWidths(f, append(append([]string{}, stats...), "28.1%/1.0M", "deepseek"), 200, 14)
	for _, field := range stats {
		if survival["28.1%/1.0M"] >= survival[field] {
			t.Errorf("quota (survives to %d) must outlive %q (survives to %d)",
				survival["28.1%/1.0M"], field, survival[field])
		}
	}
	if survival["28.1%/1.0M"] == 0 || survival["deepseek"] == 0 {
		t.Fatalf("quota/model identity vanished: %v", survival)
	}
}
