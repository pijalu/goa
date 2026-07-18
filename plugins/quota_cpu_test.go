// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// These benchmarks quantify the CPU cost of the quota plugin's recurring
// operations, so we can confirm the timers + render path don't spin the CPU.
// Run with: go test ./plugins/ -bench Quota -benchmem

// BenchmarkSegmentRender measures one status-segment evaluation (the cache
// read the footer does every refresh). Must be sub-microsecond — it runs on
// the render path.
func BenchmarkSegmentRender(b *testing.B) {
	env := newQuotaTestEnv(&testing.T{})
	env.setProvider("anthropic", map[string]any{"provider": "anthropic", "apiKey": "sk"})
	env.respond("api.anthropic.com/v1/usage", 200, `{"usage":{"session":{"used":42,"limit":100},"weekly":{"used":30,"limit":100}}}`)
	bridge := env.load(&testing.T{})
	_ = bridge
	env.callCommand("quota", "refresh")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = env.renderSegment()
	}
}

// BenchmarkSchedulerTick measures the no-op cost of the 60s refresh scheduler
// when no provider is due (the common idle case). This is what runs every
// minute forever; it must be near-zero.
func BenchmarkSchedulerTick(b *testing.B) {
	env := newQuotaTestEnv(&testing.T{})
	env.setProvider("anthropic", map[string]any{"provider": "anthropic", "apiKey": "sk"})
	env.respond("api.anthropic.com/v1/usage", 200, `{"usage":{"session":{"used":42,"limit":100}}}`)
	env.load(&testing.T{})
	// Prime the cache so subsequent non-forced refreshes are interval-gated
	// no-ops (return stale cache).
	env.callCommand("quota", "refresh")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Non-forced refresh within the interval: should be a map read.
		env.callCommand("quota", "refresh")
	}
}

// BenchmarkRefreshDueCacheHit measures the interval-gate fast path directly
// (the JS-side guard that prevents refetching inside the declared interval).
func BenchmarkLocalFetcher(b *testing.B) {
	env := newQuotaTestEnv(&testing.T{})
	env.load(&testing.T{})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// The local fetcher runs every scheduler tick (refreshInterval 0) —
		// measure its per-tick cost.
		env.callCommand("quota", "local")
	}
}

// TestCPU_IdleSchedulerDoesNotSpin measures wall-clock firing rate with the
// timer at the minimum interval, proving the process isn't burning CPU when
// "idle". The counter is atomic (the callback runs on a timer goroutine).
func TestCPU_IdleSchedulerDoesNotSpin(t *testing.T) {
	s := NewScheduler()
	defer s.Stop()

	var ticks atomic.Int64
	s.SetInterval(func() { ticks.Add(1) }, minInterval)

	time.Sleep(1200 * time.Millisecond)

	got := ticks.Load()
	if got == 0 {
		t.Fatal("scheduler never fired")
	}
	// Upper bound: correct behavior is ~5; a busy-spin would be millions.
	if got > 20 {
		t.Fatalf("scheduler fired %d times in 1.2s — busy-spinning (want ~5)", got)
	}
	t.Logf("scheduler fired %d times in 1.2s (expected ~5)", got)
}

// TestCPU_TimerGoroutineSleeps verifies that between fires the timer goroutine
// is parked on the ticker (not runnable), by sampling runtime scheduler state
// is overkill; instead we assert elapsed wall time between fires ≥ interval.
func TestCPU_TimerGoroutineSleeps(t *testing.T) {
	s := NewScheduler()
	defer s.Stop()

	fires := make(chan time.Time, 8)
	s.SetInterval(func() { fires <- time.Now() }, minInterval)

	first := <-fires
	second := <-fires
	gap := second.Sub(first)
	// The gap must be at least the interval (goroutine slept on the ticker),
	// not a hot loop. Allow scheduler jitter below but not near-zero.
	if gap < minInterval/2 {
		t.Fatalf("timer gap %v < %v — goroutine not sleeping between fires", gap, minInterval/2)
	}
}

// BenchmarkStorageSet measures the OAuth-token write path (atomic file write).
// Called rarely (login/refresh), so cost is acceptable, but good to know.
func BenchmarkStorageSet(b *testing.B) {
	st, _ := NewStorageBridge(b.TempDir(), "q")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		st.Set("k", "v"+strconv.Itoa(i))
	}
}

// BenchmarkStorageGet measures the read path used by token lookups.
func BenchmarkStorageGet(b *testing.B) {
	st, _ := NewStorageBridge(b.TempDir(), "q")
	st.Set("k", "v")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = st.Get("k")
	}
}
