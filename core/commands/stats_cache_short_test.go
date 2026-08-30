// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core"
)

// The /stats:cache:short view (bugs.md 2026-08-30): ONE session-wide Global
// statistics block only — no per-group sections, no exchanges / per-turn /
// misses / drops tables. These tests pin that contract.

// shortForbiddenSections lists every detail surface the short view must
// never print.
var shortForbiddenSections = []string{
	"## Last 10 exchanges",
	"## Cache usage per turn",
	"## Cache misses",
	"## Cache drops",
}

// countOccurrences counts non-overlapping substrings.
func countOccurrences(s, sub string) int {
	return strings.Count(s, sub)
}

func TestCacheStatsShort_SingleGlobalBlock(t *testing.T) {
	comps := cacheCalls(
		[4]int{1, 0, 300, 0},     // main agent, goal g1: 100% (300/300)
		[4]int{2, 10000, 500, 0}, // companion agent, goal g2: 4.76% (500/10500)
	)
	comps[0].AgentRole, comps[0].GoalID = "main", "g1"
	comps[1].AgentRole, comps[1].GoalID = "companion", "g2"
	rec := &fakeSessionRecorder{
		history:     cacheTurns([3]int{0, 300, 0}, [3]int{10000, 500, 0}),
		completions: comps,
	}
	w := newWriter()
	if err := showCacheStatsShort(w, rec, nil); err != nil {
		t.Fatalf("showCacheStatsShort: %v", err)
	}
	out := w.Text()
	// Exactly one session-wide Global statistics block.
	if got := countOccurrences(out, "## Global statistics"); got != 1 {
		t.Errorf("Global statistics blocks = %d, want 1:\n%s", got, out)
	}
	if got := countOccurrences(out, "Session total:"); got != 1 {
		t.Errorf("Session total lines = %d, want 1:\n%s", got, out)
	}
	// No per-group headers: the two roles would split the full view into
	// "# main · goal:g1" / "# companion · goal:g2" sections.
	if strings.Contains(out, "# main") || strings.Contains(out, "# companion") {
		t.Errorf("short view must not print group headers:\n%s", out)
	}
	for _, forbidden := range shortForbiddenSections {
		if strings.Contains(out, forbidden) {
			t.Errorf("short view must not print %q:\n%s", forbidden, out)
		}
	}
	// Token-weighted combined total: Σread/Σdenominator
	// = (300+500)/(300+10500) = 7.41% over 2 LLM calls.
	if !strings.Contains(out, "7.41%") {
		t.Errorf("short view missing token-weighted 7.41%% total:\n%s", out)
	}
	if !strings.Contains(out, "(token-weighted over 2 LLM calls)") {
		t.Errorf("short view missing per-call unit:\n%s", out)
	}
	// The missed-tokens headline comes along (perfect chain here).
	if !strings.Contains(out, "perfect cache") {
		t.Errorf("short view missing missed-tokens headline:\n%s", out)
	}
}

// TestCacheStatsShort_Routing pins the view-kind classification (the router
// splits user input on every colon, so "/stats:cache:short" arrives as the
// two args ["cache" "short"] — regression: dispatching only on args[0]
// rendered the full view for the short request).
func TestCacheStatsShort_Routing(t *testing.T) {
	cmd := &StatsCommand{}
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{"cache"}, false},               // /stats:cache
		{[]string{":cache"}, false},              // /stats::cache
		{[]string{"cache", "short"}, true},       // /stats:cache:short (router split)
		{[]string{":cache", "short"}, true},      // /stats::cache:short
		{[]string{"cache:short"}, true},          // joined form (programmatic)
		{[]string{":cache:short"}, true},         // joined colon form
		{[]string{"cache", "long"}, false},       // unknown modifier
		{[]string{"cache", "short", "x"}, false}, // trailing junk → full view
		{[]string{"session"}, false},             // unrelated subcommand
		{[]string{"cache:short:extra"}, false},   // deeper nesting → full view
	} {
		if got := cmd.cacheShortRequested(tc.args); got != tc.want {
			t.Errorf("cacheShortRequested(%v) = %v, want %v", tc.args, got, tc.want)
		}
	}
	// Pin the router contract this classification depends on.
	router := core.NewCommandRouter(core.NewCommandRegistry(), nil)
	res := router.Parse("/stats:cache:short")
	if res == nil || res.CmdName != "stats" || len(res.Args) != 2 || res.Args[0] != "cache" || res.Args[1] != "short" {
		t.Fatalf("router.Parse(/stats:cache:short) = %+v, want cmd=stats args=[cache short]", res)
	}
}

func TestCacheStatsShort_EmptyHistory(t *testing.T) {
	w := newWriter()
	if err := showCacheStatsShort(w, &fakeSessionRecorder{}, nil); err != nil {
		t.Fatalf("showCacheStatsShort: %v", err)
	}
	if got := w.Text(); !strings.Contains(got, "No turn history available. Send a message first.") {
		t.Errorf("empty history message missing, got:\n%s", got)
	}
}

func TestCacheStatsShort_NoCacheTraffic(t *testing.T) {
	rec := &fakeSessionRecorder{
		history: cacheTurns([3]int{500, 0, 0}, [3]int{700, 0, 0}),
	}
	w := newWriter()
	if err := showCacheStatsShort(w, rec, nil); err != nil {
		t.Fatalf("showCacheStatsShort: %v", err)
	}
	out := w.Text()
	if !strings.Contains(out, "No prompt-cache activity") {
		t.Errorf("no-cache line missing:\n%s", out)
	}
	if strings.Contains(out, "Session total:") {
		t.Errorf("no-cache view must not print a Session total:\n%s", out)
	}
}

// TestCacheStatsShort_LegacyTurnsFallback pins the turn-series fallback for
// sessions without a completion log: still one Global statistics block,
// unit "turns".
func TestCacheStatsShort_LegacyTurnsFallback(t *testing.T) {
	rec := &fakeSessionRecorder{
		history: cacheTurns([3]int{0, 300, 0}, [3]int{10000, 500, 0}),
	}
	w := newWriter()
	if err := showCacheStatsShort(w, rec, nil); err != nil {
		t.Fatalf("showCacheStatsShort: %v", err)
	}
	out := w.Text()
	if got := countOccurrences(out, "## Global statistics"); got != 1 {
		t.Errorf("Global statistics blocks = %d, want 1:\n%s", got, out)
	}
	if !strings.Contains(out, "(token-weighted over 2 turns)") {
		t.Errorf("legacy fallback must use the turns unit:\n%s", out)
	}
	if !strings.Contains(out, "7.41%") {
		t.Errorf("legacy fallback missing token-weighted 7.41%% total:\n%s", out)
	}
}
