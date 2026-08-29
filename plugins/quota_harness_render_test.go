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
// cache at load via goa.setTimeout(…,0), and that scheduler frame's enterVM
// window stays active across HTTP hops. buildSegmentRender deliberately
// returns "" while the VM is busy (only delayed, never lost — production
// re-renders on the goa.ui.refreshSegment signal), so a harness that renders
// exactly once can observe the transient skip and assert on "".
//
// The harness renderSegment must mirror the app render loop's FINAL state:
// when the VM is busy, wait (bounded) for the frame to drain and take the
// render made with a quiescent VM. Renders made while the VM is idle return
// as-is, so by-design empty segments stay immediate.
func TestQuotaHarnessRenderSegmentWaitsForVMIdle(t *testing.T) {
	e := newQuotaTestEnv(t)
	e.segments.AddSegment(UISegmentDef{
		ID: "quota",
		Render: func() string {
			if vmBusy() {
				return "" // mirrors buildSegmentRender's busy skip
			}
			return "[38%]"
		},
	})

	// Hold a logical VM frame, like a scheduler timer parked on HTTP inside
	// enterVM. Release it from another goroutine so the (blocking) render
	// below can observe the drain. enterVM increments synchronously, so the
	// first render attempt is guaranteed to see a busy VM.
	leave := enterVM()
	go func() {
		time.Sleep(50 * time.Millisecond)
		leave()
	}()

	if got := e.renderSegment(); got != "[38%]" {
		t.Fatalf("segment should render after the VM frame drains, got %q", got)
	}
}

// TestQuotaHarnessRenderSegmentIdleIsEmpty pins the other half of the
// contract: with no VM frame live, a by-design empty render (no_api_key and
// friends) is returned immediately without waiting out the retry budget.
func TestQuotaHarnessRenderSegmentIdleIsEmpty(t *testing.T) {
	e := newQuotaTestEnv(t)
	e.segments.AddSegment(UISegmentDef{
		ID:     "quota",
		Render: func() string { return "" }, // e.g. no_api_key with an idle VM
	})
	start := time.Now()
	if got := e.renderSegment(); got != "" {
		t.Fatalf("by-design empty segment should stay empty, got %q", got)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("idle render must not wait out the retry budget, took %v", d)
	}
}
