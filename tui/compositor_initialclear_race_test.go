// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"sync"
	"testing"
)

// raceTerminal is a Terminal whose Write blocks (once) inside the call until
// released. It lets a test hold one Compositor method inside its terminal
// write while a second Compositor method starts — deterministically exposing
// any terminal write that runs outside the Compositor's mutex.
type raceTerminal struct {
	w, h    int
	mu      sync.Mutex
	out     strings.Builder
	entered chan struct{} // closed when the first (blocking) Write begins
	release chan struct{} // closed to let the first blocking Write return
	once    sync.Once
}

func (rt *raceTerminal) Start(func(string), func())     {}
func (rt *raceTerminal) Stop()                          {}
func (rt *raceTerminal) SetRaw() (func(), error)        { return func() {}, nil }
func (rt *raceTerminal) Size() (int, int)               { return rt.w, rt.h }
func (rt *raceTerminal) HideCursor()                    {}
func (rt *raceTerminal) ShowCursor()                    {}
func (rt *raceTerminal) ClearScreen()                   {}
func (rt *raceTerminal) SetTitle(string)                {}

func (rt *raceTerminal) Write(p []byte) (int, error) {
	// The first Write blocks until released, staying inside its critical
	// section so a concurrent unsynchronized Write can overlap it.
	rt.once.Do(func() {
		close(rt.entered)
		<-rt.release
	})
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.out.Write(p)
}
func (rt *raceTerminal) WriteString(s string) { _, _ = rt.Write([]byte(s)) }

// TestCompositor_InitialClear_SerializedWithRender is the regression test for
// the bugs.md entry "race: TestRunWizardWithTerminal_FirstFrameRenders".
//
// Root cause: Compositor.InitialClear wrote to the shared terminal WITHOUT
// holding c.mu, while every other Compositor method (Render/Restore/Clear/
// Buffer) locks it. TUI.Start() calls InitialClear on the caller's goroutine
// (NOT the renderLoop), so it can run concurrently with a shutdown
// (Stop→Restore) or an in-flight frame — two goroutines then write to the
// terminal with no happens-before edge, which the race detector reports.
//
// The test blocks InitialClear's terminal write inside its (unlocked, on the
// unfixed code) section, then issues a concurrent Render. With the fix both
// take c.mu and serialize (detector silent); without it the two terminal
// writes overlap → DATA RACE.
func TestCompositor_InitialClear_SerializedWithRender(t *testing.T) {
	term := &raceTerminal{
		w: 40, h: 10,
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	comp := NewCompositor(term)
	scene := &Scene{
		TerminalW: 40, TerminalH: 10,
		Layers: []Layer{
			{Name: "chat", Kind: LayerBase, Rect: Rect{X: 0, Y: 0, W: 40, H: 8}, Content: []string{"a", "b", "c"}},
		},
	}

	// Goroutine 1: the Start-path clear (the method under test). Its terminal
	// Write blocks inside the critical section until we release it.
	clearDone := make(chan struct{})
	go func() {
		defer close(clearDone)
		comp.InitialClear()
	}()
	<-term.entered // InitialClear is now parked inside its terminal Write

	// Goroutine 2: a concurrent frame render (the renderLoop path).
	renderDone := make(chan struct{})
	go func() {
		defer close(renderDone)
		comp.Render(scene)
	}()

	// Release the blocked clear and let both complete. With the fix Render
	// blocked on c.mu until InitialClear returned; without it Render's writes
	// overlapped InitialClear's blocked Write.
	close(term.release)
	<-clearDone
	<-renderDone
}
