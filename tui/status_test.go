// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"sync"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/spinner"
)

func resetSpinner() {
	spinnerMu.Lock()
	defer spinnerMu.Unlock()
	currentSpinner = spinner.Definition{}
}

func TestStatusMsg_Render_Spacer(t *testing.T) {
	defer resetSpinner()

	sm := NewStatusMsg()
	sm.Show("Thinking...")

	lines := sm.Render(80)
	if len(lines) == 0 {
		t.Fatal("Render returned no lines for visible status")
	}
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines, got %d", len(lines))
	}
	line := lines[1]
	stripped := ansi.Strip(line)

	if !strings.Contains(stripped, "Thinking...") {
		t.Errorf("status line missing text %q:\n  got: %q", "Thinking...", stripped)
	}

	hasIndicator := strings.Contains(stripped, "◆") || strings.Contains(stripped, "◜")
	if !hasIndicator {
		t.Errorf("status line missing spinner indicator:\n  got: %q", stripped)
	}

	diamondIdx := strings.Index(stripped, "◆")
	frameIdx := strings.Index(stripped, "◜")
	textIdx := strings.Index(stripped, "Thinking...")
	indicatorIdx := diamondIdx
	if indicatorIdx < 0 {
		indicatorIdx = frameIdx
	}
	if indicatorIdx < 0 || textIdx < 0 {
		t.Fatalf("could not locate indicator and text in line: %q", stripped)
	}
	if textIdx <= indicatorIdx+1 {
		t.Errorf("no space between spinner indicator and text:\n  stripped: %q\n  indicator at %d, text at %d",
			stripped, indicatorIdx, textIdx)
	}
}

func TestStatusMsg_Render_Spacer_WithAnimatedSpinner(t *testing.T) {
	def := spinner.Definition{
		Interval: 100,
		Frames:   []string{"◜", "◠", "◝"},
	}
	SetSpinner(def)
	defer resetSpinner()

	sm := NewStatusMsg()
	sm.Show("Processing...")

	lines := sm.Render(80)
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines, got %d", len(lines))
	}
	line := lines[1]
	stripped := ansi.Strip(line)

	if !strings.Contains(stripped, "Processing...") {
		t.Errorf("status line missing text: %q", stripped)
	}
	if !strings.Contains(stripped, "◜") {
		t.Errorf("status line missing spinner frame ◜: %q", stripped)
	}

	frameIdx := strings.Index(stripped, "◜")
	textIdx := strings.Index(stripped, "Processing...")
	if frameIdx < 0 || textIdx < 0 {
		t.Fatalf("could not locate frame and text: %q", stripped)
	}
	if textIdx <= frameIdx+1 {
		t.Errorf("no space between animated spinner frame and text:\n  stripped: %q\n  frame at %d, text at %d",
			stripped, frameIdx, textIdx)
	}
}

func TestStatusMsg_Render_Hidden(t *testing.T) {
	sm := NewStatusMsg()
	lines := sm.Render(80)
	if lines != nil {
		t.Errorf("expected nil for hidden status, got %d lines", len(lines))
	}
}

func TestStatusMsg_Render_ZeroWidth(t *testing.T) {
	sm := NewStatusMsg()
	sm.Show("test")
	lines := sm.Render(0)
	if lines != nil {
		t.Errorf("expected nil for zero width, got %d lines", len(lines))
	}
}

func TestStatusMsg_SpinnerText(t *testing.T) {
	defer resetSpinner()

	SetSpinner(spinner.Definition{
		Interval: 100,
		Frames:   []string{"⠋", "⠙", "⠹"},
	})
	defer resetSpinner()

	sm := NewStatusMsg()

	if got := sm.SpinnerText(); got != "◆" {
		t.Errorf("SpinnerText() = %q, want %q", got, "◆")
	}

	sm.Show("test")
	if got := sm.SpinnerText(); got == "◆" {
		t.Error("SpinnerText() returned static diamond while spinning")
	}

	sm.Clear()
	if got := sm.SpinnerText(); got != "◆" {
		t.Errorf("SpinnerText() after Clear = %q, want %q", got, "◆")
	}
}

func TestStatusMsg_Text(t *testing.T) {
	sm := NewStatusMsg()
	if got := sm.Text(); got != "" {
		t.Errorf("Text() = %q, want empty", got)
	}
	sm.Show("hello")
	if got := sm.Text(); got != "hello" {
		t.Errorf("Text() = %q, want %q", got, "hello")
	}
	sm.Clear()
	if got := sm.Text(); got != "" {
		t.Errorf("Text() after Clear = %q, want empty", got)
	}
}

func TestStatusMsg_IsVisible(t *testing.T) {
	sm := NewStatusMsg()
	if sm.IsVisible() {
		t.Error("IsVisible() = true before Show")
	}
	sm.Show("test")
	if !sm.IsVisible() {
		t.Error("IsVisible() = false after Show")
	}
	sm.Clear()
	if sm.IsVisible() {
		t.Error("IsVisible() = true after Clear")
	}
}

// TestStatusMsg_ConcurrentShowClear proves the Actor-model invariant: Show
// and Clear are serialized by the commandLoop. Callers MUST route them through
// Apply (the loop is the sole state owner); concurrent direct calls are
// forbidden by construction. The previous mutex-protected concurrency test is
// replaced by this loop-driven one (the commandLoop is the sole state owner).
func TestStatusMsg_ConcurrentShowClear(t *testing.T) {
	sm := NewStatusMsg()
	engine, stop := startWithLoops(t, 80, 24)
	defer stop()
	sm.SetTUI(engine)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			engine.Apply(func() { sm.Show("test") })
			engine.Apply(func() { sm.Clear() })
		}()
	}
	wg.Wait()
	// Drain the queue and read final state on the loop.
	engine.ApplySync(func() {
		_ = sm.Text()
		_ = sm.IsVisible()
	})
}

// Regression test: Show must work after Clear to support multi-turn sessions.
// After handleSessionEnd calls Clear(), subsequent turns call Show() to update
// the status. The done-channel guard in Show() must not permanently disable the
// spinner.
func TestStatusMsg_ShowAfterClear(t *testing.T) {
	defer resetSpinner()

	SetSpinner(spinner.Definition{
		Interval: 100,
		Frames:   []string{"⠋", "⠙", "⠹"},
	})
	defer resetSpinner()

	sm := NewStatusMsg()

	// First turn
	sm.Show("Thinking...")
	if sm.Text() != "Thinking..." {
		t.Fatalf("Show() failed to set text: got %q", sm.Text())
	}
	if !sm.IsVisible() {
		t.Fatal("Show() did not make status visible")
	}

	// End of turn
	sm.Clear()
	if sm.IsVisible() {
		t.Fatal("Clear() did not hide status")
	}

	// Second turn — must be able to show again
	sm.Show("Waiting...")
	if sm.Text() != "Waiting..." {
		t.Fatalf("Show() after Clear() failed to set text: got %q", sm.Text())
	}
	if !sm.IsVisible() {
		t.Fatal("Show() after Clear() did not make status visible")
	}

	lines := sm.Render(80)
	if len(lines) < 2 {
		t.Fatal("Render returned no lines for visible status after Clear+Show")
	}
	line := lines[1]
	stripped := ansi.Strip(line)
	if !strings.Contains(stripped, "Waiting...") {
		t.Errorf("status line missing new text after Clear+Show: %q", stripped)
	}
}

func TestStatusMsg_Render_NoSpinnerConfig(t *testing.T) {
	SetSpinner(spinner.Definition{})
	defer resetSpinner()

	sm := NewStatusMsg()
	sm.Show("idle")

	lines := sm.Render(80)
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines, got %d", len(lines))
	}
	line := lines[1]
	stripped := ansi.Strip(line)

	if !strings.Contains(stripped, "idle") {
		t.Errorf("status line missing text: %q", stripped)
	}

	if !strings.Contains(stripped, "◜") && !strings.Contains(stripped, "◆") {
		t.Errorf("status line missing spinner indicator: %q", stripped)
	}
}
