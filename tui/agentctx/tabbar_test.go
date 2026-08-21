// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentctx

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

// newTabBarScenario builds a registry with the given view ids and attaches it
// to a fresh tab bar.
func newTabBarScenario(ids ...string) (*AgentViewRegistry, *AgentTabBar) {
	reg := NewAgentViewRegistry()
	for _, id := range ids {
		tr := NewAgentTranscript(id)
		reg.Add(id, &AgentView{Transcript: tr, Compositor: tr.Compositor()})
	}
	return reg, NewAgentTabBar(reg)
}

// TestAgentTabBar_InvisibleBelowTwoViews pins the T1-preserving rule: with
// zero or one registered view the bar renders nil (zero rows), so the
// single-agent layout carries no tab strip and no extra row.
func TestAgentTabBar_InvisibleBelowTwoViews(t *testing.T) {
	_, bar := newTabBarScenario()
	if got := bar.Render(80); got != nil {
		t.Errorf("empty registry: Render = %v, want nil", got)
	}

	reg := NewAgentViewRegistry()
	tr := NewAgentTranscript(MainAgentID)
	reg.Add(MainAgentID, &AgentView{Transcript: tr, Compositor: tr.Compositor()})
	bar.SetRegistry(reg)
	if got := bar.Render(80); got != nil {
		t.Errorf("single view: Render = %v, want nil (T1 layout unchanged)", got)
	}
}

// TestAgentTabBar_RendersPerDelegationTabs is the plan's shape check: three
// views with the middle one active render
// "main │ coder·dlg-03 │ coder·dlg-07" with the active label bold and the
// [2/3] position indicator.
func TestAgentTabBar_RendersPerDelegationTabs(t *testing.T) {
	reg, bar := newTabBarScenario(MainAgentID, "dlg-coder-03", "dlg-coder-07")
	if _, ok := reg.SelectByID("dlg-coder-03"); !ok {
		t.Fatal("SelectByID failed")
	}

	lines := bar.Render(80)
	if len(lines) != 1 {
		t.Fatalf("Render returned %d lines, want exactly 1", len(lines))
	}
	line := lines[0]
	plain := ansi.Strip(line)

	for _, want := range []string{"main", "coder·dlg-03", "coder·dlg-07", "│", "[2/3]"} {
		if !strings.Contains(plain, want) {
			t.Errorf("tab bar missing %q: %q", want, plain)
		}
	}
	// Active tab (index 1) is bold; the others are not.
	if !strings.Contains(line, ansi.Bold+"coder·dlg-03") && !strings.Contains(line, "coder·dlg-03"+ansi.Reset) {
		t.Errorf("active tab label not bold-styled: %q", line)
	}
	boldRuns := strings.Count(line, ansi.Bold)
	if boldRuns != 1 {
		t.Errorf("expected exactly 1 bold tab (the active one), got %d in %q", boldRuns, line)
	}

	// Selecting the first tab updates the indicator and the bold label.
	reg.SelectByID(MainAgentID)
	line = bar.Render(80)[0]
	if !strings.Contains(ansi.Strip(line), "[1/3]") {
		t.Errorf("indicator after reselect = %q, want [1/3]", ansi.Strip(line))
	}
}

// TestAgentTabBar_LabelKeying pins the delegation-id → label mapping: main is
// "main"; a minted dlg-<role>-<NN> id renders as "<role>·dlg-<NN>"; anything
// else renders as-is.
func TestAgentTabBar_LabelKeying(t *testing.T) {
	cases := []struct{ id, want string }{
		{MainAgentID, "main"},
		{"dlg-coder-03", "coder·dlg-03"},
		{"dlg-planner-07", "planner·dlg-07"},
		{"dlg-coder-3", "coder·dlg-03"}, // unpadded seq normalizes
		{"companion", "companion"},      // unknown shape: identity
		{"dlg-x", "dlg-x"},              // malformed dlg id: identity
	}
	for _, c := range cases {
		if got := TabLabel(c.id); got != c.want {
			t.Errorf("TabLabel(%q) = %q, want %q", c.id, got, c.want)
		}
	}
}

// TestAgentTabBar_TracksRegistry verifies the bar is pull-based: adding a
// view mid-run grows the strip, and cycling moves the indicator.
func TestAgentTabBar_TracksRegistry(t *testing.T) {
	reg, bar := newTabBarScenario(MainAgentID, "dlg-coder-03")
	if got := bar.Render(80); !strings.Contains(ansi.Strip(got[0]), "[1/2]") {
		t.Fatalf("two views: %q, want [1/2]", ansi.Strip(got[0]))
	}
	tr := NewAgentTranscript("dlg-planner-01")
	reg.Add("dlg-planner-01", &AgentView{Transcript: tr, Compositor: tr.Compositor()})
	got := ansi.Strip(bar.Render(80)[0])
	if !strings.Contains(got, "planner·dlg-01") || !strings.Contains(got, "[1/3]") {
		t.Errorf("after Add: %q, want planner tab + [1/3]", got)
	}
	reg.Cycle(1)
	got = ansi.Strip(bar.Render(80)[0])
	if !strings.Contains(got, "[2/3]") {
		t.Errorf("after Cycle: %q, want [2/3]", got)
	}
}
