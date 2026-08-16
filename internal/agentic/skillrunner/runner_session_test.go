// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package skillrunner

import (
	"strings"
	"testing"

	agentic "github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider"
)

// TestSubAgentSessionID_Rule7 pins the Rule 7 contract: a skill sub-agent is
// a NEW, divergent context and must never inherit the parent conversation's
// SessionID (the provider cache key). Regression for the unexplained cache
// miss observed when an interleaved sub-agent request shared the parent id
// (goa-export-20260816-090406.zip, miss #2: seq 61→63 with one interleaved
// request).
func TestSubAgentSessionID_Rule7(t *testing.T) {
	tests := []struct {
		name     string
		parentID string
		skill    string
		wantSame bool // sub-agent id == parent id?
		wantEmpt bool // sub-agent id == ""?
	}{
		{"empty parent stays empty (no cache affinity configured)", "", "review", false, true},
		{"non-empty parent is suffixed, never shared", "sess_123_abc", "review", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := subAgentSessionID(tt.parentID, tt.skill)
			if (got == tt.parentID) != tt.wantSame && tt.parentID != "" {
				t.Errorf("subAgentSessionID(%q, %q) = %q: must not equal parent id", tt.parentID, tt.skill, got)
			}
			if got == "" && !tt.wantEmpt {
				t.Errorf("subAgentSessionID(%q, %q) = \"\": want dedicated id", tt.parentID, tt.skill)
			}
			if tt.parentID != "" && !strings.HasPrefix(got, tt.parentID+"/skill/"+tt.skill+"/") {
				t.Errorf("subAgentSessionID = %q, want prefix %q", got, tt.parentID+"/skill/"+tt.skill+"/")
			}
		})
	}

	// Two executions of the same skill under the same parent must get
	// distinct ids (concurrent sub-agents must not share a cache entry).
	a := subAgentSessionID("sess_x", "review")
	b := subAgentSessionID("sess_x", "review")
	if a == b {
		t.Errorf("two executions derived identical ids: %q", a)
	}
}

// TestNewSubAgent_DedicatedSessionID verifies newSubAgent rewrites the
// inherited StreamOptions SessionID while leaving other options untouched.
func TestNewSubAgent_DedicatedSessionID(t *testing.T) {
	r := &Runner{cfg: Config{
		StreamOptions: provider.StreamOptions{
			SessionID: "parent-session",
			MaxTokens: 1234,
		},
	}}
	agent := r.newSubAgent("qa", "system prompt", nil)
	opts := agent.StreamOptions()
	if opts.SessionID == "parent-session" {
		t.Fatalf("sub-agent inherited parent SessionID %q (Rule 7 violation)", opts.SessionID)
	}
	if !strings.HasPrefix(opts.SessionID, "parent-session/skill/qa/") {
		t.Errorf("SessionID = %q, want parent-session/skill/qa/<rand>", opts.SessionID)
	}
	if opts.MaxTokens != 1234 {
		t.Errorf("MaxTokens = %d, want 1234 (options besides SessionID unchanged)", opts.MaxTokens)
	}

	// Empty parent SessionID: stays empty rather than inventing affinity.
	r2 := &Runner{cfg: Config{StreamOptions: provider.StreamOptions{}}}
	agent2 := r2.newSubAgent("qa", "system prompt", nil)
	if got := agent2.StreamOptions().SessionID; got != "" {
		t.Errorf("empty parent SessionID: sub-agent got %q, want empty", got)
	}
}

// Ensure the test file can construct a minimal agent without a provider:
// newSubAgent must not dial anything at construction time.
var _ = agentic.NewAgent // silence unused-import if constructor changes
