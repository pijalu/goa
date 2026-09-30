// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// TestStallWarnAfter_DefaultsToTwoThirdsOfWindow: with no explicit lead
// (execution.activity_warn_after unset) the stall warning must fire at two
// thirds of the EVENT stall — three quarters of the byte budget — so the user
// is told before the automatic retry, and before the byte-level guard, too.
func TestStallWarnAfter_DefaultsToTwoThirdsOfWindow(t *testing.T) {
	a := NewAgent(Config{})
	cases := []struct {
		name   string
		opts   provider.StreamOptions
		want   time.Duration
		reason string
	}{
		{
			name:   "45s byte budget derives 22.5s",
			opts:   provider.StreamOptions{IdleTimeout: 45 * time.Second},
			want:   22500 * time.Millisecond,
			reason: "two thirds of the 33.75s event stall",
		},
		{
			name:   "provider default budget stays proportional",
			opts:   provider.StreamOptions{},
			want:   provider.EventStallTimeout(provider.DefaultStreamIdleTimeout) * 2 / 3,
			reason: "no explicit window → provider default, still two thirds of its event stall",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := a.effectiveStallWarnAfter(tc.opts)
			if got != tc.want {
				t.Errorf("effectiveStallWarnAfter(%+v) = %s, want %s (%s)", tc.opts, got, tc.want, tc.reason)
			}
			if stall := a.effectiveEventStallTimeout(tc.opts); got >= stall {
				t.Errorf("derived lead %s must stay strictly inside the %s event stall", got, stall)
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
// pinned (e.g. 30s) by a home or project layer while activity_warn_after keeps
// the cascade default.
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
			stall := a.effectiveEventStallTimeout(tc.opts)
			want := stall * 2 / 3
			if got := a.effectiveStallWarnAfter(tc.opts); got != want {
				t.Errorf("effectiveStallWarnAfter(%+v) = %s, want the derived %s", tc.opts, got, want)
			}
			if got := a.effectiveStallWarnAfter(tc.opts); got >= stall {
				t.Errorf("derived lead %s must stay strictly inside the %s event stall", got, stall)
			}
		})
	}
}

// TestQuietWarningMessage_ByDefaultReportsShippedTiming pins the exact
// user-visible text for the shipped defaults: the lead comes from
// execution.activity_warn_after (3m20s) and the reported deadline is the event
// stall the watchdog actually retries on (3m45s = three quarters of the 5m
// byte budget):
//
//	provider quiet for 3m20s — still waiting; will auto-retry after 3m45s of silence
//
// It also asserts the message carries the values the agent actually uses (the
// lead from effectiveStallWarnAfter and the window from
// effectiveEventStallTimeout) rather than hard-coded numbers.
func TestQuietWarningMessage_ByDefaultReportsShippedTiming(t *testing.T) {
	shipped := provider.StreamOptions{IdleTimeout: 5 * time.Minute, ActivityWarnAfter: 200 * time.Second}

	agent, obs := quietTestAgent(t, provider.Api("test-stall-warn-message"), 5*time.Minute)
	warnAfter := agent.effectiveStallWarnAfter(shipped)
	stall := agent.effectiveEventStallTimeout(shipped)
	if warnAfter != 200*time.Second || stall != 225*time.Second {
		t.Fatalf("shipped timing = warn %s / retry %s, want 3m20s / 3m45s", warnAfter, stall)
	}

	agent.emitQuietWarning(warnAfter, stall)

	var msgs []string
	for _, e := range obs.Events() {
		if e.Type == EventProgress && len(e.Text) > 0 {
			msgs = append(msgs, e.Text)
		}
	}
	want := "provider quiet for 3m20s — still waiting; will auto-retry after 3m45s of silence"
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
	want := "provider quiet for 20s — still waiting; will auto-retry after 45s of silence"
	if got != want {
		t.Fatalf("quiet warning = %q, want %q", got, want)
	}
}
