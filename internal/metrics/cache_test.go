// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package metrics

import "testing"

func TestCacheHitPct(t *testing.T) {
	cases := []struct {
		name              string
		read, write, in   int
		want              float64
	}{
		{"no activity", 0, 0, 0, 0},
		{"no activity with prompt", 0, 0, 400, 0},
		{"all writes (full miss)", 0, 100, 400, 0},
		{"reads only (openai-style)", 500, 0, 500, 50}, // 500/(500+500)
		{"reads only full prompt cached", 1024, 0, 0, 100},
		{"anthropic read+write", 300, 100, 0, 75}, // 300/(300+100)
		{"read+write ignores promptN", 300, 100, 9999, 75},
		{"prompt only no cache", 0, 0, 1000, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := CacheHitPct(c.read, c.write, c.in)
			// Compare with tolerance to avoid float rounding noise.
			if abs(got-c.want) > 0.01 {
				t.Errorf("CacheHitPct(%d,%d,%d) = %.4f, want %.4f", c.read, c.write, c.in, got, c.want)
			}
		})
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
