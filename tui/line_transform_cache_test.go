// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"
	"testing"
)

// countingTransform wraps a transform and counts how often it actually runs.
type countingTransform struct {
	calls int
	fn    func(string) string
}

func (c *countingTransform) do(s string) string {
	c.calls++
	return c.fn(s)
}

// TestLineTransformCache_TransformsOnlyUnstableLines is the streaming-cost
// invariant: applying over a growing list whose leading `stable` prefix only
// extends must transform each line at most once.
func TestLineTransformCache_TransformsOnlyUnstableLines(t *testing.T) {
	var c lineTransformCache
	ct := &countingTransform{fn: func(s string) string { return "[" + s + "]" }}

	lines := make([]string, 0, 100)
	stable := 0
	for i := 0; i < 50; i++ {
		lines = append(lines, fmt.Sprintf("line%02d", i))
		// Every 10th appended line becomes part of the stable prefix.
		if i%10 == 9 {
			stable = len(lines)
		}
		before := ct.calls
		prevSrc := c.src
		got := c.apply(lines, 80, stable, ct.do)
		if len(got) != len(lines) {
			t.Fatalf("len = %d, want %d", len(got), len(lines))
		}
		// Work per call covers exactly the lines not already memoized (the open
		// tail plus lines that just became stable) — never the cached prefix.
		if ran := ct.calls - before; ran > len(lines)-prevSrc {
			t.Fatalf("call %d transformed %d lines (list %d, cached %d): cache not reused", i, ran, len(lines), prevSrc)
		}
	}
}

func TestLineTransformCache_ValuesMatchDirectTransform(t *testing.T) {
	var c lineTransformCache
	fn := func(s string) string { return s + "!" }
	lines := []string{"a", "b", "c", "d"}
	got := c.apply(lines, 10, 2, fn)
	if want := "a! b! c! d!"; strings.Join(got, " ") != want {
		t.Fatalf("apply = %q, want %q", strings.Join(got, " "), want)
	}
	lines = append(lines, "e")
	got = c.apply(lines, 10, 4, fn)
	if want := "a! b! c! d! e!"; strings.Join(got, " ") != want {
		t.Fatalf("append apply = %q, want %q", strings.Join(got, " "), want)
	}
}

func TestLineTransformCache_Invalidation(t *testing.T) {
	fn := func(s string) string { return "<" + s + ">" }

	t.Run("width change", func(t *testing.T) {
		var c lineTransformCache
		c.apply([]string{"a", "b"}, 10, 2, fn)
		gen := func(s string) string { return "(" + s + ")" }
		got := c.apply([]string{"a", "b"}, 20, 2, gen)
		if want := "(a) (b)"; strings.Join(got, " ") != want {
			t.Fatalf("width-change apply = %q, want %q", strings.Join(got, " "), want)
		}
	})

	t.Run("shrink", func(t *testing.T) {
		var c lineTransformCache
		c.apply([]string{"a", "b", "c"}, 10, 3, fn)
		gen := func(s string) string { return "{" + s + "}" }
		got := c.apply([]string{"a"}, 10, 0, gen)
		if want := "{a}"; strings.Join(got, " ") != want {
			t.Fatalf("shrink apply = %q, want %q", strings.Join(got, " "), want)
		}
	})

	t.Run("stable count drops", func(t *testing.T) {
		var c lineTransformCache
		c.apply([]string{"a", "b", "c"}, 10, 3, fn)
		gen := func(s string) string { return "/" + s + "/" }
		got := c.apply([]string{"x", "y", "c"}, 10, 1, gen)
		if want := "/x/ /y/ /c/"; strings.Join(got, " ") != want {
			t.Fatalf("stable-drop apply = %q, want %q", strings.Join(got, " "), want)
		}
	})
}

// TestLineTransformCache_ReturnIsOwned guards the aliasing contract: a caller
// keeps the returned slice across frames (the render cache does), so a later
// apply must never mutate it.
func TestLineTransformCache_ReturnIsOwned(t *testing.T) {
	var c lineTransformCache
	fn := func(s string) string { return s + "-x" }
	first := c.apply([]string{"a", "b"}, 10, 1, fn)
	snapshot := append([]string(nil), first...)
	c.apply([]string{"a", "b", "c"}, 10, 3, fn)
	for i := range snapshot {
		if first[i] != snapshot[i] {
			t.Fatalf("first result mutated by later apply: %q != %q", first[i], snapshot[i])
		}
	}
}
