// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"sync"
	"time"
)

// minInterval clamps plugin timers so a buggy plugin cannot busy-spin the
// runner with a zero-delay ticker.
const minInterval = 250 * time.Millisecond

// Scheduler owns JS timer callbacks (setInterval / setTimeout). Each timer
// fires on its own goroutine and invokes the callback directly; the callback
// acquires the global VM lock (lockVM) before touching the goja runtime, so
// goja's single-goroutine rule is preserved while a blocked bridge call
// (e.g. goa.http.fetch) can still be re-entered without deadlock.
type Scheduler struct {
	mu     sync.Mutex
	nextID int
	timers map[int]*pluginTimer
}

type pluginTimer struct {
	stop   chan struct{}
	period time.Duration // 0 = one-shot (setTimeout)
}

// NewScheduler creates a scheduler that dispatches callbacks via enqueue.
func NewScheduler() *Scheduler {
	return &Scheduler{
		timers: make(map[int]*pluginTimer),
	}
}

// SetInterval registers a repeating callback. Returns the timer id.
func (s *Scheduler) SetInterval(cb func(), interval time.Duration) int {
	if interval < minInterval {
		interval = minInterval
	}
	return s.start(cb, interval, false)
}

// SetTimeout registers a one-shot callback. Returns the timer id.
func (s *Scheduler) SetTimeout(cb func(), delay time.Duration) int {
	if delay < 0 {
		delay = 0
	}
	return s.start(cb, delay, true)
}

// Clear cancels a timer by id. Unknown ids are ignored.
func (s *Scheduler) Clear(id int) {
	s.mu.Lock()
	t, ok := s.timers[id]
	if ok {
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
		close(t.stop)
	}
}

// Count reports active timers (tests + diagnostics).
func (s *Scheduler) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.timers)
}

// start launches the timer goroutine.
func (s *Scheduler) start(cb func(), period time.Duration, oneshot bool) int {
	s.mu.Lock()
	s.nextID++
	id := s.nextID
	t := &pluginTimer{stop: make(chan struct{}), period: period}
	s.timers[id] = t
	s.mu.Unlock()

	go func() {
		if oneshot {
			s.fireOnce(t, cb)
			return
		}
		s.loop(t, cb)
	}()
	return id
}

// fireOnce waits for the period then invokes one callback, self-clearing.
func (s *Scheduler) fireOnce(t *pluginTimer, cb func()) {
	timer := time.NewTimer(t.period)
	defer timer.Stop()
	select {
	case <-t.stop:
		return
	case <-timer.C:
		invokeSafe(cb)
	}
}

// loop ticks until stopped, invoking the callback each period.
func (s *Scheduler) loop(t *pluginTimer, cb func()) {
	ticker := time.NewTicker(t.period)
	defer ticker.Stop()
	for {
		select {
		case <-t.stop:
			return
		case <-ticker.C:
			invokeSafe(cb)
		}
	}
}

// invokeSafe runs a timer callback under the global VM lock with panic
// containment so a misbehaving plugin cannot crash the timer goroutine.
func invokeSafe(cb func()) {
	if cb == nil {
		return
	}
	unlock := lockVM()
	defer unlock()
	defer func() { _ = recover() }()
	cb()
}
