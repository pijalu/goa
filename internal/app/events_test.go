// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import "testing"

func TestReviewOverlayGeometryFor(t *testing.T) {
	// A 24-row terminal reserves 0 top rows and 5 rows (input 3 + footer 2)
	// at the bottom, leaving 19 rows for the review overlay.
	geo := reviewOverlayGeometryFor(24)
	if geo.height != 19 {
		t.Errorf("height = %d, want 19", geo.height)
	}
	if geo.bottomOffset != 5 {
		t.Errorf("bottomOffset = %d, want 5", geo.bottomOffset)
	}
	if geo.width != 0 {
		t.Errorf("width = %d, want 0 (auto)", geo.width)
	}

	// Very small terminals fall back to full screen.
	geo = reviewOverlayGeometryFor(6)
	if geo.height != 6 {
		t.Errorf("small terminal height = %d, want 6", geo.height)
	}
}
