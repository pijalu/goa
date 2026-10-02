// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"testing"
	"time"
)

// TestQuotaHarnessRenderSegmentWaitsForVMIdle reproduces the CI flake in
// TestQuota_ZaiEndpointWithPathStillHitsMonitorHost: the plugin primes its
// cache at load via goa.setTimeout(…,0), and that scheduler frame stays live
// across HTTP hops. buildSegmentRender deliberately skips ("", ok=false) while
// the runtime has a frame (only delayed, never lost — production re-renders on
// the goa.ui.refreshSegment signal), so a harness that renders exactly once can
// observe the transient skip and assert on "".
//
// The harness renderSegment must mirror the app render loop's FINAL state:
// when the runtime is busy, wait (bounded) for the frame to drain and take the
// render made with a quiescent runtime. Renders made while it is idle return
// as-is, so by-design empty segments stay immediate.
func TestQuotaHarnessRenderSegmentWaitsForVMIdle(t *testing.T) {
	// frames stands in for the plugin runtime's frame state (a bridge is not
	// loaded here — only the busy/skip contract is under test).
	var frames frameState
	e := newQuotaTestEnv(t)
	e.segments.AddSegment(UISegmentDef{
		ID: "quota",
		Render: func() (string, bool) {
			if frames.busy() {
				return "", false // mirrors buildSegmentRender's busy skip
			}
			return "[38%]", true
		},
	})

	// Hold a frame, like a scheduler timer parked on HTTP. Release it from
	// another goroutine so the (blocking) render below can observe the drain.
	// enter() marks it synchronously, so the first render attempt is
	// guaranteed to see a busy runtime.
	leave := frames.enter()
	go func() {
		time.Sleep(50 * time.Millisecond)
		leave()
	}()

	if got := e.renderSegment(); got != "[38%]" {
		t.Fatalf("segment should render after the runtime frame drains, got %q", got)
	}
}

// TestQuotaHarnessRenderSegmentIdleIsEmpty pins the other half of the
// contract: with no VM frame live, a by-design empty render (no_api_key and
// friends) is returned immediately without waiting out the retry budget.
func TestQuotaHarnessRenderSegmentIdleIsEmpty(t *testing.T) {
	e := newQuotaTestEnv(t)
	e.segments.AddSegment(UISegmentDef{
		ID:     "quota",
		Render: func() (string, bool) { return "", true }, // e.g. no_api_key with an idle VM
	})
	start := time.Now()
	if got := e.renderSegment(); got != "" {
		t.Fatalf("by-design empty segment should stay empty, got %q", got)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("idle render must not wait out the retry budget, took %v", d)
	}
}
