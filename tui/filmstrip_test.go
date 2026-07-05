// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"
)

func agentFrameWith(visible ...string) AgentFrame {
	return AgentFrame{Width: 80, Height: len(visible), Visible: visible}
}

func TestFilmstrip_FirstCaptureHasNoRemoved(t *testing.T) {
	f := NewFilmstrip()
	snap := f.Capture("init", agentFrameWith("hello", "world"), "Idle")

	if len(snap.Diff.AddedLines) != 2 {
		t.Errorf("expected 2 added lines on first capture, got %v", snap.Diff.AddedLines)
	}
	if len(snap.Diff.RemovedLines) != 0 {
		t.Errorf("expected 0 removed lines on first capture, got %v", snap.Diff.RemovedLines)
	}
	if snap.Diff.StatusText != "Idle" {
		t.Errorf("StatusText = %q, want Idle", snap.Diff.StatusText)
	}
}

func TestFilmstrip_DiffDetectsAddedRemoved(t *testing.T) {
	f := NewFilmstrip()
	f.Capture("s1", agentFrameWith("alpha", "beta"), "A")
	snap := f.Capture("s2", agentFrameWith("beta", "gamma"), "B")

	added := joinLines(snap.Diff.AddedLines)
	removed := joinLines(snap.Diff.RemovedLines)
	if !strings.Contains(added, "gamma") {
		t.Errorf("expected gamma in added lines, got %q", added)
	}
	if !strings.Contains(removed, "alpha") {
		t.Errorf("expected alpha in removed lines, got %q", removed)
	}
	// "beta" persists across both frames -> neither added nor removed.
	if strings.Contains(added, "beta") {
		t.Errorf("beta should not be in added lines, got %q", added)
	}
	if strings.Contains(removed, "beta") {
		t.Errorf("beta should not be in removed lines, got %q", removed)
	}
}

func TestFilmstrip_StatusTraceTracksLifecycle(t *testing.T) {
	f := NewFilmstrip()
	f.Capture("thinking", agentFrameWith("x"), "Thinking...")
	f.Capture("tool", agentFrameWith("x"), "Tool calling")
	f.Capture("after-tool", agentFrameWith("x"), "Sending request...")
	f.Capture("answering", agentFrameWith("x"), "Answering...")

	trace := f.StatusTrace()
	want := []string{"Thinking...", "Tool calling", "Sending request...", "Answering..."}
	if len(trace) != len(want) {
		t.Fatalf("StatusTrace len = %d, want %d (%v)", len(trace), len(want), trace)
	}
	for i := range want {
		if trace[i] != want[i] {
			t.Errorf("StatusTrace[%d] = %q, want %q", i, trace[i], want[i])
		}
	}
}

func TestFilmstrip_RenderIncludesSteps(t *testing.T) {
	f := NewFilmstrip()
	f.Capture("step one", agentFrameWith("visible line"), "Working")
	out := f.Render()
	for _, want := range []string{"step 0", "step one", "status: Working", "+ visible line"} {
		if !strings.Contains(out, want) {
			t.Errorf("Render() missing %q:\n%s", want, out)
		}
	}
}

func TestFilmstrip_EmptyVisibleLinesIgnored(t *testing.T) {
	f := NewFilmstrip()
	snap := f.Capture("blank", agentFrameWith("", "  ", "real"), "")
	// Only "real" should be counted as added; whitespace-only lines are noise.
	if len(snap.Diff.AddedLines) != 1 || snap.Diff.AddedLines[0] != "real" {
		t.Errorf("expected only 'real' as added, got %v", snap.Diff.AddedLines)
	}
}

func TestFilmstrip_LastReturnsMostRecent(t *testing.T) {
	f := NewFilmstrip()
	if f.Last() != nil {
		t.Fatal("Last() should be nil on empty filmstrip")
	}
	f.Capture("a", agentFrameWith("x"), "s1")
	f.Capture("b", agentFrameWith("x"), "s2")
	last := f.Last()
	if last == nil || last.Label != "b" {
		t.Fatalf("Last() = %+v, want label b", last)
	}
}

func joinLines(lines []string) string { return strings.Join(lines, "|") }
