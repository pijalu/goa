package tui

import "testing"

// hashColor must never panic, even for labels whose signed hash would overflow
// and produce a negative modulo (e.g. "fix login bug" previously → palette[-4]).
func TestHashColor_NeverNegative(t *testing.T) {
	labels := []string{
		"fix login bug", "coder", "explore", "plan", "reviewer",
		"a very long sub-agent description that will overflow the signed accumulator",
		"", "x", "日本語ラベル",
	}
	for _, l := range labels {
		got := hashColor(l) // must not panic
		if got == "" {
			t.Errorf("hashColor(%q) returned empty", l)
		}
	}
}
