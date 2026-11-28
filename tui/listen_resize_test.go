// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// resizeWatcherFunc adapts a function to the resizeWatcher interface.
type resizeWatcherFunc func(out chan<- struct{}, done <-chan struct{})

func (f resizeWatcherFunc) watch(out chan<- struct{}, done <-chan struct{}) { f(out, done) }

// TestListenResizeCreatesOneWatcherPerSession is the regression test for the
// watcher-respawn bug: listenResize used to evaluate resizeEvents(t.done)
// inside the select, so every resize event created a new watcher goroutine
// (on Windows: an extra concurrent reader of the console input queue, causing
// key reordering and missed resizes). Exactly one watcher must be created per
// session no matter how many events fire.
func TestListenResizeCreatesOneWatcherPerSession(t *testing.T) {
	var created atomic.Int32
	fire := make(chan struct{}, 16)

	saved := newResizeWatcher
	newResizeWatcher = func() resizeWatcher {
		created.Add(1)
		return resizeWatcherFunc(func(out chan<- struct{}, done <-chan struct{}) {
			for {
				select {
				case <-fire:
					sendResize(out)
				case <-done:
					return
				}
			}
		})
	}
	defer func() { newResizeWatcher = saved }()

	ui := &TUI{done: make(chan struct{})}
	go ui.listenResize()
	defer close(ui.done)

	const n = 5
	for i := 0; i < n; i++ {
		fire <- struct{}{}
		deadline := time.After(2 * time.Second)
		for !ui.renderDirty.Load() {
			select {
			case <-deadline:
				t.Fatalf("resize event %d did not trigger RequestRender", i)
			default:
				runtime.Gosched()
			}
		}
		ui.renderDirty.Store(false) // consume, ready for the next event
	}

	if got := created.Load(); got != 1 {
		t.Fatalf("listenResize created %d watchers across %d events, want 1", got, n)
	}
}
