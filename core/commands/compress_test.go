// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/agentic"
)

func TestParseCompressArgs_DefaultForce(t *testing.T) {
	strategy, force, err := parseCompressArgs(nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if strategy != "" {
		t.Errorf("strategy = %q, want empty", strategy)
	}
	if !force {
		t.Error("manual /compress must default to force=true")
	}
}

func TestParseCompressArgs_StrategyOverride(t *testing.T) {
	strategy, force, err := parseCompressArgs([]string{"micro"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if strategy != "micro" {
		t.Errorf("strategy = %q, want micro", strategy)
	}
	if !force {
		t.Error("force must remain true when overriding strategy")
	}
}

func TestParseCompressArgs_UnknownStrategy(t *testing.T) {
	if _, _, err := parseCompressArgs([]string{"bogus"}); err == nil {
		t.Error("expected error for unknown strategy")
	}
}

func TestParseCompressArgs_NoForceOptOut(t *testing.T) {
	strategy, force, err := parseCompressArgs([]string{"micro", "noforce"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if strategy != "micro" {
		t.Errorf("strategy = %q, want micro", strategy)
	}
	if force {
		t.Error("noforce should opt out of forced compression")
	}
}

func TestParseCompressArgs_ForceKeyword(t *testing.T) {
	strategy, force, err := parseCompressArgs([]string{"summarize", "--force"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if strategy != "summarize" {
		t.Errorf("strategy = %q, want summarize", strategy)
	}
	if !force {
		t.Error("--force should keep force=true")
	}
}

func TestIsKnownStrategy(t *testing.T) {
	known := []string{"tool_elision", "selective", "summarize", "hybrid", "micro"}
	for _, s := range known {
		if !isKnownStrategy(s) {
			t.Errorf("isKnownStrategy(%q) = false, want true", s)
		}
	}
	if isKnownStrategy("bogus") {
		t.Error("isKnownStrategy(bogus) = true, want false")
	}
}

// TestCompressCommand_Run_AppliesForcedMicro verifies that /compress invokes
// the agent's forced compression path even when usage is well below the
// configured MinContextRatio threshold.
func TestCompressCommand_Run_AppliesForcedMicro(t *testing.T) {
	agent := agentic.NewAgent(agentic.Config{
		SystemPrompt: "You are helpful.",
		ContextCompression: agentic.ContextCompressionConfig{
			MaxTokens:  1_000_000,
			Thresholds: agentic.CompressionThresholds{SoftPercent: 80},
			Strategies: agentic.CompressionLayerStrategies{Soft: agentic.CompressionMicro},
			MicroCompaction: agentic.MicroCompactionConfig{
				KeepRecentMessages: 1,
				MinContentTokens:   1,
				MinContextRatio:    0.99, // force would be required to act on tiny history
				TruncatedMarker:    "[cleared]",
			},
		},
	})
	// History with an old, large tool result body that micro compaction
	// should clear when forced.
	agent.SetHistory([]agentic.Message{
		{Type: agentic.Content, Role: agentic.System, Content: "You are helpful."},
		{Type: agentic.Content, Role: agentic.User, Content: "run something"},
		{Type: agentic.Content, Role: agentic.Assistant, Content: ""},
		{Type: agentic.Content, Role: agentic.ToolRole, Content: strings.Repeat("x", 5000)},
		{Type: agentic.Content, Role: agentic.User, Content: "thanks"},
		{Type: agentic.Content, Role: agentic.Assistant, Content: "ok"},
	})

	am := core.NewAgentManager(&config.Config{}, nil, nil, nil, nil, "")
	am.SetActiveAgentForTest(agent)

	buf := &strings.Builder{}
	ctx := core.Context{Config: &config.Config{}, AgentManager: am, OutputBuffer: buf}

	before := agent.ContextStats().EstimatedTokens

	cmd := &CompressCommand{}
	if err := cmd.Run(ctx, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	after := agent.ContextStats().EstimatedTokens

	out := buf.String()
	if !strings.Contains(out, "micro") && !strings.Contains(out, "default") {
		t.Errorf("expected output to mention applied strategy, got: %s", out)
	}

	// The big tool result body must have been replaced by the forced compaction
	// and the reported token count must have dropped.
	if after >= before {
		t.Errorf("token count did not decrease: %d -> %d", before, after)
	}
	for _, m := range agent.GetHistory() {
		if m.Role == agentic.ToolRole && strings.Contains(m.Content, "xxxx") {
			t.Errorf("tool result body was not cleared by forced micro compaction")
		}
	}
}

// TestCompressCommand_NoAgentSession errors clearly when no agent is bound.
func TestCompressCommand_NoAgentSession(t *testing.T) {
	am := core.NewAgentManager(&config.Config{}, nil, nil, nil, nil, "")
	buf := &strings.Builder{}
	ctx := core.Context{Config: &config.Config{}, AgentManager: am, OutputBuffer: buf}
	cmd := &CompressCommand{}
	if err := cmd.Run(ctx, nil); err == nil {
		t.Error("expected error when no agent session exists")
	}
}

// TestReportCompression_Lifespan just guards the reporting helper signatures.
func TestReportCompression_Lifespan(t *testing.T) {
	buf := &strings.Builder{}
	ctx := core.Context{OutputBuffer: buf}
	before := &agentic.ContextStats{EstimatedTokens: 100, MaxTokens: 1000, UsagePercent: 10, Messages: 5}
	after := &agentic.ContextStats{EstimatedTokens: 60, MaxTokens: 1000, UsagePercent: 6, Messages: 5}
	reportCompression(ctx, "micro", before, after, 5*time.Millisecond)
	if !strings.Contains(buf.String(), "freed 40") {
		t.Errorf("expected freed tokens in output, got: %s", buf.String())
	}
}

// TestReportCompression_CoherentBasis reproduces the inconsistent /compress
// report from production: before the compression the projected occupancy was
// provider-anchored (UsagePercent=35 while EstimatedTokens/MaxTokens = 66%),
// and after it the anchor was invalidated so UsagePercent fell back to the raw
// heuristic (51% — equal to the estimate). The report must derive BOTH
// percentages from the same series as the printed token counts
// (EstimatedTokens), so "freed N tokens" can never sit next to a rising
// percentage that came from a different measurement basis.
func TestReportCompression_CoherentBasis(t *testing.T) {
	buf := &strings.Builder{}
	ctx := core.Context{OutputBuffer: buf}
	before := &agentic.ContextStats{
		EstimatedTokens: 695679,
		ProjectedTokens: 367002,
		MaxTokens:       1048576,
		UsagePercent:    35, // provider-anchored projection, pre-invalidation
		Messages:        4469,
	}
	after := &agentic.ContextStats{
		EstimatedTokens: 542317,
		ProjectedTokens: 542317,
		MaxTokens:       1048576,
		UsagePercent:    51, // heuristic fallback after invalidateContextUsageLocked
		Messages:        4469,
	}
	reportCompression(ctx, "", before, after, 10*time.Millisecond)

	out := buf.String()
	for _, want := range []string{
		"Context compressed (default):",
		"695679 → 542317 tokens (freed 153362)",
		"of 1048576 context window",
		"Messages: 4469 → 4469",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q, got: %s", want, out)
		}
	}
	// Percentages must match the printed token counts against the window:
	// 66% before (695679/1048576) and 51% after (542317/1048576).
	if !strings.Contains(out, " 66% → 51% of 1048576") {
		t.Errorf("usage line not estimate-coherent, got: %s", out)
	}
	// The stale provider-anchored before-percent must not resurface.
	if strings.Contains(out, "35%") {
		t.Errorf("output leaks the cross-basis 35%% figure, got: %s", out)
	}
}

// TestReportCompression_NothingToTrim pins the no-reduction branch to the same
// estimate-based percentage convention as the freed-tokens branch.
func TestReportCompression_NothingToTrim(t *testing.T) {
	buf := &strings.Builder{}
	ctx := core.Context{OutputBuffer: buf}
	stats := &agentic.ContextStats{
		EstimatedTokens: 500000,
		ProjectedTokens: 250000,
		MaxTokens:       1000000,
		UsagePercent:    25, // projection disagrees with the estimate on purpose
		Messages:        42,
	}
	reportCompression(ctx, "tool_elision", stats, stats, time.Millisecond)

	out := buf.String()
	if !strings.Contains(out, "(nothing to trim)") {
		t.Errorf("expected nothing-to-trim notice, got: %s", out)
	}
	if !strings.Contains(out, "50% usage") { // 500000/1000000, not the projected 25%
		t.Errorf("usage percent not derived from estimated tokens, got: %s", out)
	}
}

// TestPctOf covers the percentage helper's arithmetic and degenerate window.
func TestPctOf(t *testing.T) {
	tests := []struct {
		name   string
		tokens int
		max    int
		want   string
	}{
		{"zero window", 123456, 0, "?"},
		{"negative window", 1, -5, "?"},
		{"floor division", 54, 100, "54"},
		{"rounds down", 549, 1000, "54"},
		{"empty", 0, 1000, "0"},
		{"over full", 1100000, 1000000, "110"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pctOf(tt.tokens, tt.max); got != tt.want {
				t.Errorf("pctOf(%d, %d) = %q, want %q", tt.tokens, tt.max, got, tt.want)
			}
		})
	}
}
// TestCompressCommand_AsyncHint verifies that LLM-backed strategies (summarize,
// hybrid, default) opt into async execution while in-memory strategies stay
// synchronous.
func TestCompressCommand_AsyncHint(t *testing.T) {
	cmd := &CompressCommand{}
	tests := []struct {
		name      string
		args      []string
		wantAsync bool
		wantSub   string // substring expected in the label when async
	}{
		{"summarize", []string{"summarize"}, true, "summarize"},
		{"hybrid", []string{"hybrid"}, true, "hybrid"},
		{"default empty", nil, true, "Compressing"},
		{"default force", []string{"force"}, true, "Compressing"},
		{"tool_elision", []string{"tool_elision"}, false, ""},
		{"selective", []string{"selective"}, false, ""},
		{"micro", []string{"micro"}, false, ""},
		{"unknown", []string{"bogus"}, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			label := cmd.AsyncHint(tt.args)
			if tt.wantAsync && label == "" {
				t.Fatalf("AsyncHint(%v) = empty, want non-empty label", tt.args)
			}
			if !tt.wantAsync && label != "" {
				t.Fatalf("AsyncHint(%v) = %q, want empty (sync)", tt.args, label)
			}
			if tt.wantSub != "" && !strings.Contains(label, tt.wantSub) {
				t.Errorf("AsyncHint(%v) = %q, want it to contain %q", tt.args, label, tt.wantSub)
			}
		})
	}
}