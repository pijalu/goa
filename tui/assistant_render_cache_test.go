// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

// The memoized assistant render must be byte-identical to a fresh render, and
// SetText must invalidate the cache so streamed growth is reflected. Guards
// the A2 memoization against serving stale frames.
func TestAssistantMessage_RenderCache_Correctness(t *testing.T) {
	m := newAssistantMessage("# Title\n\nsome **bold** text")
	first := m.Render(100)
	// Second render with unchanged text → cache hit, must equal first.
	second := m.Render(100)
	if len(first) != len(second) {
		t.Fatalf("cached render length differs: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("cached render line %d differs:\n%q\n%q", i, first[i], second[i])
		}
	}

	// SetText must invalidate: the new content must appear.
	m.SetText("# Title\n\nsome **bold** text\n\nnew paragraph appended")
	grown := m.Render(100)
	found := false
	for _, l := range grown {
		if strings.Contains(ansi.Strip(l), "new paragraph appended") {
			found = true
		}
	}
	if !found {
		t.Errorf("SetText did not invalidate cache; new content missing")
	}

	// Width change must re-render (cache keyed on width).
	narrow := m.Render(60)
	if len(narrow) == 0 {
		t.Error("render at width 60 produced no lines")
	}
}

// SetFinishReason must invalidate so the footer line appears on completion.
func TestAssistantMessage_RenderCache_FinishReason(t *testing.T) {
	m := newAssistantMessage("body")
	_ = m.Render(100)
	m.SetFinishReason("stop", 123, 4567)
	out := m.Render(100)
	joined := ""
	for _, l := range out {
		joined += ansi.Strip(l)
	}
	if !strings.Contains(joined, "stop") {
		t.Errorf("finish reason line missing after SetFinishReason: %q", joined)
	}
}
