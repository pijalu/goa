// SPDX-License-Identifier: GPL-3.0-or-later

package commands

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/ansi"
	tui "github.com/pijalu/goa/tui"
)

// --- Section separators: --- rules between goal groups + blank lines -------
// between report sections (bugs.md 2026-08-30) -------------------------------

// assertBlankLineBeforeSections verifies every ## section header that follows
// a previous section is preceded by a blank line, so sections never abut.
// (The first section sits under the group header or opens the report.)
func assertBlankLineBeforeSections(t *testing.T, out string) {
	t.Helper()
	for _, h := range []string{
		"## Global statistics",
		"## Cache usage per turn",
		"## Cache misses",
		"## Cache drops",
	} {
		if !strings.Contains(out, "\n\n"+h+"\n") {
			t.Errorf("%q not preceded by a blank line:\n%s", h, out)
		}
	}
}

// TestWriteCacheView_MultiGroupSeparators pins the multi-group layout: a
// `---` horizontal rule exactly once between the two goal groups (never
// leading, never trailing) plus a blank line before every ## section header.
func TestWriteCacheView_MultiGroupSeparators(t *testing.T) {
	turns := cacheTurns([3]int{10, 300, 100})
	turns[0].AgentRole, turns[0].GoalID = "main", "g1"
	comps := cacheCalls([4]int{1, 10, 300, 100})
	comps[0].AgentRole, comps[0].GoalID = "main", "g1"
	turns2 := append(turns, core.TurnRecord{
		Number: 2, AgentRole: "companion",
		TokenUsage: core.TurnTokenUsage{PromptN: 5, CacheRead: 40, CacheWrite: 10},
	})
	comps2 := append(comps, core.CompletionRecord{
		TurnNumber: 2, AgentRole: "companion", PromptN: 5, CacheRead: 40, CacheWrite: 10,
	})
	var b strings.Builder
	writeCacheView(&b, cacheTurnsFromHistory(turns2, nil), cacheCompletionsFromHistory(comps2),
		map[string]string{"g1": "tidy.falcon"})
	out := b.String()

	if got := strings.Count(out, "\n---\n"); got != 1 {
		t.Fatalf("want exactly one --- rule between groups, found %d:\n%s", got, out)
	}
	first := strings.Index(out, "# main · tidy.falcon\n")
	rule := strings.Index(out, "\n---\n")
	second := strings.Index(out, "# companion\n")
	if rule <= first || rule >= second {
		t.Fatalf("rule at %d must sit strictly between the group headers (%d < rule < %d):\n%s",
			rule, first, second, out)
	}
	assertBlankLineBeforeSections(t, out)
}

// TestWriteCacheView_SingleGroupSeparators: the header-less solo report gains
// the inter-section blank lines but never a rule.
func TestWriteCacheView_SingleGroupSeparators(t *testing.T) {
	var b strings.Builder
	writeCacheView(&b,
		cacheTurnsFromHistory(cacheTurns([3]int{10, 300, 100}), nil),
		cacheCompletionsFromHistory(cacheCalls([4]int{1, 10, 300, 100})),
		nil,
	)
	out := b.String()
	if strings.Contains(out, "\n---\n") {
		t.Fatalf("single-group report must not contain a --- rule:\n%s", out)
	}
	assertBlankLineBeforeSections(t, out)
}

// TestWriteCacheView_RuleRendersThroughMarkdownPipeline feeds a multi-group
// report through the real MD renderer: the --- rule must surface as the
// full-width faint rule line (────) between the goal sections, not as raw
// dashes or a mis-parsed heading.
func TestWriteCacheView_RuleRendersThroughMarkdownPipeline(t *testing.T) {
	turns := cacheTurns([3]int{10, 300, 100})
	turns[0].AgentRole, turns[0].GoalID = "main", "g1"
	comps := cacheCalls([4]int{1, 10, 300, 100})
	comps[0].AgentRole, comps[0].GoalID = "main", "g1"
	turns2 := append(turns, core.TurnRecord{
		Number: 2, AgentRole: "companion",
		TokenUsage: core.TurnTokenUsage{PromptN: 5, CacheRead: 40, CacheWrite: 10},
	})
	comps2 := append(comps, core.CompletionRecord{
		TurnNumber: 2, AgentRole: "companion", PromptN: 5, CacheRead: 40, CacheWrite: 10,
	})
	var b strings.Builder
	writeCacheView(&b, cacheTurnsFromHistory(turns2, nil), cacheCompletionsFromHistory(comps2),
		map[string]string{"g1": "tidy.falcon"})
	r := tui.NewMDStreamRenderer(80, tui.DarkTheme())
	rendered := ansi.Strip(strings.Join(r.Render(b.String()), "\n"))
	if !strings.Contains(rendered, "────") {
		t.Fatalf("rendered output lacks the horizontal rule line:\n%s", rendered)
	}
}

// --- Cache-miss shape classification (bugs.md 2026-08-30, plan F3/F4):
// the P7 no-tools collapse bust is a distinct report kind -----------------

// TestScanMissesNoToolsStepClassification verifies the third miss kind: a
// bust on a text-only-collapse call (request carried no tools + tool_choice
// "none" — the turn's final summary round) keeps its recomputed tokens but
// classifies as a NO-TOOLS STEP — never unexpected — mirroring the RCA
// export shape (read 80,768 → 0 with an intact message prefix).
func TestScanMissesNoToolsStepClassification(t *testing.T) {
	series := []cacheTurn{
		{Num: 1, PromptN: 10, CacheRead: 80768},      // prefix established
		{Num: 1, PromptN: 1, TextOnlyCollapse: true}, // collapse round: read 0 BY DESIGN
	}
	scans := scanMisses(series)
	if scans[1].Missed != 80768 {
		t.Errorf("step 2 missed = %d, want 80768 (the recomputed prefix stays visible)", scans[1].Missed)
	}
	if scans[1].Unexpected() {
		t.Error("collapse bust must NOT classify unexpected (intentional request-shape change)")
	}
	if scans[1].Partial() {
		t.Error("collapse bust must NOT classify partial")
	}
	if !scans[1].NoToolsStep() {
		t.Error("collapse bust must classify as a no-tools step")
	}
	if scans[0].NoToolsStep() || scans[0].Unexpected() {
		t.Errorf("step 1 (prefix establishment) must not classify at all: %+v", scans[0])
	}
}

// TestScanMissesNoToolsStepPartialShape verifies the kind also covers the
// partial shape: a no-tools call whose read shrank without vanishing (tool
// schemas gone from the prefix, message head still served) classifies as a
// no-tools step, not partial.
func TestScanMissesNoToolsStepPartialShape(t *testing.T) {
	series := []cacheTurn{
		{Num: 1, PromptN: 10, CacheRead: 80768},
		{Num: 1, PromptN: 1, CacheRead: 60000, TextOnlyCollapse: true},
	}
	scans := scanMisses(series)
	if scans[1].Missed != 20768 {
		t.Errorf("step 2 missed = %d, want 20768", scans[1].Missed)
	}
	if !scans[1].NoToolsStep() || scans[1].Partial() || scans[1].Unexpected() {
		t.Errorf("shrunken no-tools call must classify no-tools-step only: %+v", scans[1])
	}
}

// TestScanMissesNoToolsStepNormalBustUnchanged pins the F4 non-regression:
// a bust on a NORMAL call (no collapse flag) still classifies unexpected.
func TestScanMissesNoToolsStepNormalBustUnchanged(t *testing.T) {
	series := []cacheTurn{
		{Num: 1, PromptN: 10, CacheRead: 80768},
		{Num: 1, PromptN: 1, CacheRead: 0}, // plain bust: no flag
	}
	scans := scanMisses(series)
	if !scans[1].Unexpected() {
		t.Errorf("normal bust must still classify unexpected: %+v", scans[1])
	}
	if scans[1].NoToolsStep() {
		t.Error("normal bust must not classify as a no-tools step")
	}
}

// TestTotalMissedTokensNoToolsKind verifies the Global statistics fold:
// no-tools events keep their tokens in the total but count under their own
// NoTools counter — never under Unexpected/Partial (same rationale as
// intentional resets, minus the baseline restart: the loss is real, just
// intentional).
func TestTotalMissedTokensNoToolsKind(t *testing.T) {
	cases := []struct {
		name   string
		series []cacheTurn
		tot    missTotals
	}{
		{"collapse bust is its own kind in the headline", []cacheTurn{
			{Num: 1, PromptN: 10, CacheRead: 80768},
			{Num: 1, PromptN: 1, CacheRead: 0, TextOnlyCollapse: true},
		}, missTotals{Tokens: 80768, Events: 1, NoTools: 1}},
		{"mixed: unexpected and no-tools stay separate counters", []cacheTurn{
			{Num: 1, PromptN: 10, CacheRead: 5000},
			{Num: 2, PromptN: 1, CacheRead: 0},                         // real unexpected bust
			{Num: 2, PromptN: 10, CacheRead: 3000},                     // re-warm
			{Num: 2, PromptN: 1, CacheRead: 0, TextOnlyCollapse: true}, // collapse bust
		}, missTotals{Tokens: 8000, Events: 2, Unexpected: 1, NoTools: 1}},
	}
	for _, tc := range cases {
		if got := totalMissedTokens(tc.series); got != tc.tot {
			t.Errorf("%s: totals = %+v, want %+v", tc.name, got, tc.tot)
		}
	}
}

// TestWriteCacheMissListNoToolsKind verifies the misses table renders the
// distinct kind label end-to-end.
func TestWriteCacheMissListNoToolsKind(t *testing.T) {
	var b strings.Builder
	writeCacheMissList(&b, []cacheTurn{
		{Num: 1, PromptN: 10, CacheRead: 80768},
		{Num: 1, PromptN: 1, CacheRead: 0, TextOnlyCollapse: true},
	})
	out := b.String()
	if !strings.Contains(out, "| T1 | no-tools step | 100.0% | 80,768 |") {
		t.Errorf("miss list must render the no-tools step kind:\n%s", out)
	}
	if strings.Contains(out, "unexpected") {
		t.Errorf("collapse bust must not render as unexpected:\n%s", out)
	}
}

// TestMissedTokensLineNoToolsSegment verifies the headline names no-tools
// events in their own segment and that sessions without them keep the exact
// established format.
func TestMissedTokensLineNoToolsSegment(t *testing.T) {
	line := missedTokensLine(missTotals{Tokens: 80768, Events: 1, NoTools: 1})
	if !strings.Contains(line, "80,768 across 1 exchange(s) (0 unexpected, 0 partial, 1 no-tools)") {
		t.Errorf("headline must name the no-tools event: %q", line)
	}
	line = missedTokensLine(missTotals{Tokens: 8000, Events: 1, Unexpected: 1})
	if !strings.Contains(line, "(1 unexpected, 0 partial)") || strings.Contains(line, "no-tools") {
		t.Errorf("established headline format changed: %q", line)
	}
}

// TestCacheSeriesMappingTextOnlyCollapse verifies the flag plumbing into the
// view: the completion log carries TextOnlyCollapse into the cache series,
// while the legacy turn series (no per-call provenance) never invents it.
func TestCacheSeriesMappingTextOnlyCollapse(t *testing.T) {
	comps := []core.CompletionRecord{
		{TurnNumber: 1, PromptN: 10, CacheRead: 80768},
		{TurnNumber: 1, PromptN: 1, TextOnlyCollapse: true},
	}
	series := cacheCompletionsFromHistory(comps)
	if series[0].TextOnlyCollapse || !series[1].TextOnlyCollapse {
		t.Errorf("completion→cacheTurn mapping lost the collapse flag: %+v", series)
	}
	turns := cacheTurnsFromHistory(cacheTurns([3]int{10, 80768, 0}), nil)
	if turns[0].TextOnlyCollapse {
		t.Error("turn series must not invent a collapse flag")
	}
}

// TestShowCacheStatsNoToolsScenario replays the RCA export scenario
// end-to-end through the rendered view: established prefix (read 80,768),
// then the P7 collapse bust (read → 0, flag set), then the next turn
// re-warming. The misses table must answer "real miss or switch of context"
// at a glance: the bust shows as "no-tools step", the unexpected counters
// stay at zero, and the headline keeps the recomputed tokens visible.
func TestShowCacheStatsNoToolsScenario(t *testing.T) {
	rec := &fakeSessionRecorder{
		history: []core.TurnRecord{
			{Number: 1, AgentRole: "main", TokenUsage: core.TurnTokenUsage{PromptN: 10, CacheRead: 80768}},
			{Number: 2, AgentRole: "main", TokenUsage: core.TurnTokenUsage{PromptN: 10, CacheRead: 60000}},
		},
		completions: []core.CompletionRecord{
			{TurnNumber: 1, AgentRole: "main", PromptN: 10, CacheRead: 80768},
			{TurnNumber: 1, AgentRole: "main", PromptN: 1, TextOnlyCollapse: true},
			{TurnNumber: 2, AgentRole: "main", PromptN: 10, CacheRead: 60000},
		},
	}
	w := newWriter()
	if err := showCacheStats(w, rec, nil); err != nil {
		t.Fatalf("showCacheStats: %v", err)
	}
	report := w.Text()
	if !strings.Contains(report, "| T1 | no-tools step | 100.0% | 80,768 |") {
		t.Errorf("misses table lacks the no-tools step row:\n%s", report)
	}
	if !strings.Contains(report, "(0 unexpected, 0 partial, 1 no-tools)") {
		t.Errorf("global headline must exclude the no-tools event from unexpected/partial:\n%s", report)
	}
	if strings.Contains(report, "| T1 | unexpected |") {
		t.Errorf("collapse bust must not file under unexpected:\n%s", report)
	}
}

// TestShowCacheStatsNoToolsScenarioMDPipeline runs the same RCA scenario
// through the MD pipeline (the way /stats:cache actually renders on screen)
// and verifies the distinct kind label survives rendering in the misses
// table (plan validation step 4).
func TestShowCacheStatsNoToolsScenarioMDPipeline(t *testing.T) {
	rec := &fakeSessionRecorder{
		history: []core.TurnRecord{
			{Number: 1, AgentRole: "main", TokenUsage: core.TurnTokenUsage{PromptN: 10, CacheRead: 80768}},
		},
		completions: []core.CompletionRecord{
			{TurnNumber: 1, AgentRole: "main", PromptN: 10, CacheRead: 80768},
			{TurnNumber: 1, AgentRole: "main", PromptN: 1, TextOnlyCollapse: true},
		},
	}
	w := newWriter()
	if err := showCacheStats(w, rec, nil); err != nil {
		t.Fatalf("showCacheStats: %v", err)
	}
	r := tui.NewMDStreamRenderer(80, tui.DarkTheme())
	rendered := strings.Join(r.Render(w.Text()), "\n")
	if !strings.Contains(rendered, "no-tools step") {
		t.Errorf("rendered misses table lacks the no-tools step kind:\n%s", rendered)
	}
	// The ONLY legitimate "unexpected" mention is the headline's zero count
	// — no miss row may classify the collapse bust unexpected.
	if n := strings.Count(rendered, "unexpected"); n != 1 || !strings.Contains(rendered, "(0 unexpected") {
		t.Errorf("rendered report must classify the collapse bust only as no-tools step:\n%s", rendered)
	}
	if !strings.Contains(rendered, "80,768") {
		t.Errorf("rendered report must keep the recomputed tokens visible:\n%s", rendered)
	}
}
