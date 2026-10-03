// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"testing"
)

// The page must stay cheap per frame: it is repainted at the engine's own tick
// rate, so anything that forces a synchronous layout inside the frame path
// costs a reflow 20-60 times a second. Cell size is measured when the geometry
// changes, not when a row is painted.
func TestClientJS_FramesDoNotRemeasureTheFont(t *testing.T) {
	h := newClientHarness(t)
	before := int(h.call(t, "__measures").ToInteger())
	if before == 0 {
		t.Fatal("the page never measured its cell size at load")
	}

	for i := 0; i < 5; i++ {
		h.deliver(t, frameDoc(t, i, "streaming row"))
	}
	if after := int(h.call(t, "__measures").ToInteger()); after != before {
		t.Errorf("painting 5 frames took %d measurements, want %d (one per geometry change)",
			after-before, 0)
	}

	// A geometry change is exactly when re-measuring is correct.
	h.call(t, "__fireWindow", "resize")
	if after := int(h.call(t, "__measures").ToInteger()); after <= before {
		t.Error("a window resize did not re-measure the cell size")
	}
}

// Keys the browser owns must keep their browser meaning. Swallowing F5 (reload)
// or F12 (devtools) traps the user in the page.
func TestClientJS_KeepsBrowserReservedKeys(t *testing.T) {
	h := newClientHarness(t)
	for _, ev := range []map[string]any{
		{"key": "F5", "code": "F5"},
		{"key": "F12", "code": "F12"},
		{"key": "F11", "code": "F11"},
		{"key": "r", "code": "KeyR", "ctrlKey": true},
		{"key": "I", "code": "KeyI", "ctrlKey": true, "shiftKey": true},
	} {
		before := len(h.sent(t))
		if claimed := h.keydown(t, ev); claimed {
			t.Errorf("key %v was claimed; the browser must keep it", ev)
		}
		if got := len(h.sent(t)); got != before {
			t.Errorf("key %v was sent to the engine anyway", ev)
		}
	}
}

// Keys the engine binds must still be claimed, or Ctrl+W would close the tab
// instead of deleting a word.
func TestClientJS_ClaimsEngineKeys(t *testing.T) {
	h := newClientHarness(t)
	for _, ev := range []map[string]any{
		{"key": "w", "code": "KeyW", "ctrlKey": true},
		{"key": "l", "code": "KeyL", "ctrlKey": true},
		{"key": "a", "code": "KeyA"},
		{"key": "Enter", "code": "Enter"},
	} {
		before := len(h.sent(t))
		if claimed := h.keydown(t, ev); !claimed {
			t.Errorf("key %v was left to the browser; the engine binds it", ev)
		}
		sentKeys := 0
		for _, m := range h.sent(t)[before:] {
			if m["t"] == "key" {
				sentKeys++
			}
		}
		if sentKeys != 1 {
			t.Errorf("key %v sent %d key messages, want 1", ev, sentKeys)
		}
	}
}
