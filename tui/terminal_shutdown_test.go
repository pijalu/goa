// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// newTestPipeTerminal builds a ProcessTerminal whose stdin reader is an
// os.Pipe, so input can be driven deterministically without a real console.
func newTestPipeTerminal(t *testing.T) (*ProcessTerminal, *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { r.Close(); w.Close() })

	pt := NewProcessTerminal()
	pt.reader = r
	pt.fd = int(r.Fd())
	pt.stdinBuffer = NewStdinBuffer()
	pt.done = make(chan struct{})
	pt.readLoopDone = make(chan struct{})
	return pt, w
}

// collectInput returns a thread-safe recorder for onInput events.
func collectInput(pt *ProcessTerminal) (*[]string, *sync.Mutex) {
	var mu sync.Mutex
	var got []string
	pt.onInput = func(s string) {
		mu.Lock()
		got = append(got, s)
		mu.Unlock()
	}
	return &got, &mu
}

// TestReadLoopExitsOnStop is the regression test for the /setup wizard
// freeze: when the main TUI engine stops (Stop → terminal.Stop), its stdin
// readLoop MUST terminate before the wizard engine starts its own reader.
// Before the fix, the readLoop was left blocked in os.Stdin.Read: the wizard
// then had TWO readers racing for every keystroke, input consumed by the dead
// engine was silently lost, and the wizard GUI appeared unresponsive.
func TestReadLoopExitsOnStop(t *testing.T) {
	pt, w := newTestPipeTerminal(t)
	got, mu := collectInput(pt)

	go pt.readLoop()

	// Normal input is dispatched while the terminal is live.
	_, err := w.Write([]byte("hi"))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(*got) == 2 && (*got)[0] == "h" && (*got)[1] == "i"
	}, time.Second, 5*time.Millisecond)

	// Stop the terminal while the readLoop is blocked waiting for input.
	close(pt.done)
	pt.shutdownReadLoop()

	// The readLoop must exit promptly (the interrupt wakes the pipe read).
	select {
	case <-pt.readLoopDone:
	case <-time.After(2 * time.Second):
		t.Fatal("readLoop did not exit after Stop — a stale stdin reader would steal the wizard's keystrokes")
	}

	// Input that arrives after Stop must never be dispatched to the dead
	// engine: it belongs to the successor engine's reader.
	_, err = w.Write([]byte("zz"))
	require.NoError(t, err)
	time.Sleep(150 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, *got, 2, "post-stop input was dispatched to the stopped terminal")
}

// TestTUIStopTerminatesStdinReader exercises the same guarantee through the
// TUI engine, mirroring the App flow: engine.Start + RunLoops + Stop. The
// wizard (config.RunSetupWizard) and the relaunched app both create a new
// engine right after the previous one stops, so the previous reader must be
// gone by then.
func TestTUIStopTerminatesStdinReader(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { r.Close(); w.Close() })

	pt := NewProcessTerminal()
	pt.reader = r
	pt.fd = int(r.Fd())

	engine := NewTUI(pt)
	require.NoError(t, engine.Start())
	engine.RunLoops()

	// Give the readLoop a moment to block on the pipe read (it is already
	// blocked: nothing has been written and Start launched it synchronously).
	engine.Stop()

	select {
	case <-pt.readLoopDone:
	case <-time.After(2 * time.Second):
		t.Fatal("TUI.Stop left the stdin readLoop running — the /setup wizard would race it for input")
	}
	_ = w
}

// blockingReader simulates a Windows console: it blocks until data is pushed
// and ignores read deadlines (ReadConsole has no deadline support).
type blockingReader struct {
	mu    sync.Mutex
	data  []byte
	ready chan struct{}
}

func (r *blockingReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	for len(r.data) == 0 {
		ch := r.ready
		r.mu.Unlock()
		<-ch
		r.mu.Lock()
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

func (r *blockingReader) push(s string) {
	r.mu.Lock()
	r.data = append(r.data, s...)
	ch := r.ready
	r.ready = make(chan struct{})
	r.mu.Unlock()
	close(ch)
}

func (r *blockingReader) SetReadDeadline(time.Time) error {
	return os.ErrNoDeadline // Windows console: deadlines unsupported
}

// TestEscapeDebounceDoesNotEatNextKey is the regression test for the wizard's
// back/cancel navigation eating the keystroke after every Escape on Windows.
// A bare ESC blocks the debounce read (no deadline); the fallback timer emits
// the ESC on its own; the NEXT key then arrives and must be forwarded alone,
// never re-prefixed with the stale ESC (which decodes as Alt+<key> and is
// silently dropped).
func TestEscapeDebounceDoesNotEatNextKey(t *testing.T) {
	br := &blockingReader{ready: make(chan struct{})}
	pt := NewProcessTerminal()
	pt.reader = br
	pt.stdinBuffer = NewStdinBuffer()
	got, mu := collectInput(pt)

	// A bare ESC arrives: the debounce read blocks waiting for completing
	// bytes (on a Windows console it blocks until the next keypress).
	pt.startEscapeDebounce()
	debounceDone := make(chan struct{})
	go func() {
		pt.pollEscapeDebounce()
		close(debounceDone)
	}()

	// The 40ms fallback timer emits the bare ESC on its own.
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(*got) == 1 && (*got)[0] == "\x1b"
	}, time.Second, 5*time.Millisecond)

	// The user's next key arrives while the debounce read is still blocked.
	br.push("x")
	select {
	case <-debounceDone:
	case <-time.After(time.Second):
		t.Fatal("pollEscapeDebounce did not return")
	}

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{"\x1b", "x"}, *got,
		"the key following Escape was eaten (merged with a stale ESC)")
}
