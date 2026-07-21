// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/spinner"
)

// titleSink records every title written by the controller.
type titleSink struct {
	mu     sync.Mutex
	titles []string
}

func (s *titleSink) set(t string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.titles = append(s.titles, t)
}

func (s *titleSink) last() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.titles) == 0 {
		return ""
	}
	return s.titles[len(s.titles)-1]
}

func (s *titleSink) contains(t string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.titles {
		if x == t {
			return true
		}
	}
	return false
}

func hexDef() spinner.Definition {
	return spinner.Definition{Interval: 50, Frames: []string{"⬡", "⬢", "⬣", "⬢"}}
}

// TestTitleController_BootShowsBrand covers bugs.md "Title bar startup
// sequence": the boot title g⬡a is emitted as early as construction.
func TestTitleController_BootShowsBrand(t *testing.T) {
	sink := &titleSink{}
	tc := newTitleController(sink.set, hexDef(), true)
	defer tc.stop()
	if got := sink.last(); got != "g⬡a" {
		t.Fatalf("boot title = %q, want g⬡a", got)
	}
}

// TestTitleController_StartupDonePlaysTransition verifies the one-shot
// g⬡a → g⬡ → ⬡ transition at startup completion, ending in normal mode on
// the base title.
func TestTitleController_StartupDonePlaysTransition(t *testing.T) {
	sink := &titleSink{}
	tc := newTitleController(sink.set, hexDef(), true)
	defer tc.stop()
	tc.setBase("⬡ - proj")

	done := make(chan struct{})
	go func() {
		tc.startupDone()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("transition did not complete within 5s")
	}

	for _, want := range []string{"g⬡a", "g⬡", "⬡"} {
		if !sink.contains(want) {
			t.Errorf("transition missing frame %q; titles: %v", want, sink.titles)
		}
	}
	if got := sink.last(); got != "⬡ - proj" {
		t.Errorf("final title = %q, want base title %q", got, "⬡ - proj")
	}
}

// TestTitleController_StartupDoneOnce verifies a second startupDone call is a
// no-op (the fallback timer and the explicit hook may both fire).
func TestTitleController_StartupDoneOnce(t *testing.T) {
	sink := &titleSink{}
	tc := newTitleController(sink.set, hexDef(), true)
	defer tc.stop()
	done := make(chan struct{})
	go func() { tc.startupDone(); close(done) }()
	<-done
	before := len(sink.titles)
	tc.startupDone() // must be a no-op
	tc.startupDone()
	if len(sink.titles) != before {
		t.Errorf("second startupDone wrote titles: before=%d after=%d", before, len(sink.titles))
	}
}

// TestTitleController_WorkingAnimatesWithFrames covers bugs.md "Animated
// title bar while working": in normal mode, working=true spins the title with
// the spinner frames, preserving the contextual suffix; idle restores base.
func TestTitleController_WorkingAnimatesWithFrames(t *testing.T) {
	sink := &titleSink{}
	tc := newTitleController(sink.set, hexDef(), true)
	defer tc.stop()
	tc.setBase("⬡ - proj")
	done := make(chan struct{})
	go func() { tc.startupDone(); close(done) }()
	<-done

	tc.setWorking(true)
	// Working frame = spinner frame + contextual suffix. frame[0] is ⬡, which
	// coincides with the base glyph; tick to a distinguishable frame.
	tc.tick() // frame 1 = ⬢
	if got := sink.last(); got != "⬢ - proj" {
		t.Errorf("working frame = %q, want %q", got, "⬢ - proj")
	}
	tc.tick() // frame 2 = ⬣
	if got := sink.last(); got != "⬣ - proj" {
		t.Errorf("working frame = %q, want %q", got, "⬣ - proj")
	}

	tc.setWorking(false)
	if got := sink.last(); got != "⬡ - proj" {
		t.Errorf("idle title = %q, want base %q", got, "⬡ - proj")
	}
}

// TestTitleController_AnimatedOffStaysStatic verifies the config-off path:
// working never animates the title (bugs.md: configurable, default on).
func TestTitleController_AnimatedOffStaysStatic(t *testing.T) {
	sink := &titleSink{}
	tc := newTitleController(sink.set, hexDef(), false) // animated = false
	defer tc.stop()
	tc.setBase("⬡ - proj")
	done := make(chan struct{})
	go func() { tc.startupDone(); close(done) }()
	<-done

	tc.setWorking(true)
	tc.tick()
	if got := sink.last(); got != "⬡ - proj" {
		t.Errorf("animated-off working title = %q, want static base %q", got, "⬡ - proj")
	}
}

// TestTitleController_WorkingBeforeStartupDone verifies the working flag is
// recorded (not lost) when set during the startup/transition phase and
// applied once normal mode begins.
func TestTitleController_WorkingBeforeStartupDone(t *testing.T) {
	sink := &titleSink{}
	tc := newTitleController(sink.set, hexDef(), true)
	defer tc.stop()
	tc.setBase("⬡ - proj")
	tc.setWorking(true) // before startupDone — must not crash nor animate yet

	done := make(chan struct{})
	go func() { tc.startupDone(); close(done) }()
	<-done

	// After normal mode begins with working=true, the title spins.
	last := sink.last()
	if last != "⬡ - proj" && last != "⬢ - proj" && last != "⬣ - proj" {
		t.Errorf("unexpected title after startup with working: %q", last)
	}
}
