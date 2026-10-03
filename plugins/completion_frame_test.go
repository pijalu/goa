// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"testing"
	"time"
)

// TestCompletion_DoesNotBlockWhileFrameHeld is the regression for "startup
// blocked: / completion stalls behind the quota plugin's startup prime".
//
// Command completion is a UI-facing path: the editor calls it inline on the
// commandLoop (tui/editor_autocomp.go — scheduleAutoComp → updateAutoComp →
// completer.Complete), and the commandLoop is the sole owner of input and
// render state. A blocking enterFrame() there freezes the whole keyboard for as
// long as the plugin's frame is parked on a blocking bridge call — seconds at
// startup, while the bundled quota plugin primes its cache over provider HTTP.
//
// Completion is best-effort and recomputed on the next keystroke, so a busy
// frame must return immediately with no candidates instead of waiting. This is
// the same discipline buildSegmentRender already applies via tryEnterFrame.
func TestCompletion_DoesNotBlockWhileFrameHeld(t *testing.T) {
	var registered func(prefix string) []Completion

	ctx := newExtendedContext(t, t.TempDir(), NewHTTPBridge())
	ctx.RegisterCompletion = func(name string, fn func(string) []Completion) error {
		registered = fn
		return nil
	}

	bridge := NewJSBridge(PluginDef{ID: "demo", Permissions: []string{"network"}}, ctx)

	// Hold the runtime frame for the busy check: this is the state the quota
	// prime leaves behind (a live frame across goa.http.fetch). A completion
	// call that waits for a free frame would block until the deadline below.
	unlock := bridge.enterFrame()
	held := true
	defer func() {
		if held {
			unlock()
		}
	}()

	if _, err := bridge.vm.RunString(`
		goa.registerCompletion("demo", function (prefix) {
			return [{ value: prefix + "-hit", description: "from JS" }];
		});
	`); err != nil {
		t.Fatalf("RunString: %v", err)
	}
	if registered == nil {
		t.Fatal("goa.registerCompletion did not reach the host")
	}

	type result struct{ comps []Completion }
	done := make(chan result, 1)
	go func() {
		done <- result{comps: registered("re")}
	}()

	select {
	case r := <-done:
		if len(r.comps) != 0 {
			t.Fatalf("busy frame must yield no candidates, got %d: %+v", len(r.comps), r.comps)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("completion blocked on a busy runtime frame: on the commandLoop this freezes the keyboard for the whole plugin prime")
	}

	// Releasing the frame must restore real completions: the skip is a busy
	// signal, not a permanent loss of the completer.
	unlock()
	held = false

	if comps := registered("re"); len(comps) != 1 || comps[0].Value != "re-hit" {
		t.Fatalf("after the frame drains, completion must return the JS result, got %+v", comps)
	}
}
