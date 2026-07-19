// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"strings"
	"sync"
	"testing"
)

// TestSegmentRender_SerializedWithScheduler is the regression test for the
// provider-quota footer crash: a plugin segment's JS render function is
// invoked from the app's drainSegmentRefreshes goroutine while scheduler
// timer callbacks also execute JS on the same goja runtime. Without the VM
// lock in the render closure, concurrent VM access clobbers vm.prg mid-call
// and goja panics with a nil dereference in vm.halted (bridge_extended.go:280).
//
// The test renders a segment from many goroutines while a JS interval timer
// fires at the minimum interval; with -race and repeated iterations, an
// unlocked render would corrupt the runtime quickly.
func TestSegmentRender_SerializedWithScheduler(t *testing.T) {
	ctx := newExtendedContext(t, t.TempDir(), NewHTTPBridge())
	sch := ctx.Extended.Scheduler
	defer sch.Stop()
	ui := ctx.Extended.UI

	bridge := runJS(t, ctx, `
		var counter = 0;
		goa.ui.addSegment({
			id: "quota",
			priority: 10,
			render: function() {
				// Touch runtime state so a concurrent VM mutation is observable.
				counter++;
				return "tok:" + counter;
			}
		});
		goa.setInterval(function() {
			// Mutate JS state from the scheduler goroutine.
			counter = counter + 1;
		}, 300);
	`)
	_ = bridge

	// Find the registered segment's render closure.
	var render func() string
	for _, s := range ui.Segments() {
		if s.ID == "quota" {
			render = s.Render
		}
	}
	if render == nil {
		t.Fatal("quota segment render closure not registered")
	}

	// Hammer the render closure from multiple goroutines (simulating the app
	// render loop + refresh drains) while the scheduler timer fires. Any
	// unsynchronized VM access shows up as a goja panic or a -race report.
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				out := render()
				if !strings.HasPrefix(out, "tok:") {
					t.Errorf("render = %q, want tok:N", out)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// TestSegmentRender_JSPanicContained verifies a render function that throws
// (or triggers a Go panic via a bridge) returns "" instead of crashing the
// caller's goroutine.
func TestSegmentRender_JSPanicContained(t *testing.T) {
	ctx := newExtendedContext(t, t.TempDir(), NewHTTPBridge())
	defer ctx.Extended.Scheduler.Stop()
	ui := ctx.Extended.UI

	runJS(t, ctx, `
		goa.ui.addSegment({
			id: "boom",
			priority: 1,
			render: function() { throw new Error("kaboom"); }
		});
		goa.ui.addSegment({
			id: "ok",
			priority: 2,
			render: function() { return "fine"; }
		});
	`)

	byID := map[string]func() string{}
	for _, s := range ui.Segments() {
		byID[s.ID] = s.Render
	}
	if got := byID["boom"](); got != "" {
		t.Fatalf("throwing render = %q, want empty", got)
	}
	if got := byID["ok"](); got != "fine" {
		t.Fatalf("healthy render = %q, want fine", got)
	}
}

// TestSegmentRender_SemanticColor covers the {text, color} render contract: a
// plugin naming a semantic color gets theme-colored output via the injected
// SegmentColor mapper, without emitting console codes itself.
func TestSegmentRender_SemanticColor(t *testing.T) {
	ctx := newExtendedContext(t, t.TempDir(), NewHTTPBridge())
	defer ctx.Extended.Scheduler.Stop()
	ctx.Extended.SegmentColor = func(name string) string {
		return map[string]string{"ok": "#3fb950", "warn": "#d29922", "critical": "#f85149", "pending": "#8b949e"}[name]
	}
	ui := ctx.Extended.UI

	runJS(t, ctx, `
		goa.ui.addSegment({
			id: "quota",
			priority: 1,
			render: function() { return { text: "[5h:7%]", color: "ok" }; }
		});
		goa.ui.addSegment({
			id: "unknown-color",
			priority: 2,
			render: function() { return { text: "plain", color: "chartreuse" }; }
		});
	`)

	byID := map[string]func() string{}
	for _, s := range ui.Segments() {
		byID[s.ID] = s.Render
	}
	got := byID["quota"]()
	if !strings.Contains(got, "[5h:7%]") {
		t.Fatalf("colored segment missing text: %q", got)
	}
	if !strings.Contains(got, "38;2;63;185;80") { // #3fb950 as truecolor fg
		t.Fatalf("colored segment missing ANSI fg: %q", got)
	}
	// Unknown color names fall back to unstyled text.
	if got := byID["unknown-color"](); got != "plain" {
		t.Fatalf("unknown color = %q, want plain text", got)
	}
}

// TestSegmentRender_NoColorMapper verifies that without a SegmentColor mapper
// configured, {text, color} degrades to plain text (no escapes).
func TestSegmentRender_NoColorMapper(t *testing.T) {
	ctx := newExtendedContext(t, t.TempDir(), NewHTTPBridge())
	defer ctx.Extended.Scheduler.Stop()
	ui := ctx.Extended.UI

	runJS(t, ctx, `
		goa.ui.addSegment({
			id: "quota",
			priority: 1,
			render: function() { return { text: "[wk:21%]", color: "critical" }; }
		});
	`)
	var render func() string
	for _, s := range ui.Segments() {
		if s.ID == "quota" {
			render = s.Render
		}
	}
	if got := render(); got != "[wk:21%]" {
		t.Fatalf("no mapper = %q, want unstyled [wk:21%%]", got)
	}
}
