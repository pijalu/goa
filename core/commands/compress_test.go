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
			MaxTokens:        1_000_000,
			ThresholdPercent: 80,
			Strategy:         agentic.CompressionMicro,
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
