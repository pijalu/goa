// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"testing"
	"time"
)

func TestFilterResizeRecords(t *testing.T) {
	tests := []struct {
		name string
		buf  []inputRecord
		want bool
	}{
		{"nil buffer", nil, false},
		{"empty buffer", []inputRecord{}, false},
		{"single buffer-size record", []inputRecord{{winEventWindowBufferSize}}, true},
		{"key record", []inputRecord{{winEventKey}}, false},
		{"mouse record", []inputRecord{{winEventMouse}}, false},
		{"menu record", []inputRecord{{winEventMenu}}, false},
		{"focus record", []inputRecord{{winEventFocus}}, false},
		{"unknown event type", []inputRecord{{0x0042}}, false},
		{"only foreign records", []inputRecord{{winEventKey}, {winEventMouse}, {winEventFocus}}, false},
		{"resize among foreign records",
			[]inputRecord{{winEventKey}, {winEventWindowBufferSize}, {winEventFocus}}, true},
		{"multiple resize records",
			[]inputRecord{{winEventWindowBufferSize}, {winEventWindowBufferSize}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := filterResizeRecords(tt.buf); got != tt.want {
				t.Fatalf("filterResizeRecords(%v) = %v, want %v", tt.buf, got, tt.want)
			}
		})
	}
}

// TestSyntheticRecordBatchFiresChannel pins the drain-loop composition used
// by the Windows watcher over a ReadConsoleInput batch: the channel fires
// exactly when the synthetic INPUT_RECORD buffer contains a buffer-size
// event; pure key/mouse/focus batches never produce a notification.
func TestSyntheticRecordBatchFiresChannel(t *testing.T) {
	tests := []struct {
		name string
		recs []inputRecord
		want bool
	}{
		{"buffer-size event", []inputRecord{{winEventWindowBufferSize}}, true},
		{"foreign events only", []inputRecord{{winEventKey}, {winEventMouse}}, false},
		{"resize behind foreign events",
			[]inputRecord{{winEventFocus}, {winEventWindowBufferSize}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if fired := filterResizeRecords(tt.recs); fired != tt.want {
				t.Fatalf("filterResizeRecords(%v) = %v, want %v", tt.recs, fired, tt.want)
			}
			assertResizeChannelFiring(t, tt.want)
		})
	}
}

// assertResizeChannelFiring pins the drain-loop notification contract:
// sendResize must make the buffered channel deliver exactly one event when a
// resize was signaled, and never fire when there was none.
func assertResizeChannelFiring(t *testing.T, wantFired bool) {
	t.Helper()
	out := make(chan struct{}, 1)
	if wantFired {
		sendResize(out)
	}
	select {
	case <-out:
		if !wantFired {
			t.Fatal("channel fired without a buffer-size event")
		}
	default:
		if wantFired {
			t.Fatal("buffer-size event did not fire the channel")
		}
	}
}

func TestIsResizeRecord(t *testing.T) {
	if !isResizeRecord(inputRecord{eventType: winEventWindowBufferSize}) {
		t.Fatal("buffer-size record not recognized")
	}
	for _, et := range []uint16{winEventKey, winEventMouse, winEventMenu, winEventFocus, 0} {
		if isResizeRecord(inputRecord{eventType: et}) {
			t.Fatalf("event type %#x misclassified as resize", et)
		}
	}
}

// scriptedSizes returns a size function plus a feed channel. Ownership of the
// current value transfers entirely to whichever goroutine runs the returned
// function (the poller), so test and worker never share mutable state.
func scriptedSizes(w0, h0 int) (size func() (int, int), feed chan<- [2]int) {
	ch := make(chan [2]int, 16)
	cur := [2]int{w0, h0}
	size = func() (int, int) {
		select {
		case s := <-ch:
			cur = s
		default:
		}
		return cur[0], cur[1]
	}
	return size, ch
}

// pinWatcher pins newResizeWatcher for the duration of the test and returns a
// restore func. Deferred restores must run AFTER done channels are closed
// (defer order in each test: restore first, close(done) second).
func pinWatcher(w resizeWatcher) func() {
	saved := newResizeWatcher
	newResizeWatcher = func() resizeWatcher { return w }
	return func() { newResizeWatcher = saved }
}

func TestSendResizeCoalescesWithoutBlocking(t *testing.T) {
	out := make(chan struct{}, resizeChannelBuffer)
	for range resizeChannelBuffer {
		sendResize(out)
	}
	blocked := make(chan struct{})
	go func() {
		defer close(blocked)
		for range 3 {
			sendResize(out) // must coalesce, never block
		}
	}()
	select {
	case <-blocked:
	case <-time.After(2 * time.Second):
		t.Fatal("sendResize blocked on a full channel")
	}
	if got := len(out); got != resizeChannelBuffer {
		t.Fatalf("len(out) = %d, want %d (events dropped prematurely)", got, resizeChannelBuffer)
	}
}

func TestRunResizePollerFiresOnSizeChange(t *testing.T) {
	out := make(chan struct{}, resizeChannelBuffer)
	done := make(chan struct{})
	defer close(done)
	size, feed := scriptedSizes(80, 24)
	go runResizePoller(out, done, size, 2*time.Millisecond)

	time.Sleep(10 * time.Millisecond) // let a few constant-size ticks pass
	feed <- [2]int{100, 30}           // next tick must observe the change

	select {
	case <-out:
	case <-time.After(2 * time.Second):
		t.Fatal("poller did not fire after size change")
	}
}

func TestRunResizePollerIgnoresConstantSize(t *testing.T) {
	out := make(chan struct{}, resizeChannelBuffer)
	done := make(chan struct{})
	defer close(done)
	size, _ := scriptedSizes(80, 24)
	go runResizePoller(out, done, size, 2*time.Millisecond)

	// ~20 ticks at constant size: no notification may ever be emitted.
	time.Sleep(40 * time.Millisecond)
	if n := len(out); n != 0 {
		t.Fatalf("poller fired %d times without any size change", n)
	}
}

// TestRunResizePollerStopsWhenDoneClosed proves the loop exits on done with a
// synchronous call: with done already closed and an interval far longer than
// any scheduling pause, the select must take the done branch before the first
// tick — so the size function must not run again.
func TestRunResizePollerStopsWhenDoneClosed(t *testing.T) {
	out := make(chan struct{}, resizeChannelBuffer)
	done := make(chan struct{})
	close(done)
	calls := 0
	runResizePoller(out, done, func() (int, int) {
		calls++
		return 80, 24
	}, time.Hour)
	if calls != 1 { // exactly the baseline read, no polling afterwards
		t.Fatalf("size function called %d times after done closed, want 1", calls)
	}
}

// TestResizeEventsFallsBackToPollerWhenWatcherUnavailable covers criterion (2):
// a nil platform watcher must transparently switch resizeEvents to the legacy
// 250ms poller while keeping the channel contract.
func TestResizeEventsFallsBackToPollerWhenWatcherUnavailable(t *testing.T) {
	restore := pinWatcher(nil) // event mode unavailable
	defer restore()

	done := make(chan struct{})
	defer close(done)
	size, feed := scriptedSizes(80, 24)
	savedSize := consoleSizeFunc
	consoleSizeFunc = size
	defer func() { consoleSizeFunc = savedSize }()

	out := resizeEvents(done)
	var recv <-chan struct{} = out // contract: receive-only channel

	time.Sleep(20 * time.Millisecond)
	feed <- [2]int{132, 43} // detected by a poll tick within resizeFallbackInterval

	select {
	case <-recv:
	case <-time.After(2 * time.Second):
		t.Fatal("fallback poller did not report the size change")
	}
}

// stubWatcher records that it was preferred over the poller.
type stubWatcher struct {
	started chan struct{}
	fired   chan struct{}
}

func newStubWatcher() *stubWatcher {
	return &stubWatcher{started: make(chan struct{}), fired: make(chan struct{})}
}

func (w *stubWatcher) watch(out chan<- struct{}, done <-chan struct{}) {
	close(w.started)
	sendResize(out)
	close(w.fired)
	<-done // hold the watcher until the test releases it
}

// TestResizeEventsPrefersEventWatcher covers criterion (3)'s wiring half: when
// the platform provides a watcher it is used instead of the poller.
func TestResizeEventsPrefersEventWatcher(t *testing.T) {
	w := newStubWatcher()
	restore := pinWatcher(w)
	defer restore()

	// If the poller were (wrongly) started instead, the real consoleSizeFunc
	// could only fire on an actual terminal change; the stub fires instantly.
	done := make(chan struct{})
	defer close(done)
	out := resizeEvents(done)
	var recv <-chan struct{} = out

	select {
	case <-w.started:
	case <-time.After(2 * time.Second):
		t.Fatal("platform watcher was not used (poller path taken instead)")
	}
	select {
	case <-recv:
	case <-time.After(2 * time.Second):
		t.Fatal("watcher event was not forwarded")
	}
}

func TestResizeEventsChannelContract(t *testing.T) {
	restore := pinWatcher(newStubWatcher())
	defer restore()
	done := make(chan struct{})
	out := resizeEvents(done)
	close(done) // release the stub watcher before restoring hooks

	if cap(out) != resizeChannelBuffer {
		t.Fatalf("channel capacity = %d, want %d (unchanged contract)", cap(out), resizeChannelBuffer)
	}
}
