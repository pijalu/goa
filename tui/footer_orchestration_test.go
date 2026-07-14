// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"
)

// TestFooter_RendersOrchestrationStatsAsPerAgentLines verifies that during
// orchestration the footer renders ONLY the per-agent lines, suppressing the
// normal chrome (workdir/mode and main stats/model). Each agent line carries
// its own role-prefixed stats + model.
func TestFooter_RendersOrchestrationStatsAsPerAgentLines(t *testing.T) {
	f := NewFooter()
	f.SetData(FooterData{
		Mode:  "yolo",
		Stats: "↑10 ↓5",
		Model: "(google) gemma",
		OrchestrationStats: "Coder: ↑40 ↓12 CH96.2% - (google) gemma\n" +
			"Reviewer: - (lmstudio) qwen • medium",
	})
	lines := f.Render(100)
	if len(lines) != 2 {
		t.Fatalf("expected exactly 2 footer lines during orchestration, got %d: %q", len(lines), lines)
	}
	joined := strings.Join(lines, "\n")
	stripped := stripANSI(joined)
	if strings.Contains(stripped, "YOLO") {
		t.Errorf("workdir/mode chrome should be suppressed during orchestration; got:\n%s", stripped)
	}
	if strings.Contains(stripped, "↑10 ↓5") {
		t.Errorf("main stats line should be suppressed during orchestration; got:\n%s", stripped)
	}
	if !strings.Contains(stripped, "Coder:") {
		t.Errorf("missing per-agent Coder line; got:\n%s", stripped)
	}
	if !strings.Contains(stripped, "Reviewer:") {
		t.Errorf("missing per-agent Reviewer line; got:\n%s", stripped)
	}
	if !strings.Contains(stripped, "CH96.2%") {
		t.Errorf("per-agent line should carry the cache-hit stat; got:\n%s", stripped)
	}
}

// TestFooter_IdleIsTwoLinesNoSpacer verifies that when no orchestration stats
// are present the footer renders exactly its two chrome lines with NO blank
// spacer. (A blank third line wasted the bottom terminal row forever — the
// "empty line at bottom" bug. The chat viewport fill absorbs any chrome-height
// change when orchestration starts/stops, so the spacer is unnecessary.)
func TestFooter_IdleIsTwoLinesNoSpacer(t *testing.T) {
	f := NewFooter()
	f.SetData(FooterData{Mode: "yolo", Model: "gemma"})
	idle := f.Render(80)
	if len(idle) != 2 {
		t.Fatalf("idle footer should be 2 chrome lines (no spacer), got %d: %q", len(idle), idle)
	}
	for i, l := range idle {
		if strings.TrimSpace(stripANSI(l)) == "" {
			t.Errorf("idle footer line %d is blank; footer must not emit blank lines", i)
		}
	}
}
