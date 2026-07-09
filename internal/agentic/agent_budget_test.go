// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import "testing"

func TestIsGuardrailResult(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"budget exceeded", "[goa-system] Tool call budget exceeded", true},
		{"duplicate hint", "[goa-system] This exact tool call (same tool with same arguments) was already executed this turn", true},
		{"loop guardrail", "[goa-system] Loop guardrail: repeated too many times", true},
		{"real tool result", "written", false},
		{"error result", "Error: file not found", false},
		{"whitespace budget", "  [goa-system] Tool call budget exceeded  ", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsGuardrailResult(tt.text); got != tt.want {
				t.Errorf("IsGuardrailResult(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}
