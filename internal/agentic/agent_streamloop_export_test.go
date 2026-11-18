// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"os"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// TestStreamLoop_Export20261118ShortCommandCycle reproduces the final response
// in goa-export-20261118-154608.zip. The model rotated through short commands
// ("Emit", "Go", "Now", "Final", ...), so no exact unit or individual
// three-word shingle reached the configured threshold of 15. The response was
// cancelled by the user after 97 seconds without a stream-loop detection.
func TestStreamLoop_Export20261118ShortCommandCycle(t *testing.T) {
	text := readStreamLoopExport20261118(t)
	if !streamLoopWouldDetect(text, 15) {
		t.Fatal("exported short-command cycle was not detected")
	}
}

func TestStreamLoop_Export20261118ShortCommandCycleStopsTurn(t *testing.T) {
	text := readStreamLoopExport20261118(t)
	p := newStrikeProvider("test-export-short-command", scriptedStreamStep{
		events: []provider.AssistantMessageEvent{{Type: provider.EventTextDelta, Delta: text}},
	})
	_, _, err := runStrikeTurn(t, p, Config{
		StreamLoopMaxRepeats: func() int { return 15 },
		StreamLoopMaxStrikes: 1,
	})
	if err == nil || !strings.Contains(err.Error(), "stream loop detected") {
		t.Fatalf("turn error = %v, want automatic stream-loop stop", err)
	}
	if p.Calls() != 1 {
		t.Fatalf("provider calls = %d, want 1", p.Calls())
	}
}

func readStreamLoopExport20261118(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("testdata/streamloop_20261118.txt")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimPrefix(string(data), "Loop:\n ")
}
