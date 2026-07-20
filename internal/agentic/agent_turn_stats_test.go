// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"testing"
	"time"
)

// TestGenTiming_StartsAtStreamStart verifies the output-speed timing window
// opens when the stream starts — not on the first mapped event. Providers
// whose reasoning streams as unmapped deltas (e.g. z.ai GLM reasoning_content)
// otherwise measure only the short content tail, producing absurd tok/s
// (bugs.md z.ai Issue 7: 212864.6 tok/s for a 4.1K-token turn).
func TestGenTiming_StartsAtStreamStart(t *testing.T) {
	a := &Agent{}

	// Simulate consumeStream entry: the window must open immediately.
	a.startGenTiming()
	if a.genStartTime.IsZero() {
		t.Fatal("genStartTime must be set at stream start, before any mapped event")
	}

	// A long "unmapped reasoning" phase must be inside the window.
	time.Sleep(50 * time.Millisecond)
	a.markGenStart() // first mapped event arrives late — must NOT restart the window
	elapsed := time.Since(a.genStartTime)
	if elapsed < 50*time.Millisecond {
		t.Errorf("timing window restarted on first mapped event: elapsed=%v, want >= 50ms", elapsed)
	}

	a.recordGenDuration()
	if a.genDuration < 50*time.Millisecond {
		t.Errorf("genDuration = %v, want >= 50ms (window must span reasoning phase)", a.genDuration)
	}
}

// TestFallbackOutputSpeed_SaneWindow verifies fallbackOutputSpeed derives a
// plausible speed when the window spans the whole generation.
func TestFallbackOutputSpeed_SaneWindow(t *testing.T) {
	a := &Agent{}
	a.startGenTiming()
	time.Sleep(20 * time.Millisecond)
	a.recordGenDuration()

	speed := a.fallbackOutputSpeed(100)
	if speed <= 0 {
		t.Fatalf("speed = %v, want > 0", speed)
	}
	// 100 tokens over >=20ms must be <= 5000 tok/s — anything higher implies a
	// collapsed window.
	if speed > 5000 {
		t.Errorf("speed = %.1f tok/s, want <= 5000 (window collapsed?)", speed)
	}
}
