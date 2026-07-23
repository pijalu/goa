// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"strings"
	"testing"
)

// TestChatViewport_NilCacheAfterRebuildDoesNotBlankFrame is the regression test
// for the app-layer root cause of the mascot-redraw bug.
//
// rebuildFrame's tool-widget patch path (patchRunningToolWidgets →
// updateEntryInCache) invalidates the frame cache — sets renderCache.lines =
// nil — when a running tool widget's rendered height changes mid-tick. Render
// then used to call bottomAlign(renderCache.lines) on the nil cache, returning
// a frame of blank lines for one frame. That transient empty frame collapsed
// TotalHeight(), dropped the canvas below the scrollback watermark, and made
// the compositor repaint the off-screen header/mascot onto the visible screen
// (term.log 2026-07-23 07:52:39, during a running `make quality` bash tool).
//
// The fix rebuilds the cache within the same Render when the patch invalidated
// it, so the returned frame is always consistent. This test drives the real
// code path: a running widget whose re-render grows in height during a ticker
// patch, then asserts the same Render never returns a collapsed frame.
func TestChatViewport_NilCacheAfterRebuildDoesNotBlankFrame(t *testing.T) {
	cv := NewChatViewport()
	width := 60
	cv.SetAllocatedHeight(0)

	cv.AddSystemMessage("before-tool-content")
	tc := cv.AddToolExecution("bash", `{"command":"make quality"}`)
	tc.SetStatus(ToolRunning)

	// Establish a valid, clean frame cache.
	if first := cv.Render(width); len(first) == 0 {
		t.Fatal("expected non-empty initial render")
	}

	// Simulate the ticker-patch condition WITHOUT going through a dirty-marking
	// setter (the exact path that nils the cache): force the tool widget's
	// cached rendered lines to be shorter than its next render by shrinking the
	// recorded cache, then request a ticker patch. updateEntryInCache sees the
	// height mismatch and invalidates the frame cache.
	cv.entries[len(cv.entries)-1].renderedLines = []string{"stub"} // height will differ on re-render
	cv.InvalidateRunningToolWidgets()

	out := cv.Render(width)
	joined := strings.Join(out, "\n")

	// The returned frame must be consistent: prior content present, not a
	// blank/collapsed frame, and TotalHeight must reflect real content.
	if len(out) == 0 {
		t.Fatalf("Render returned an empty frame after a cache-invalidating tool patch")
	}
	if !strings.Contains(joined, "before-tool-content") {
		t.Errorf("prior content missing after cache-invalidating tool patch (frame collapsed):\n%s", joined)
	}
	if cv.TotalHeight() == 0 {
		t.Errorf("TotalHeight() collapsed to 0 after cache-invalidating tool patch")
	}
}
