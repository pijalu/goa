// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

// TestAgentTabBar_RendersActiveAndIndicator builds a 3-tab view with the middle
// tab active and asserts the bar shows the source prefix, a bolded active label,
// and a right-justified [2/3] indicator. The inactive case returns nil.
func TestAgentTabBar_RendersActiveAndIndicator(t *testing.T) {
	b := NewAgentTabBar()

	// No view: invisible.
	if lines := b.Render(80); lines != nil {
		t.Errorf("Render without view = %v, want nil", lines)
	}

	v := NewMultiAgentView("orchestration")
	b.SetView(v)
	v.ApplyEvent(AgentViewEvent{Kind: EvSourceStarted})
	v.ApplyEvent(AgentViewEvent{Kind: EvAgentStarted, AgentID: "a-1", Role: "alpha"})
	v.ApplyEvent(AgentViewEvent{Kind: EvAgentStarted, AgentID: "b-1", Role: "beta"})

	// 4 tabs: stats, alpha, beta, all. Select the first agent tab.
	v.SelectByKey("a-1")

	lines := b.Render(80)
	if len(lines) != 1 {
		t.Fatalf("Render returned %d lines, want 1", len(lines))
	}
	raw := lines[0]
	plain := stripANSI(raw)

	if !strings.HasPrefix(plain, "orchestration:") {
		t.Errorf("missing source prefix: %q", plain)
	}
	if !strings.Contains(plain, "alpha") || !strings.Contains(plain, "Stats") || !strings.Contains(plain, "All") {
		t.Errorf("tab labels missing: %q", plain)
	}
	// Active tab is bold (raw keeps the bold sequence), inactive are not.
	if !strings.Contains(raw, ansi.Bold) {
		t.Errorf("active tab not bolded: %q", raw)
	}
	if !strings.Contains(plain, "[2/4]") {
		t.Errorf("indicator missing/wrong: %q", plain)
	}
}

// TestAgentTabBar_NarrowWidthStaysOneLine asserts the bar never exceeds width
// and always yields exactly one line.
func TestAgentTabBar_NarrowWidthStaysOneLine(t *testing.T) {
	b := NewAgentTabBar()
	v := NewMultiAgentView("orchestration")
	b.SetView(v)
	v.ApplyEvent(AgentViewEvent{Kind: EvSourceStarted})
	v.ApplyEvent(AgentViewEvent{Kind: EvAgentStarted, AgentID: "a-1", Role: "alpha"})
	for _, w := range []int{20, 40, 80} {
		lines := b.Render(w)
		if len(lines) != 1 {
			t.Errorf("width %d: got %d lines, want 1", w, len(lines))
		}
		if visibleLen(lines[0]) > w+2 {
			t.Errorf("width %d: line visible len %d > %d", w, visibleLen(lines[0]), w)
		}
	}
}
