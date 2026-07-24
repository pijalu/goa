// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"encoding/json"
	"strings"
)

// bashUpstreamKey extracts a deduplication key for a bash command that ignores
// the trailing filter/pipe. Two invocations that share the same expensive
// upstream but differ only in the filter — e.g.
//
//	go test -count=1 -v . 2>&1 | grep -c "result mismatch"
//	go test -count=1 -v . 2>&1 | grep -c "table not found"
//
// — map to the same key ("go test -count=1 -v . 2>&1"), because the costly
// part (running the full test suite) is identical; only the cheap re-filter
// changed. The key is used ONLY to detect near-duplicate expensive runs and
// nudge the model toward "run once, save output, re-filter the file"; it never
// changes what actually executes.
func bashUpstreamKey(command string) string {
	// Take the segment before the first top-level pipe. This is a heuristic,
	// not a full shell parse — good enough for the dedup nudge and safe
	// (a false split only means we miss or fire a hint, never wrong output).
	if i := strings.Index(command, "|"); i >= 0 {
		command = command[:i]
	}
	// Normalize whitespace so trivial spacing differences don't split the key.
	return strings.Join(strings.Fields(command), " ")
}

// bashCommandFromArgs pulls the "command" field out of a bash tool's JSON
// arguments. Returns "" when the args are not a bash call or cannot be parsed.
func bashCommandFromArgs(arguments string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(arguments), &m); err != nil {
		return ""
	}
	if s, ok := m["command"].(string); ok {
		return s
	}
	return ""
}

// bashReuseTracker detects when the model re-runs the same expensive upstream
// command within a single state epoch (i.e. with no intervening file-mutating
// tool call) while only changing the trailing filter. It is reset whenever the
// state epoch advances, so re-running the same test command AFTER an edit —
// a legitimate workflow — is never flagged.
type bashReuseTracker struct {
	epoch int
	// seen counts how many times each upstream key has run in the current epoch.
	seen map[string]int
}

func newBashReuseTracker() *bashReuseTracker {
	return &bashReuseTracker{seen: make(map[string]int)}
}

// reset discards all tracked commands; called when the state epoch advances.
func (t *bashReuseTracker) reset(epoch int) {
	t.epoch = epoch
	t.seen = make(map[string]int)
}

// recordUpstream registers one execution of the given upstream key at the
// given epoch and reports whether this is a near-duplicate re-run (the same
// upstream already ran earlier in the SAME epoch). The first run in an epoch
// is never a duplicate. When the epoch differs from the tracker's current
// epoch, the tracker resets first (world changed → fresh observation).
func (t *bashReuseTracker) recordUpstream(upstream string, epoch int) bool {
	if upstream == "" {
		return false
	}
	if epoch != t.epoch {
		t.reset(epoch)
	}
	t.seen[upstream]++
	return t.seen[upstream] > 1
}

// nearDuplicateHint is appended (non-blocking) to a bash result when the model
// re-ran an expensive upstream command with only the filter changed. It teaches
// the cheaper save-once-refilter pattern for subsequent calls without blocking
// the current (legitimately needed) result.
const nearDuplicateHint = "\n\n[goa-system] Efficiency note: you re-ran the same base command with only the trailing filter changed. That re-executes the expensive upstream each time. Cheaper pattern: run it once and save the output (e.g. `cmd > /tmp/out.txt 2>&1`), then grep/count the saved file for each pattern (`grep -c X /tmp/out.txt`)."
