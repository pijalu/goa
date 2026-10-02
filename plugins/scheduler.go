// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"sync"
	"sync/atomic"
	"time"
)

// minInterval clamps plugin timers so a buggy plugin cannot busy-spin the
// runner with a zero-delay ticker.
const minInterval = 250 * time.Millisecond

// Scheduler owns JS timer callbacks (setInterval / setTimeout). Each timer
// fires on its own goroutine and invokes the callback inside the registering
// runtime's frame (FrameGate — *JSBridge), so goja's single-goroutine rule
// holds: a tick that finds a frame live is deferred and retried, never run
// alongside live JavaScript.
//
// The gate is per timer, not per scheduler: one Scheduler is shared by every
// plugin (internal/app), and each plugin owns a distinct runtime.
type Scheduler struct {
	mu     sync.Mutex
	nextID int
	timers map[int]*pluginTimer
}

type pluginTimer struct {
	stop   chan struct{}
	period time.Duration // 0 = one-shot (setTimeout)
	cancel atomic.Bool
}

// NewScheduler creates a scheduler that dispatches callbacks via enqueue.
func NewScheduler() *Scheduler {
	return &Scheduler{
		timers: make(map[int]*pluginTimer),
	}
}

// SetInterval registers a repeating callback that runs directly (callers
// outside a JS runtime). Returns the timer id.
func (s *Scheduler) SetInterval(cb func(), interval time.Duration) int {
	return s.SetIntervalGated(nil, cb, interval)
}

// SetIntervalGated registers a repeating callback that runs inside gate's
// frame. A tick deferred by a live frame is simply skipped — the next tick
// retries — so intervals need no back-off.
func (s *Scheduler) SetIntervalGated(gate FrameGate, cb func(), interval time.Duration) int {
	if interval < minInterval {
		interval = minInterval
	}
	return s.start(func() bool { return runGated(gate, cb) }, interval, false)
}

// SetTimeout registers a one-shot callback that runs directly (callers outside
// a JS runtime). Returns the timer id.
func (s *Scheduler) SetTimeout(cb func(), delay time.Duration) int {
	return s.SetTimeoutGated(nil, cb, delay)
}

// SetTimeoutGated registers a one-shot callback inside gate's frame. A
// deferred one-shot (live frame at fire time) is retried after a short
// back-off — delayed, never dropped, since a one-shot carries the cache prime.
func (s *Scheduler) SetTimeoutGated(gate FrameGate, cb func(), delay time.Duration) int {
	if delay < 0 {
		delay = 0
	}
	return s.start(func() bool { return runGated(gate, cb) }, delay, true)
}

// Clear cancels a timer by id. Unknown ids are ignored.
func (s *Scheduler) Clear(id int) {
	s.mu.Lock()
	t, ok := s.timers[id]
	if ok {
		t.cancel.Store(true)
		delete(s.timers, id)
	}
	s.mu.Unlock()
	if ok {
		close(t.stop)
	}
}

// Stop cancels all timers (plugin unload / app shutdown).
func (s *Scheduler) Stop() {
	s.mu.Lock()
	timers := s.timers
	s.timers = make(map[int]*pluginTimer)
	s.mu.Unlock()
	for _, t := range timers {
		t.cancel.Store(true)
		close(t.stop)
	}
}

// Count reports active timers (tests + diagnostics).
func (s *Scheduler) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.timers)
}

// start launches the timer goroutine. tick reports whether the callback
// actually ran (false = deferred by the frame gate).
func (s *Scheduler) start(tick func() bool, period time.Duration, oneshot bool) int {
	s.mu.Lock()
	s.nextID++
	id := s.nextID
	t := &pluginTimer{stop: make(chan struct{}), period: period}
	s.timers[id] = t
	s.mu.Unlock()

	go func() {
		if oneshot {
			// One-shots stay REGISTERED until they reach a terminal state
			// (callback ran, or stop closed): a fire attempt deferred by a
			// live JS frame parks fireOnce in its back-off loop, and that
			// pending retry must remain cancellable by Clear/Stop — the
			// plugin-unload and test-cleanup path (bugs.md: a dropped
			// before-fire one-shot outlived Scheduler.Stop as a zombie,
			// later entering a VM frame under a live segment render).
			// Deregister only AFTER fireOnce returns.
			s.fireOnce(t, tick)
			s.drop(id)
			return
		}
		s.loop(t, tick)
	}()
	return id
}

// drop unregisters a timer id without signaling cancellation. Clear is the
// cancel path (delete + close stop); drop only forgets the entry.
func (s *Scheduler) drop(id int) {
	s.mu.Lock()
	delete(s.timers, id)
	s.mu.Unlock()
}

// fireOnce waits for the period then runs one tick. A tick deferred by the
// frame gate re-fires after a short back-off so a one-shot prime (e.g. the
// quota cache warm) is delayed, never dropped. The timer stays registered for
// the whole wait so Clear/Stop can cancel a pending retry; the caller (start)
// deregisters once this returns.
func (s *Scheduler) fireOnce(t *pluginTimer, tick func() bool) {
	delay := t.period
	for {
		timer := time.NewTimer(delay)
		select {
		case <-t.stop:
			timer.Stop()
			return
		case <-timer.C:
			// Cancellation can race with the timer becoming ready. Check both
			// after waking and immediately before dispatch so Clear/Stop wins
			// over a pending one-shot rather than merely removing its registry
			// entry.
			if t.cancel.Load() {
				return
			}
			if tick() {
				return
			}
			delay = 50 * time.Millisecond
		}
	}
}

// loop ticks until stopped, running the callback each period. A deferred tick
// is simply skipped — the next tick retries — so intervals need no back-off.
func (s *Scheduler) loop(t *pluginTimer, tick func() bool) {
	ticker := time.NewTicker(t.period)
	defer ticker.Stop()
	for {
		select {
		case <-t.stop:
			return
		case <-ticker.C:
			tick()
		}
	}
}

// runGated runs cb inside gate's frame and reports whether it ran.
//
// false means a frame was live on that runtime, i.e. DEFERRED: the caller
// re-arms (one-shot back-off) or skips the tick (interval). Two goja frames
// must never overlap — that is the corruption behind the flaky
// TestPluginCommandExecutesThroughRouter and, on the quota plugin, the
// z.ai coding-plan credential read that saw `tok` truthy yet undefined.
// A nil gate means the caller owns no runtime (host-side timers, tests), so
// the callback runs directly.
//
// Panics are contained: a misbehaving plugin must not crash a timer goroutine.
func runGated(gate FrameGate, cb func()) (ran bool) {
	if cb == nil {
		return true
	}
	if gate != nil {
		leave, ok := gate.TryEnter()
		if !ok {
			return false
		}
		defer leave()
	}
	defer func() {
		_ = recover()
		ran = true
	}()
	cb()
	return true
}

// invokeSafe runs one timer tick under gate's frame discipline (see
// runGated). Exported within the package for tests that drive the tick path
// directly.
func invokeSafe(gate FrameGate, cb func()) { runGated(gate, cb) }
