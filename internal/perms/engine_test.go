// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package perms

import "testing"

func TestEngineEvaluate(t *testing.T) {
	eng := NewEngine([]Rule{
		{Pattern: "bash", Decision: DecisionAsk},
		{Pattern: "mcp__*__*", Decision: DecisionAllow},
		{Pattern: "mcp__dangerous__*", Decision: DecisionDeny},
		{Pattern: "write", Decision: DecisionAsk, Mode: "confirm"},
	})

	cases := []struct {
		toolName string
		mode     string
		dec      Decision
		matched  bool
	}{
		{"bash", "", DecisionAsk, true},
		{"mcp__fs__read", "", DecisionAllow, true},
		{"mcp__dangerous__rm", "", DecisionDeny, true}, // more specific deny wins
		{"read", "", "", false},
		{"write", "confirm", DecisionAsk, true},
		{"write", "yolo", "", false}, // mode-scoped rule does not apply
	}

	for _, tc := range cases {
		got := eng.Evaluate(tc.toolName, tc.mode)
		if got.Matched != tc.matched {
			t.Errorf("Evaluate(%q, %q).Matched = %v, want %v", tc.toolName, tc.mode, got.Matched, tc.matched)
		}
		if got.Matched && got.Decision != tc.dec {
			t.Errorf("Evaluate(%q, %q).Decision = %q, want %q", tc.toolName, tc.mode, got.Decision, tc.dec)
		}
	}
}

func TestEngineSpecificityOrdering(t *testing.T) {
	// Less specific allow is declared first, but the more specific deny
	// should be evaluated first and win.
	eng := NewEngine([]Rule{
		{Pattern: "mcp__*__*", Decision: DecisionAllow},
		{Pattern: "mcp__fs__rm", Decision: DecisionDeny},
	})
	got := eng.Evaluate("mcp__fs__rm", "")
	if !got.Matched || got.Decision != DecisionDeny {
		t.Errorf("expected deny for specific rule, got %+v", got)
	}
}

func TestEngineEmpty(t *testing.T) {
	eng := NewEngine(nil)
	got := eng.Evaluate("bash", "")
	if got.Matched {
		t.Error("empty engine should not match")
	}
}
