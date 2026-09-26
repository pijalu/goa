// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// TestStallWarnAfter_DefaultsToTwoThirdsOfWindow: with the shipped defaults
// (execution.activity_timeout 45s, execution.activity_warn_after unset) the
// stall warning must fire at 30s — two thirds of the window — so the user is
// told before the automatic retry at 45s.
func TestStallWarnAfter_DefaultsToTwoThirdsOfWindow(t *testing.T) {
	a := NewAgent(Config{})
	cases := []struct {
		name   string
		opts   provider.StreamOptions
		want   time.Duration
		reason string
	}{
		{
			name:   "shipped 45s window derives 30s",
			opts:   provider.StreamOptions{IdleTimeout: 45 * time.Second},
			want:   30 * time.Second,
			reason: "two thirds of the configured window",
		},
		{
			name:   "provider default 2m window stays proportional",
			opts:   provider.StreamOptions{},
			want:   provider.DefaultStreamIdleTimeout * 2 / 3,
			reason: "no explicit window → provider default, still two thirds",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := a.effectiveStallWarnAfter(tc.opts); got != tc.want {
				t.Errorf("effectiveStallWarnAfter(%+v) = %s, want %s (%s)", tc.opts, got, tc.want, tc.reason)
			}
		})
	}
}

// TestStallWarnAfter_ExplicitOverride: a configured lead inside the window wins
// over the derived default.
func TestStallWarnAfter_ExplicitOverride(t *testing.T) {
	a := NewAgent(Config{})
	opts := provider.StreamOptions{IdleTimeout: 45 * time.Second, ActivityWarnAfter: 10 * time.Second}
	if got := a.effectiveStallWarnAfter(opts); got != 10*time.Second {
		t.Errorf("effectiveStallWarnAfter = %s, want the configured 10s", got)
	}
}

// TestStallWarnAfter_IgnoredAtOrBeyondStallWindow: a lead at or beyond the
// window would never precede the retry it announces (and could never fire at
// all), so the agent derives two thirds instead. This is the case a
// configuration-layer cross-check must NOT reject: activity_timeout is commonly
// pinned (e.g. 30s) while activity_warn_after keeps the shipped 30s default.
func TestStallWarnAfter_IgnoredAtOrBeyondStallWindow(t *testing.T) {
	a := NewAgent(Config{})
	cases := []struct {
		name string
		opts provider.StreamOptions
	}{
		{
			name: "lead equal to the window (pinned timeout + shipped default)",
			opts: provider.StreamOptions{IdleTimeout: 30 * time.Second, ActivityWarnAfter: 30 * time.Second},
		},
		{
			name: "lead beyond the window",
			opts: provider.StreamOptions{IdleTimeout: 30 * time.Second, ActivityWarnAfter: 90 * time.Second},
		},
		{
			name: "negative lead",
			opts: provider.StreamOptions{IdleTimeout: 30 * time.Second, ActivityWarnAfter: -time.Second},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := tc.opts.IdleTimeout * 2 / 3
			if got := a.effectiveStallWarnAfter(tc.opts); got != want {
				t.Errorf("effectiveStallWarnAfter(%+v) = %s, want the derived %s", tc.opts, got, want)
			}
			if got := a.effectiveStallWarnAfter(tc.opts); got >= tc.opts.IdleTimeout {
				t.Errorf("derived lead %s must stay strictly inside the %s window", got, tc.opts.IdleTimeout)
			}
		})
	}
}

// TestQuietWarningMessage_ByDefaultReports30sAnd45s pins the exact
// user-visible text for the shipped defaults:
//
//	provider quiet for 30s — still waiting; will auto-retry after 45s of silence
//
// It also asserts the message carries the values the agent actually uses (the
// lead from effectiveStallWarnAfter and the window from
// effectiveEventStallTimeout) rather than hard-coded numbers.
func TestQuietWarningMessage_ByDefaultReports30sAnd45s(t *testing.T) {
	shipped := provider.StreamOptions{IdleTimeout: 45 * time.Second}

	agent, obs := quietTestAgent(t, provider.Api("test-stall-warn-message"), 45*time.Second)
	warnAfter := agent.effectiveStallWarnAfter(shipped)
	stall := agent.effectiveEventStallTimeout(shipped)
	if warnAfter != 30*time.Second || stall != 45*time.Second {
		t.Fatalf("shipped timing = warn %s / retry %s, want 30s / 45s", warnAfter, stall)
	}

	agent.emitQuietWarning(warnAfter, stall)

	var msgs []string
	for _, e := range obs.Events() {
		if e.Type == EventProgress && len(e.Text) > 0 {
			msgs = append(msgs, e.Text)
		}
	}
	want := "provider quiet for 30s — still waiting; will auto-retry after 45s of silence"
	if len(msgs) != 1 || msgs[0] != want {
		t.Fatalf("quiet warning = %q, want exactly %q", msgs, want)
	}
}

// TestQuietWarningMessage_FollowsConfiguredTiming guards against the message
// drifting from the configured values: a custom pair must be reported verbatim.
func TestQuietWarningMessage_FollowsConfiguredTiming(t *testing.T) {
	agent, obs := quietTestAgent(t, provider.Api("test-stall-warn-custom"), 60*time.Second)
	custom := provider.StreamOptions{IdleTimeout: 60 * time.Second, ActivityWarnAfter: 20 * time.Second}

	agent.emitQuietWarning(agent.effectiveStallWarnAfter(custom), agent.effectiveEventStallTimeout(custom))

	var got string
	for _, e := range obs.Events() {
		if e.Type == EventProgress {
			got = e.Text
		}
	}
	want := "provider quiet for 20s — still waiting; will auto-retry after 1m0s of silence"
	if got != want {
		t.Fatalf("quiet warning = %q, want %q", got, want)
	}
}
