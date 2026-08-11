// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// statsWithCompactions builds sessionStats carrying two compression rounds.
func statsWithCompactions() sessionStats {
	return sessionStats{
		PromptN:    100,
		PredictedN: 50,
		Compactions: []CompactionRound{
			{Strategy: "ceiling", BeforePct: 95, AfterPct: 43, FreedTokens: 105689, Removed: 238, At: time.Now()},
			{Strategy: "micro", BeforePct: 60, AfterPct: 55, FreedTokens: 1200, At: time.Now()},
		},
	}
}

// TestPlainRenderer_SummaryPrintsCompactionRounds verifies the headless
// --plain summary documents each compression round (bugs.md "context
// compressions are invisible").
func TestPlainRenderer_SummaryPrintsCompactionRounds(t *testing.T) {
	var buf bytes.Buffer
	r := newPlainRenderer(&buf)
	r.Summary(statsWithCompactions(), 3, 1500*time.Millisecond)

	out := buf.String()
	if !strings.Contains(out, "-- compression 1: ceiling 95%→43%") {
		t.Errorf("plain summary missing round 1 line, got:\n%s", out)
	}
	if !strings.Contains(out, "freed=105689") || !strings.Contains(out, "removed=238") {
		t.Errorf("plain summary missing freed/removed detail, got:\n%s", out)
	}
	if !strings.Contains(out, "-- compression 2: micro 60%→55%") {
		t.Errorf("plain summary missing round 2 line, got:\n%s", out)
	}
}

// TestANSIRenderer_SummaryPrintsCompactionRounds verifies the colored headless
// summary also documents each compression round.
func TestANSIRenderer_SummaryPrintsCompactionRounds(t *testing.T) {
	var buf bytes.Buffer
	r := newANSIRenderer(&buf)
	r.Summary(statsWithCompactions(), 3, 1500*time.Millisecond)

	out := buf.String()
	if !strings.Contains(out, "compression 1: ceiling 95%→43%") {
		t.Errorf("ansi summary missing round 1 line, got:\n%s", out)
	}
	if !strings.Contains(out, "compression 2: micro 60%→55%") {
		t.Errorf("ansi summary missing round 2 line, got:\n%s", out)
	}
}

// TestPlainRenderer_SummaryNoCompactionsNoLines verifies no compression lines
// are printed for a session that never compressed.
func TestPlainRenderer_SummaryNoCompactionsNoLines(t *testing.T) {
	var buf bytes.Buffer
	r := newPlainRenderer(&buf)
	r.Summary(sessionStats{PromptN: 10, PredictedN: 5}, 1, time.Millisecond)

	if strings.Contains(buf.String(), "compression") {
		t.Errorf("expected no compression lines for an uncompressed session, got:\n%s", buf.String())
	}
}
