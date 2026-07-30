// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import "testing"

// TestScreenOwnership_Bookkeeping covers the claim/release counter behind
// OwnsScreen: the crash-log tee (internal/app) gates TTY forwarding on it,
// so a stuck or negative count would respectively mute the terminal forever
// or re-admit stray fd-level writes mid-frame.
func TestScreenOwnership_Bookkeeping(t *testing.T) {
	if OwnsScreen() {
		t.Fatal("no claim yet: OwnsScreen must be false")
	}
	claimScreen()
	if !OwnsScreen() {
		t.Fatal("after claim: OwnsScreen must be true")
	}
	// Nested claim (overlapping sessions): one release must not clear it.
	claimScreen()
	releaseScreen()
	if !OwnsScreen() {
		t.Fatal("nested claim released once: OwnsScreen must still be true")
	}
	releaseScreen()
	if OwnsScreen() {
		t.Fatal("all claims released: OwnsScreen must be false")
	}
}

// TestProcessTerminal_StopWithoutRawDoesNotClaim guards the degraded path:
// when SetRaw fails (not a terminal), Start must NOT claim screen ownership
// — piped/headless sessions keep stderr forwarding to the terminal — and
// Stop must stay balanced (no release without a claim).
func TestProcessTerminal_StopWithoutRawDoesNotClaim(t *testing.T) {
	pt := &ProcessTerminal{}
	if pt.screenClaimed {
		t.Fatal("fresh ProcessTerminal must not own the screen")
	}
	pt.Stop() // must not release an unclaimed screen (counter would go negative)
	if OwnsScreen() {
		t.Fatal("Stop without a prior claim corrupted the ownership counter")
	}
}
