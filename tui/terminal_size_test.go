// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import "testing"

// TestProcessTerminal_SizeFiltersTransientBlip is the bugs.md "Mascot redraw"
// regression: a single failed/degenerate TIOCGWINSZ read must NOT make Size()
// fall back to 80x24 for one frame — that blip drove the compositor's
// resize/full-repaint path and repainted the header mid-session. Once a
// plausible size is known, a transient misread returns the last good size.
//
// The ioctl itself can't be mocked here (ProcessTerminal calls term.GetSize
// on a fixed fd), so this exercises the filtering logic directly via the
// struct's cached state, which is the decision point the bug lived in.
func TestProcessTerminal_SizeFiltersTransientBlip(t *testing.T) {
	pt := &ProcessTerminal{}

	// Simulate: a real terminal was read at 120x40 (cached), then the ioctl
	// transiently fails / returns degenerate. The filter must keep 120x40.
	pt.sizeMu.Lock()
	pt.lastGoodW, pt.lastGoodH = 120, 40
	pt.sizeMu.Unlock()

	// Emulate the transient path the same way Size() does when GetSize errs.
	w, h := pt.filteredSize(0, 0, true)
	if w != 120 || h != 40 {
		t.Fatalf("transient blip must return last good size 120x40, got %dx%d", w, h)
	}

	// A degenerate read (h<3) is also filtered.
	w, h = pt.filteredSize(120, 1, false)
	if w != 120 || h != 40 {
		t.Fatalf("degenerate read must return last good size 120x40, got %dx%d", w, h)
	}

	// A genuine resize (valid, non-degenerate) updates the cache.
	w, h = pt.filteredSize(100, 30, false)
	if w != 100 || h != 30 {
		t.Fatalf("genuine resize must pass through 100x30, got %dx%d", w, h)
	}
	pt.sizeMu.Lock()
	if pt.lastGoodW != 100 || pt.lastGoodH != 30 {
		t.Fatalf("cache must update on genuine resize, got %dx%d", pt.lastGoodW, pt.lastGoodH)
	}
	pt.sizeMu.Unlock()
}

// TestProcessTerminal_SizeDefaultBeforeAnyGoodRead: with no prior good read,
// a failed ioctl still yields the historical 80x24 default so first-frame
// layout has something to work with.
func TestProcessTerminal_SizeDefaultBeforeAnyGoodRead(t *testing.T) {
	pt := &ProcessTerminal{}
	w, h := pt.filteredSize(0, 0, true)
	if w != 80 || h != 24 {
		t.Fatalf("no good read yet must default to 80x24, got %dx%d", w, h)
	}
}
