// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
)

// compactEvent builds an EventCompact with the structured payload.
func compactEvent(strategy string, before, after, freed, removed int, detail string) *agentic.OutputEvent {
	return &agentic.OutputEvent{
		Type: agentic.EventCompact,
		Text: strategy,
		Compaction: &agentic.CompactionInfo{
			Strategy:    strategy,
			BeforePct:   before,
			AfterPct:    after,
			FreedTokens: freed,
			Removed:     removed,
			Detail:      detail,
		},
	}
}

// TestHandleAgentOutputEvent_CompactRendersBubble verifies an EventCompact
// surfaces a conversation bubble naming the strategy ("context
// compressions are invisible").
func TestHandleAgentOutputEvent_CompactRendersBubble(t *testing.T) {
	app := New(testSubsystems())
	app.handleAgentOutputEvent(compactEvent("ceiling", 95, 43, 105689, 238, ""))

	cv := app.subs.chat
	if !containsRendered(cv, "Context compacted (ceiling)") {
		t.Errorf("expected a compaction bubble naming the strategy, rendered:\n%s", strings.Join(cv.Render(80), "\n"))
	}
	if !containsRendered(cv, "95% → 43%") {
		t.Errorf("expected before→after percentages in the bubble")
	}
	if !containsRendered(cv, "238 messages dropped") {
		t.Errorf("expected the dropped-message count in the bubble")
	}
}

// TestRecordCompact_ClassifiesNewStrategies verifies the footer classifier:
// ceiling/selective/hybrid/etc. count as regular compacts, micro as a
// micro-compact.
func TestRecordCompact_ClassifiesNewStrategies(t *testing.T) {
	app := New(testSubsystems())
	app.recordCompact(compactEvent("ceiling", 95, 43, 0, 10, ""))
	app.recordCompact(compactEvent("selective", 90, 50, 0, 5, ""))
	app.recordCompact(compactEvent("micro", 80, 70, 0, 0, ""))

	if app.compacts != 2 {
		t.Errorf("compacts = %d, want 2 (ceiling + selective)", app.compacts)
	}
	if app.microCompacts != 1 {
		t.Errorf("microCompacts = %d, want 1 (micro)", app.microCompacts)
	}
}

// TestRecordCompact_FallsBackToTextLabel verifies events without a structured
// payload (legacy paths) still classify via the free-text label.
func TestRecordCompact_FallsBackToTextLabel(t *testing.T) {
	app := New(testSubsystems())
	app.recordCompact(&agentic.OutputEvent{Type: agentic.EventCompact, Text: "micro"})
	if app.microCompacts != 1 {
		t.Errorf("microCompacts = %d, want 1 via Text fallback", app.microCompacts)
	}
}

// TestRecordCompact_AppendsCompactionRound verifies each EventCompact appends
// a per-round record copied into sessionStats by buildFooterStatsLocked.
func TestRecordCompact_AppendsCompactionRound(t *testing.T) {
	app := New(testSubsystems())
	app.recordCompact(compactEvent("ceiling", 95, 43, 105689, 238, ""))
	app.recordCompact(compactEvent("micro", 60, 55, 1200, 0, ""))

	app.statsMu.Lock()
	st := app.buildFooterStatsLocked()
	app.statsMu.Unlock()

	if len(st.Compactions) != 2 {
		t.Fatalf("sessionStats.Compactions = %d rounds, want 2", len(st.Compactions))
	}
	first := st.Compactions[0]
	if first.Strategy != "ceiling" || first.BeforePct != 95 || first.AfterPct != 43 ||
		first.FreedTokens != 105689 || first.Removed != 238 {
		t.Errorf("round 0 = %+v, want ceiling 95→43 freed=105689 removed=238", first)
	}
	if first.At.IsZero() {
		t.Errorf("round 0 At must be stamped, got zero time")
	}
	if st.Compactions[1].Strategy != "micro" {
		t.Errorf("round 1 strategy = %q, want micro", st.Compactions[1].Strategy)
	}
	// Aggregate counters still feed the footer.
	if st.Compacts != 1 || st.MicroCompacts != 1 {
		t.Errorf("aggregates = %d compacts / %d micro, want 1/1", st.Compacts, st.MicroCompacts)
	}
}

// TestClearStats_ResetsCompactionRounds verifies /clear starts compaction
// bookkeeping fresh.
func TestClearStats_ResetsCompactionRounds(t *testing.T) {
	app := New(testSubsystems())
	app.recordCompact(compactEvent("ceiling", 95, 43, 0, 10, ""))
	app.clearStats()

	if app.compacts != 0 || app.microCompacts != 0 {
		t.Errorf("counters not reset: compacts=%d micro=%d", app.compacts, app.microCompacts)
	}
	if len(app.compactions) != 0 {
		t.Errorf("compactions not reset: %d rounds remain", len(app.compactions))
	}
}

// TestFormatFooterStats_CompactCounter verifies the footer c:… segment appears
// once any compression ran.
func TestFormatFooterStats_CompactCounter(t *testing.T) {
	stats := formatFooterStats(sessionStats{MicroCompacts: 0, Compacts: 2})
	if !strings.Contains(stats, "c:0m-2") {
		t.Errorf("expected footer compact counter c:0m-2, got %q", stats)
	}
	// No compressions → no segment.
	none := formatFooterStats(sessionStats{})
	if strings.Contains(none, "c:") {
		t.Errorf("expected no compact counter with zero compressions, got %q", none)
	}
}

// TestFormatCompactionBubble_NoPayload verifies a bare EventCompact (no
// structured payload) still renders a readable bubble.
func TestFormatCompactionBubble_NoPayload(t *testing.T) {
	got := formatCompactionBubble(&agentic.OutputEvent{Type: agentic.EventCompact, Text: "micro"})
	if !strings.Contains(got, "Context compacted (micro)") {
		t.Errorf("bubble = %q, want strategy label", got)
	}
}

// TestFormatCompactionBubble_DetailOnNewLine verifies the summarize detail is
// carried on a second line.
func TestFormatCompactionBubble_DetailOnNewLine(t *testing.T) {
	got := formatCompactionBubble(compactEvent("summarize", 90, 10, 0, 40, "Summary of conversation"))
	if !strings.Contains(got, "Context compacted (summarize)") {
		t.Errorf("bubble = %q, want summarize label", got)
	}
	if !strings.Contains(got, "\nSummary of conversation") {
		t.Errorf("bubble = %q, want detail on a new line", got)
	}
}

// TestCompactionStrategy_StructuredWinsOverText verifies the resolver prefers
// the structured payload when Text and Compaction.Strategy disagree.
func TestCompactionStrategy_StructuredWinsOverText(t *testing.T) {
	ev := &agentic.OutputEvent{
		Type:       agentic.EventCompact,
		Text:       "stale-text-label",
		Compaction: &agentic.CompactionInfo{Strategy: "micro"},
	}
	if got := compactionStrategy(ev); got != "micro" {
		t.Errorf("compactionStrategy = %q, want structured payload to win", got)
	}
}

// TestRecordCompact_ClassifiesElisionLabels verifies both the canonical
// "tool_elision" label and the legacy "elision" label (emitted before the
// strategy rename, still present in old session logs) classify as full
// compacts — elision is NOT micro compaction.
func TestRecordCompact_ClassifiesElisionLabels(t *testing.T) {
	for _, label := range []string{"tool_elision", "elision"} {
		t.Run(label, func(t *testing.T) {
			app := New(testSubsystems())
			app.recordCompact(compactEvent(label, 90, 60, 0, 0, ""))
			if app.compacts != 1 || app.microCompacts != 0 {
				t.Errorf("label %q: compacts=%d micro=%d, want 1/0", label, app.compacts, app.microCompacts)
			}
			if got := app.compactions[0].Strategy; got != label {
				t.Errorf("round strategy = %q, want %q (recorded verbatim)", got, label)
			}
		})
	}
}

// TestHeadlessRecordCompact_MatchesAppClassification verifies the headless
// path reads the structured payload (not just Text) and appends per-round
// records exactly like App.recordCompact.
func TestHeadlessRecordCompact_MatchesAppClassification(t *testing.T) {
	h := &HeadlessApp{}
	// Text deliberately disagrees with the structured strategy: headless must
	// trust the payload like the TUI does.
	h.recordCompact(&agentic.OutputEvent{
		Type:       agentic.EventCompact,
		Text:       "anything",
		Compaction: &agentic.CompactionInfo{Strategy: "micro", BeforePct: 80, AfterPct: 70},
	})
	h.recordCompact(compactEvent("tool_elision", 90, 60, 500, 0, ""))

	if h.microCompacts != 1 || h.compacts != 1 {
		t.Errorf("headless counters = %dm/%d, want 1m/1", h.microCompacts, h.compacts)
	}
	st := h.buildStatsLocked()
	if len(st.Compactions) != 2 {
		t.Fatalf("headless Compactions = %d rounds, want 2", len(st.Compactions))
	}
	if st.Compactions[0].Strategy != "micro" || st.Compactions[1].Strategy != "tool_elision" {
		t.Errorf("round strategies = %q,%q, want micro,tool_elision",
			st.Compactions[0].Strategy, st.Compactions[1].Strategy)
	}
}
