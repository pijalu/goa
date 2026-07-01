// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"sync"
	"time"

	"github.com/pijalu/goa/internal/spinner"
)

// currentSpinner holds the active spinner definition.
var currentSpinner spinner.Definition
var spinnerMu sync.Mutex

// SetSpinner updates the active spinner definition used by all StatusMsg
// instances. A nil or empty definition uses a static diamond indicator.
// Called only during bootstrap (pre-loop), so the package-level guard simply
// documents single-writer intent.
func SetSpinner(def spinner.Definition) {
	spinnerMu.Lock()
	defer spinnerMu.Unlock()
	currentSpinner = def
}

// getSpinner returns the current spinner frames and interval.
func getSpinner() (frames []string, interval time.Duration) {
	spinnerMu.Lock()
	defer spinnerMu.Unlock()
	if len(currentSpinner.Frames) == 0 {
		_, def := spinner.Default()
		currentSpinner = def
	}
	return currentSpinner.Frames, time.Duration(currentSpinner.IntervalMS()) * time.Millisecond
}

// StatusMsg displays status updates (sending, thinking, tool calls) with an
// animated spinner with status container and loader.
//
// Concurrency: the commandLoop is the sole owner of StatusMsg state. Show,
// Clear, Render and the frame-advance all run on the loop; the animation
// goroutine only forwards each tick back to the loop via TUI.Apply (see
//). No mutex is required.
type StatusMsg struct {
	text     string
	spinning bool
	frameIdx int
	tui      *TUI
	ticker   *time.Ticker
	done     chan struct{}
}

// NewStatusMsg creates a StatusMsg component.
func NewStatusMsg() *StatusMsg { return &StatusMsg{} }

// SpinnerText returns the current spinner character.
func (s *StatusMsg) SpinnerText() string {
	if !s.spinning {
		return "◆"
	}
	frames, _ := getSpinner()
	if len(frames) == 0 {
		return "◆"
	}
	return frames[s.frameIdx%len(frames)]
}

// Text returns the current status text without the spinner prefix.
func (s *StatusMsg) Text() string { return s.text }

// SetTUI stores the TUI reference used to schedule frame advances on the loop.
func (s *StatusMsg) SetTUI(t *TUI) { s.tui = t }

// Show sets the status text and starts the spinner animation.
func (s *StatusMsg) Show(text string) {
	if s.text == text && s.spinning {
		return
	}
	s.text = text
	if !s.spinning {
		s.spinning = true
		s.frameIdx = 0
		done := make(chan struct{})
		_, interval := getSpinner()
		ticker := time.NewTicker(interval)
		s.done = done
		s.ticker = ticker
		go s.animate(done, ticker.C)
	}
}

// Clear hides the status and stops the spinner.
func (s *StatusMsg) Clear() {
	s.text = ""
	if s.spinning {
		s.spinning = false
		if s.ticker != nil {
			s.ticker.Stop()
			s.ticker = nil
		}
		close(s.done)
	}
}

// animate forwards each spinner tick to the commandLoop via Apply. It owns no
// state directly: tickFrame (the actual mutation) runs on the loop, serialized
// with Show/Clear/Render. It exits when done is closed by Clear.
func (s *StatusMsg) animate(done chan struct{}, tickerC <-chan time.Time) {
	for {
		select {
		case <-tickerC:
			if s.tui != nil {
				s.tui.Apply(s.tickFrame)
			}
		case <-done:
			return
		}
	}
}

// tickFrame advances the spinner frame. Runs on the commandLoop (sole owner),
// so it takes no lock.
func (s *StatusMsg) tickFrame() {
	if !s.spinning {
		return
	}
	frames, _ := getSpinner()
	n := len(frames)
	if n == 0 {
		n = 1
	}
	s.frameIdx = (s.frameIdx + 1) % n
}

// IsVisible returns whether status is shown.
func (s *StatusMsg) IsVisible() bool { return s.text != "" }

// Render returns the status line if visible, with 1col left/right padding
// and a leading/trailing blank line for visual separation.
func (s *StatusMsg) Render(width int) []string {
	txt := s.text
	if txt == "" || width <= 0 {
		return nil
	}
	frames, _ := getSpinner()
	var prefix string
	if s.spinning && len(frames) > 0 {
		prefix = frames[s.frameIdx%len(frames)]
	} else {
		prefix = "◆"
	}
	padded := padToWidth(dimText(prefix+" "+txt), width-1)
	line := " " + padded
	return []string{"", line, ""}
}

// HandleInput is a no-op.
func (s *StatusMsg) HandleInput(data string) {}

// Invalidate is a no-op.
func (s *StatusMsg) Invalidate() {}
