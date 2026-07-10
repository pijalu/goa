// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
	orchpanel "github.com/pijalu/goa/tui/orchestrator"
)

// TestAggregateByRole_SumsAndUpgradesStatus verifies that multiple handles for
// the same role are collapsed into one row, summing counters and keeping the
// most active status.
func TestAggregateByRole_SumsAndUpgradesStatus(t *testing.T) {
	rows := []orchpanel.AgentEnhancedRow{
		{AgentID: "c-1", Role: "coder", Status: "finished", TokensIn: 10, TokensOut: 5, CacheRead: 1, CacheCreation: 2, ToolCalls: 1, Turns: 1},
		{AgentID: "c-2", Role: "coder", Status: "running", TokensIn: 20, TokensOut: 10, CacheRead: 3, CacheCreation: 4, ToolCalls: 2, Turns: 1},
		{AgentID: "r-1", Role: "reviewer", Status: "idle", TokensIn: 7, TokensOut: 3},
	}

	got := aggregateByRole(rows)
	if len(got) != 2 {
		t.Fatalf("expected 2 aggregated rows, got %d", len(got))
	}

	coder := got[0]
	if coder.Role != "coder" {
		t.Errorf("first role = %q, want coder", coder.Role)
	}
	if coder.TokensIn != 30 || coder.TokensOut != 15 {
		t.Errorf("coder tokens = %d/%d, want 30/15", coder.TokensIn, coder.TokensOut)
	}
	if coder.CacheRead != 4 || coder.CacheCreation != 6 {
		t.Errorf("coder cache = %d/%d, want 4/6", coder.CacheRead, coder.CacheCreation)
	}
	if coder.ToolCalls != 3 || coder.Turns != 2 {
		t.Errorf("coder toolCalls/turns = %d/%d, want 3/2", coder.ToolCalls, coder.Turns)
	}
	if coder.Status != "running" {
		t.Errorf("coder status = %q, want running", coder.Status)
	}

	reviewer := got[1]
	if reviewer.Role != "reviewer" {
		t.Errorf("second role = %q, want reviewer", reviewer.Role)
	}
	if reviewer.TokensIn != 7 {
		t.Errorf("reviewer tokensIn = %d, want 7", reviewer.TokensIn)
	}
}

// TestAggregateByRole_KeepsLatestContext verifies that the latest non-zero
// context estimate is retained when aggregating handles for the same role.
func TestAggregateByRole_KeepsLatestContext(t *testing.T) {
	rows := []orchpanel.AgentEnhancedRow{
		{AgentID: "c-1", Role: "coder", ContextEstimate: 100, ContextMax: 1000, ContextAutoMax: false},
		{AgentID: "c-2", Role: "coder", ContextEstimate: 200, ContextMax: 2000, ContextAutoMax: true},
	}
	got := aggregateByRole(rows)
	if len(got) != 1 {
		t.Fatalf("expected 1 row, got %d", len(got))
	}
	if got[0].ContextEstimate != 200 || got[0].ContextMax != 2000 || !got[0].ContextAutoMax {
		t.Errorf("context not updated from latest row: %+v", got[0])
	}
}

// TestFormatOrchAgentLine_RendersContextAndActiveColor verifies that a running
// agent line includes context usage and colors the model green.
func TestFormatOrchAgentLine_RendersContextAndActiveColor(t *testing.T) {
	r := orchpanel.AgentEnhancedRow{
		Role:            "coder",
		Model:           "gemma",
		Provider:        "google",
		Thinking:        "medium",
		Status:          "running",
		TokensIn:        1000,
		TokensOut:       500,
		CacheRead:       100,
		CacheCreation:   50,
		ToolCalls:       2,
		ContextEstimate: 50000,
		ContextMax:      1000000,
		ContextAutoMax:  true,
	}
	line := formatOrchAgentLine(r)
	stripped := ansi.Strip(line)

	if !strings.HasPrefix(stripped, "Coder: ") {
		t.Errorf("expected title-cased role prefix, got %q", stripped)
	}
	if !strings.Contains(stripped, "1.0M (auto)") {
		t.Errorf("expected context usage with auto flag, got %q", stripped)
	}
	if !strings.Contains(stripped, "google") || !strings.Contains(stripped, "gemma") {
		t.Errorf("expected provider/model, got %q", stripped)
	}
	if !strings.Contains(stripped, "medium") {
		t.Errorf("expected thinking level, got %q", stripped)
	}
	if !strings.Contains(line, ansi.Fg("#3fb950")) {
		t.Errorf("expected green active color for running agent, got %q", line)
	}
}

// TestFormatOrchAgentLine_IdleIsFaint verifies that an idle agent is rendered
// in the faint (dim) color rather than green.
func TestFormatOrchAgentLine_IdleIsFaint(t *testing.T) {
	r := orchpanel.AgentEnhancedRow{Role: "coder", Model: "gemma", Status: "idle"}
	line := formatOrchAgentLine(r)
	if strings.Contains(line, ansi.Fg("#3fb950")) {
		t.Errorf("idle agent should not be green, got %q", line)
	}
	if !strings.Contains(line, ansi.Faint) {
		t.Errorf("idle agent should be faint, got %q", line)
	}
}

// TestFormatOrchAgentLine_DropsOffThinking verifies that an "off" thinking
// level is omitted from the rendered line.
func TestFormatOrchAgentLine_DropsOffThinking(t *testing.T) {
	r := orchpanel.AgentEnhancedRow{Role: "coder", Model: "gemma", Status: "finished", Thinking: "off"}
	line := ansi.Strip(formatOrchAgentLine(r))
	if strings.Contains(line, "off") {
		t.Errorf("'off' thinking level should be omitted, got %q", line)
	}
}
