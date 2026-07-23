// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"strings"
	"testing"
)

// TestChatViewport_UpdateLastEntry_StaleOffsetFallsBack is the regression test
// for the app-layer root cause of the mascot-redraw bug.
//
// updateLastEntry is the incremental streaming fast path: it truncates the
// frame cache to e.lineOffset and re-appends the freshly rendered last entry.
// It previously trusted e.lineOffset with no bounds check. When the offset is
// stale — larger than the live cache, which happens when entries above the
// last were removed or shrank without recomputing offsets — the truncation
// panics (slice out of range). In production that panic is swallowed by the
// render loop's recoverToLog, leaving the frame cache truncated/invalid for
// that frame: the transcript transiently renders near-empty, the canvas height
// collapses below the scrollback watermark, and the compositor repaints the
// off-screen header/mascot onto the visible screen (the mascot flash).
//
// The guard must reject the stale offset and fall back to a full rebuild so
// every entry's offset is recomputed from truth — no panic, no collapse.
func TestChatViewport_UpdateLastEntry_StaleOffsetFallsBack(t *testing.T) {
	cv := NewChatViewport()
	cv.SetAllocatedHeight(0) // no bottom-align padding for clarity
	cv.AddSystemMessage("first")
	cv.AddSystemMessage("second")
	cv.AddSystemMessage("third")
	w := 60
	if lines := cv.Render(w); len(lines) == 0 {
		t.Fatal("expected non-empty render")
	}

	// Corrupt the last entry's lineOffset to simulate a stale offset that
	// exceeds the live cache (the panic/collapse vector).
	cv.entries[len(cv.entries)-1].lineOffset = len(cv.renderCache.lines) + 50
	cv.entries[len(cv.entries)-1].dirty = true
	cv.generation++

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("updateLastEntry panicked on stale offset: %v", r)
		}
	}()
	out := cv.Render(w)
	if len(out) == 0 {
		t.Fatalf("render after stale offset produced empty output (collapse)")
	}
	joined := strings.Join(out, "\n")
	for _, want := range []string{"first", "second", "third"} {
		if !strings.Contains(joined, want) {
			t.Errorf("output missing %q after stale-offset rebuild:\n%s", want, joined)
		}
	}
}
