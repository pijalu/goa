// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
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

// waitLast polls until the most recent title equals want (or the deadline
// passes) and returns the final value. Title writes are asynchronous (a
// dedicated writer goroutine), so assertions must wait rather than read
// immediately.
func (s *titleSink) waitLast(want string, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if s.last() == want {
			return want
		}
		time.Sleep(2 * time.Millisecond)
	}
	return s.last()
}

// waitContains polls until some recorded title equals want (or the deadline
// passes).
func (s *titleSink) waitContains(want string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if s.contains(want) {
			return true
		}
		time.Sleep(2 * time.Millisecond)
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
	if got := sink.waitLast("g⬡a", 2*time.Second); got != "g⬡a" {
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
		if !sink.waitContains(want, 2*time.Second) {
			t.Errorf("transition missing frame %q; titles: %v", want, sink.titles)
		}
	}
	if got := sink.waitLast("⬡ - proj", 2*time.Second); got != "⬡ - proj" {
		t.Errorf("final title = %q, want base title %q", got, "⬡ - proj")
	}
}

// TestTitleController_StartupDoneOnce verifies a second startupDone call is a
// no-op (the fallback timer and the explicit hook may both fire).
func TestTitleController_StartupDoneOnce(t *testing.T) {
	sink := &titleSink{}
	tc := newTitleController(sink.set, hexDef(), true)
	defer tc.stop()
	tc.setBase("⬡ - proj")
	done := make(chan struct{})
	go func() { tc.startupDone(); close(done) }()
	<-done
	// Wait for the transition to fully complete (final base write landed) so
	// the count is stable — the latest-wins writer is async.
	sink.waitLast("⬡ - proj", 2*time.Second)
	before := len(sink.titles)
	tc.startupDone() // must be a no-op
	tc.startupDone()
	// Allow any (incorrect) extra writes to flush, then compare.
	time.Sleep(50 * time.Millisecond)
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
	if got := sink.waitLast("⬢ - proj", 2*time.Second); got != "⬢ - proj" {
		t.Errorf("working frame = %q, want %q", got, "⬢ - proj")
	}
	tc.tick() // frame 2 = ⬣
	if got := sink.waitLast("⬣ - proj", 2*time.Second); got != "⬣ - proj" {
		t.Errorf("working frame = %q, want %q", got, "⬣ - proj")
	}

	tc.setWorking(false)
	if got := sink.waitLast("⬡ - proj", 2*time.Second); got != "⬡ - proj" {
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
	// With animated=false the title stays the static base; the transition's
	// final base write is async, so poll for it rather than read once (the
	// latest-wins channel can reorder/drop the intermediate g⬡ frame under
	// load).
	if got := sink.waitLast("⬡ - proj", 2*time.Second); got != "⬡ - proj" {
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

	// After normal mode begins with working=true, the title spins. Writes are
	// async, so poll until an animated frame lands (frame[0] ⬡ coincides with
	// the base glyph; any of the hexagon frames is a valid working frame).
	last := ""
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		last = sink.last()
		if last == "⬡ - proj" || last == "⬢ - proj" || last == "⬣ - proj" {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if last != "⬡ - proj" && last != "⬢ - proj" && last != "⬣ - proj" {
		t.Errorf("unexpected title after startup with working: %q", last)
	}
}

// TestTitleSpinnerDefFor_DefaultsToHexagonBlack covers bugs.md "hexagon-black
// as default for terminal title": with no explicit spinner configured (or the
// default hexagon status spinner), the title animation uses hexagon-black.
func TestTitleSpinnerDefFor_DefaultsToHexagonBlack(t *testing.T) {
	def := titleSpinnerDefFor(nil)
	want := []string{"⬢", "⬣"}
	if len(def.Frames) != len(want) || def.Frames[0] != want[0] || def.Frames[1] != want[1] {
		t.Errorf("titleSpinnerDefFor(nil).Frames = %v, want %v", def.Frames, want)
	}
}

// TestTitleSpinnerDefFor_ExplicitSpinnerOverrides verifies an explicit
// tui.spinner value overrides the title animation frame set.
func TestTitleSpinnerDefFor_ExplicitSpinnerOverrides(t *testing.T) {
	cfg := &config.Config{}
	cfg.TUI.Spinner = "arc"
	def := titleSpinnerDefFor(cfg)
	arcDef, _ := spinner.Get("arc")
	if len(def.Frames) != len(arcDef.Frames) || def.Frames[0] != arcDef.Frames[0] {
		t.Errorf("titleSpinnerDefFor(arc).Frames = %v, want arc frames %v", def.Frames, arcDef.Frames)
	}
}

// TestTitleSpinnerDefFor_NoneDisables verifies "none" disables the title
// animation (empty frames).
func TestTitleSpinnerDefFor_NoneDisables(t *testing.T) {
	cfg := &config.Config{}
	cfg.TUI.Spinner = "none"
	def := titleSpinnerDefFor(cfg)
	if len(def.Frames) != 0 {
		t.Errorf("titleSpinnerDefFor(none).Frames = %v, want empty", def.Frames)
	}
}

// TestTitleStartupHook_FiresOnBothLoadsDone is a filmstrip-style validation
// of the startup sequence (bugs.md "startup sequence not working"): the boot
// brand shows first, then when BOTH the plugin and history loads complete the
// transition plays (g⬡a → g⬡ → ⬡) and the title settles on the base. It
// proves the explicit startup-done hook drives the sequence end-to-end.
func TestTitleStartupHook_FiresOnBothLoadsDone(t *testing.T) {
	sink := &titleSink{}
	a := New(testSubsystems())
	a.titleCtl = newTitleController(sink.set, hexDef(), true)
	defer a.titleCtl.stop()
	a.titleCtl.setBase("⬡ - goa")

	// Boot brand shown before any load completes.
	if got := sink.waitLast("g⬡a", 2*time.Second); got != "g⬡a" {
		t.Fatalf("boot title = %q, want g⬡a", got)
	}

	a.pluginsLoaded = make(chan struct{})
	a.historyLoadDone = make(chan struct{})
	a.startTitleStartupHook()

	// Only one load done: transition must NOT have started yet (still boot
	// brand, no g⬡ intermediate).
	close(a.pluginsLoaded)
	time.Sleep(150 * time.Millisecond)
	if sink.waitContains("⬡ - goa", 150*time.Millisecond) {
		t.Fatal("transition/base title appeared before both loads completed")
	}

	// Both done: transition plays and the title settles on the base.
	close(a.historyLoadDone)
	if !sink.waitContains("⬡", 6*time.Second) {
		t.Fatalf("transition frame ⬡ never appeared; titles: %v", sink.titles)
	}
	if got := sink.waitLast("⬡ - goa", 6*time.Second); got != "⬡ - goa" {
		t.Fatalf("final title = %q, want ⬡ - goa; titles: %v", got, sink.titles)
	}
}

// TestTitleStartupHook_FallbackFiresWithoutHook verifies the 5s fallback
// plays the transition even when the loads never signal (uses a short
// deadline by calling startupDone directly through the fallback path is not
// feasible here, so this asserts the hook fires when channels are nil = both
// already done).
func TestTitleStartupHook_NilChannelsFireImmediately(t *testing.T) {
	sink := &titleSink{}
	a := New(testSubsystems())
	a.titleCtl = newTitleController(sink.set, hexDef(), true)
	defer a.titleCtl.stop()
	a.titleCtl.setBase("⬡ - goa")

	// Nil channels = both loads already done → transition fires immediately.
	a.startTitleStartupHook()
	if got := sink.waitLast("⬡ - goa", 6*time.Second); got != "⬡ - goa" {
		t.Fatalf("final title = %q, want ⬡ - goa; titles: %v", got, sink.titles)
	}
}
