// SPDX-License-Identifier: GPL-3.0-or-later
package agentic

import (
	"testing"
	"time"
)

func TestDecideCompactionPolicyExhaustive(t *testing.T) {
	base := CompactionPolicyInput{MaxTokens: 1000, SoftPercent: 50, HighMarkPercent: 80, HardPercent: 95, SoftStrategyAvailable: true, HighMarkStrategyAvailable: true}
	cases := []struct {
		name string
		in   CompactionPolicyInput
		want CompactionPolicyDecision
	}{
		{"below marks", base, Noop},
		{"soft boundary cold", withUsage(base, 500), SoftMaintenance},
		{"soft boundary hot", withUsage(base, 500, func(in *CompactionPolicyInput) { in.CacheHot = true }), Noop},
		{"high mark", withUsage(base, 800), HighMarkCompaction},
		{"hard boundary", withUsage(base, 950, func(in *CompactionPolicyInput) { in.CacheHot = true }), EmergencyFallback},
		{"above hard", withUsage(base, 1200), EmergencyFallback},
		{"soft unavailable", withUsage(base, 500, func(in *CompactionPolicyInput) { in.SoftStrategyAvailable = false }), Noop},
		{"high unavailable", withUsage(base, 800, func(in *CompactionPolicyInput) {
			in.HighMarkStrategyAvailable = false
			in.SoftStrategyAvailable = false
		}), Noop},
		{"zero max", CompactionPolicyInput{EstimatedTokens: 100, HighMarkStrategyAvailable: true}, Noop},
		{"negative usage", withUsage(base, -1), Noop},
		{"cache ttl hot", withUsage(base, 500, func(in *CompactionPolicyInput) {
			in.LastTurnAt = time.Unix(100, 0)
			in.Now = time.Unix(101, 0)
			in.CacheTTL = 2 * time.Second
		}), Noop},
		{"cache ttl cold", withUsage(base, 500, func(in *CompactionPolicyInput) {
			in.LastTurnAt = time.Unix(100, 0)
			in.Now = time.Unix(103, 0)
			in.CacheTTL = 2 * time.Second
		}), SoftMaintenance},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DecideCompactionPolicy(tc.in); got != tc.want {
				t.Fatalf("decision = %s, want %s", got, tc.want)
			}
		})
	}
}

func withUsage(in CompactionPolicyInput, usage int, opts ...func(*CompactionPolicyInput)) CompactionPolicyInput {
	in.EstimatedTokens = usage
	for _, opt := range opts {
		opt(&in)
	}
	return in
}

func TestCompactionPolicyUsesNextRequestRisk(t *testing.T) {
	in := CompactionPolicyInput{
		EstimatedTokens: 500, NextRequestTokens: 790, ReserveTokens: 20,
		MaxTokens: 1000, HighMarkPercent: 75, HardPercent: 95,
		HighMarkStrategyAvailable: true,
	}
	if got := DecideCompactionPolicy(in); got != HighMarkCompaction {
		t.Fatalf("decision = %s, want high mark from next-request risk", got)
	}
}

func TestCompactionStrategiesOrdered(t *testing.T) {
	got := CompactionStrategiesOrdered()
	want := []CompressionStrategy{CompressionToolElision, CompressionMicro, CompressionSelective, CompressionSummarize}
	if len(got) != len(want) {
		t.Fatalf("strategy count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("strategy[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	got[0] = CompressionSummarize
	if CompactionStrategiesOrdered()[0] != CompressionToolElision {
		t.Fatal("strategy order must return an independent slice")
	}
}

func TestCompactionPolicyDecisionString(t *testing.T) {
	for decision, want := range map[CompactionPolicyDecision]string{
		Noop: "noop", SoftMaintenance: "soft_maintenance", HighMarkCompaction: "high_mark_compaction", EmergencyFallback: "emergency_fallback",
	} {
		if decision.String() != want {
			t.Errorf("%d.String() = %q, want %q", decision, decision.String(), want)
		}
	}
}
