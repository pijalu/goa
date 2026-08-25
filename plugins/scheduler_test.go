// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestScheduler_StopCancelsDeferredOneShot is the regression test for the
// bugs.md CI flake "TestQuota_CarouselPrefersAPIProvidersOverLocal renders an
// empty segment": a setTimeout(0) one-shot that fires while a synchronous JS
// frame is active is deferred by invokeSafeWithReschedule and retried every
// 50ms by fireOnce. The old scheduler dropped the timer from its map BEFORE
// the first fire attempt, so a deferred retry loop was invisible to Stop() —
// the callback goroutine outlived Scheduler.Stop() (the plugin-unload /
// test-cleanup path) and later ran its stale callback in a gap where no other
// frame was live. A segment render hitting that exact window observes
// vmBusy()==true and buildSegmentRender returns "" (the CI failure).
//
// Contract under test: Scheduler.Stop() must cancel a one-shot even after its
// first fire attempt was deferred; the callback must never run after Stop
// returns.
func TestScheduler_StopCancelsDeferredOneShot(t *testing.T) {
	sch := NewScheduler()

	// Hold a logical frame so the one-shot's first fire attempt is deferred
	// (invokeSafeWithReschedule sees vmBusy()==true and reschedules).
	leave := enterVM()
	unlock := lockVM()

	var ran atomic.Bool
	sch.SetTimeout(func() { ran.Store(true) }, 0)

	// Give the timer goroutine time to reach its first (deferred) fire
	// attempt: it must now be parked in the 50ms back-off loop.
	time.Sleep(150 * time.Millisecond)

	// Release the frame briefly WITHOUT letting the callback win the race:
	// instead of unlocking, stop the scheduler while the frame is still held.
	// The deferral loop's next attempt must observe the closed stop channel,
	// not the free VM.
	sch.Stop()
	leave()
	unlock()

	// After Stop() returns, the callback must never run — not immediately,
	// and not on any later 50ms back-off tick.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if ran.Load() {
			t.Fatal("one-shot callback ran after Scheduler.Stop: deferred one-shot survived stop (zombie timer)")
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// TestScheduler_ClearCancelsPendingOneShot covers the user-facing clearTimeout
// path against a deferred one-shot: Clear must win over the pending retry loop
// exactly like it wins over a not-yet-fired timer.
func TestScheduler_ClearCancelsPendingOneShot(t *testing.T) {
	sch := NewScheduler()

	leave := enterVM()
	unlock := lockVM()

	var ran atomic.Bool
	id := sch.SetTimeout(func() { ran.Store(true) }, 0)
	time.Sleep(150 * time.Millisecond) // let the first fire attempt defer

	sch.Clear(id)
	leave()
	unlock()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if ran.Load() {
			t.Fatal("one-shot callback ran after Scheduler.Clear: deferred one-shot ignored clearTimeout")
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// TestScheduler_OneShotRemovedAfterRun guards the bounded-lifetime invariant
// the original drop-before-fire design protected: a one-shot that RAN must not
// accumulate in the timers map until Stop (unbounded growth for long-lived
// sessions whose plugins schedule zero-delay timers repeatedly).
func TestScheduler_OneShotRemovedAfterRun(t *testing.T) {
	sch := NewScheduler()

	done := make(chan struct{})
	var once sync.Once
	sch.SetTimeout(func() { once.Do(func() { close(done) }) }, 0)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("one-shot never ran")
	}

	// The entry must be gone once the run completed (allow the goroutine a
	// moment to perform its post-run deregistration).
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if sch.Count() == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("completed one-shot still registered: %d timers", sch.Count())
}
