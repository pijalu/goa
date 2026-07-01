// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/ansi"
)

// startWithLoops is a test helper that builds a minimal TUI over a fake
// terminal, starts it, and launches the Actor-model loops (commandLoop +
// renderLoop). It returns the engine and a stop fn. Tests that exercise the
// running loops use this; the single-goroutine tests call only Start().
func startWithLoops(t *testing.T, w, h int) (*TUI, func()) {
	t.Helper()
	term := &fakeTerminal{w: w, h: h}
	engine := NewTUI(term)
	engine.AddChild(NewChatViewport())
	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	engine.RunLoops()
	stop := func() {
		engine.Stop()
		select {
		case <-engine.Stopped():
		case <-time.After(time.Second):
			t.Fatalf("loops did not stop within 1s")
		}
	}
	return engine, stop
}

// TestApplySync_RunsOnCommandLoop proves that an ApplySync Command submitted
// from a non-loop goroutine executes on the commandLoop goroutine (the sole
// state owner). This is the cornerstone of the Actor model: callers do not
// touch state directly.
func TestApplySync_RunsOnCommandLoop(t *testing.T) {
	engine, stop := startWithLoops(t, 80, 24)
	defer stop()

	var ranOn uint64
	engine.ApplySync(func() { ranOn = goroutineID() })
	if ranOn == 0 {
		t.Fatal("ApplySync command did not capture a goroutine ID")
	}
	if got := engine.loopGoroutine.Load(); got != ranOn {
		t.Fatalf("ApplySync ran on goroutine %d, want commandLoop %d", ranOn, got)
	}
}

// TestApply_SerializesConcurrentCommands proves the commandLoop is the single
// serialization point: many goroutines submitting Commands via Apply see them
// executed one at a time. A plain int is mutated only by Commands; if two ran
// concurrently the final count would be wrong and/or the race detector fires.
func TestApply_SerializesConcurrentCommands(t *testing.T) {
	engine, stop := startWithLoops(t, 80, 24)
	defer stop()

	const n = 200
	var counter int
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			engine.Apply(func() { counter++ })
		}()
	}
	wg.Wait()
	// ApplySync drains: it blocks until the loop has run everything ahead of it.
	engine.ApplySync(func() {})
	if counter != n {
		t.Fatalf("counter = %d, want %d (commands were not all serialized)", counter, n)
	}
}

// TestApplySync_ReentrantRunsInline proves the self-deadlock guard: when a
// Command running on the commandLoop calls ApplySync (e.g. a shortcut callback
// that triggers ShowSelector), the call runs inline on the loop instead of
// blocking on the cmds channel. Without the guard this test would hang.
func TestApplySync_ReentrantRunsInline(t *testing.T) {
	engine, stop := startWithLoops(t, 80, 24)
	defer stop()

	var innerRanOn, outerRanOn uint64
	done := make(chan struct{})
	engine.Apply(func() {
		outerRanOn = goroutineID()
		// Re-entrant synchronous call from inside a loop command.
		engine.ApplySync(func() { innerRanOn = goroutineID() })
		// Signal from the loop after both IDs are captured, so the test
		// goroutine reads them under a happens-before relationship.
		close(done)
	})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("re-entrant ApplySync deadlocked")
	}
	loop := engine.loopGoroutine.Load()
	if outerRanOn != loop {
		t.Fatalf("outer command ran on goroutine %d, want loop %d", outerRanOn, loop)
	}
	if innerRanOn != loop {
		t.Fatalf("inner ApplySync ran on goroutine %d, want loop %d", innerRanOn, loop)
	}
}

// TestShowOverlay_OffLoopRegistration proves ShowOverlay (called from a non-loop
// goroutine, as the tool-confirmation path does) registers the overlay on the
// commandLoop and the handle's Hide closure routes back through Apply.
func TestShowOverlay_OffLoopRegistration(t *testing.T) {
	engine, stop := startWithLoops(t, 80, 24)
	defer stop()

	var handle *OverlayHandle
	var observed int
	done := make(chan struct{})
	go func() {
		defer close(done)
		handle = engine.ShowOverlay(NewInput(), OverlayOptions{CaptureInput: true, Height: 3})
		// Observe state on the loop once the overlay is registered.
		engine.ApplySync(func() { observed = len(engine.overlayStack) })
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ShowOverlay from off-loop goroutine did not complete")
	}
	if handle == nil || !handle.IsVisible() {
		t.Fatal("overlay handle not returned/visible after ShowOverlay")
	}
	if observed != 1 {
		t.Fatalf("overlayStack len = %d after ShowOverlay, want 1", observed)
	}

	// Hide from an off-loop goroutine must remove the overlay on the loop.
	hideDone := make(chan struct{})
	go func() {
		defer close(hideDone)
		handle.Hide()
		engine.ApplySync(func() { observed = len(engine.overlayStack) })
	}()
	select {
	case <-hideDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Hide from off-loop goroutine did not complete")
	}
	if observed != 0 {
		t.Fatalf("overlayStack len = %d after Hide, want 0", observed)
	}
	if handle.IsVisible() {
		t.Error("handle reports visible after Hide")
	}
}

// TestRequestRender_DirtyFlag proves the renderLoop picks up mutations flagged
// via Apply and publishes a frame. This closes the loop: mutation → dirty →
// snapshot → compositor output.
func TestRequestRender_DirtyFlag(t *testing.T) {
	engine, stop := startWithLoops(t, 80, 24)
	defer stop()

	engine.Apply(func() {
		engine.findChatViewport().AddSystemMessage("actor-model-frame")
	})

	deadline := time.Now().Add(500 * time.Millisecond)
	var saw bool
	for !saw && time.Now().Before(deadline) {
		for _, w := range termWrites(engine) {
			if strings.Contains(ansi.Strip(w), "actor-model-frame") {
				saw = true
				break
			}
		}
		if !saw {
			time.Sleep(5 * time.Millisecond)
		}
	}
	if !saw {
		t.Fatal("renderLoop did not emit a frame with the mutation within 500ms")
	}
}

// termWrites returns a snapshot of the fake terminal's written chunks.
func termWrites(engine *TUI) []string {
	ft, ok := engine.terminal.(*fakeTerminal)
	if !ok {
		return nil
	}
	return ft.Writes()
}
