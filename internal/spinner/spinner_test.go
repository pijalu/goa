// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package spinner

import "testing"

// TestBuiltinSpinnersLoad ensures the embedded spinners.json parses and
// contains the expected definitions.
func TestBuiltinSpinnersLoad(t *testing.T) {
	all := All()
	if len(all) == 0 {
		t.Fatal("no spinner definitions loaded")
	}
	if _, ok := Get("arc"); !ok {
		t.Error("default spinner 'arc' missing")
	}
}

// TestRequestedSpinners covers bugs.md "Additional spinner animation":
// the three unicode animations must exist with the exact frames requested.
func TestRequestedSpinners(t *testing.T) {
	cases := map[string][]string{
		"orbit":    {"⊙", "⊚", "⊛", "⊚"},
		"quadrant": {"◴", "◷", "◶", "◵"},
		"flare":    {"✴", "✳", "✵", "✷", "✸", "✹", "✺", "✹", "✸", "✷", "✵", "✳"},
	}
	for name, want := range cases {
		d, ok := Get(name)
		if !ok {
			t.Errorf("spinner %q not registered", name)
			continue
		}
		if len(d.Frames) != len(want) {
			t.Errorf("spinner %q frames = %v, want %v", name, d.Frames, want)
			continue
		}
		for i := range want {
			if d.Frames[i] != want[i] {
				t.Errorf("spinner %q frame[%d] = %q, want %q", name, i, d.Frames[i], want[i])
			}
		}
		if d.IntervalMS() <= 0 {
			t.Errorf("spinner %q has non-positive interval", name)
		}
	}
}

// TestNamesIncludesRequested ensures the new spinners are listed by Names().
func TestNamesIncludesRequested(t *testing.T) {
	names := Names()
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	for _, want := range []string{"orbit", "quadrant", "flare"} {
		if !set[want] {
			t.Errorf("Names() missing %q", want)
		}
	}
}
