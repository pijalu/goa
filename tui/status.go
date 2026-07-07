// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"log"
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

var currentSpinnerFrame string
var spinnerFrameMu sync.Mutex

func updateCurrentSpinnerFrame(frames []string, frameIdx int) {
	spinnerFrameMu.Lock()
	defer spinnerFrameMu.Unlock()
	if len(frames) == 0 {
		currentSpinnerFrame = ""
	} else {
		currentSpinnerFrame = frames[frameIdx%len(frames)]
	}
}

// CurrentSpinnerFrame returns the most recent spinner frame rendered by the
// active StatusMsg animation. Other components (tool widgets, footer) can use
// this to share the same animated frame without running their own goroutines.
func CurrentSpinnerFrame() string {
	spinnerFrameMu.Lock()
	defer spinnerFrameMu.Unlock()
	return currentSpinnerFrame
}

// getSpinner returns the current spinner frames and interval.
func getSpinner() (frames []string, interval time.Duration) {
	spinnerMu.Lock()
	defer spinnerMu.Unlock()
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
	text          string
	spinning      bool
	frameIdx      int
	tui           *TUI
	ticker        *time.Ticker
	done          chan struct{}
	sessionEnded  bool
	onFrameChange func()
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

// SetOnFrameChange registers a callback that is invoked every time the
// animated spinner frame advances. Callers (e.g. the chat viewport) can use
// it to invalidate dependent components that display the same frame.
func (s *StatusMsg) SetOnFrameChange(fn func()) { s.onFrameChange = fn }

// Show sets the status text and starts the spinner animation.
// If the session has ended, Show() is a no-op so late events cannot restart
// the spinner after the turn is finished. In-turn Clear() calls do NOT block
// Show(), so the spinner can survive across sequential tool calls.
func (s *StatusMsg) Show(text string) {
	if s.sessionEnded {
		log.Printf("[StatusMsg] Show(%q) ignored: sessionEnded=true", text)
		return
	}
	if s.text == text && s.spinning {
		return
	}
	oldText := s.text
	s.text = text
	log.Printf("[StatusMsg] Show(%q) oldText=%q spinning=%v", text, oldText, s.spinning)
	frames, _ := getSpinner()
	updateCurrentSpinnerFrame(frames, s.frameIdx)
	if s.onFrameChange != nil {
		s.onFrameChange()
	}
	if !s.spinning {
		s.spinning = true
		s.frameIdx = 0
		// Only launch the animation goroutine when the commandLoop is running.
		// In the single-goroutine test mode (loops not running), TUI.Apply runs
		// the callback inline, so a background animation would race with the test
		// goroutine. The status text and initial frame are still visible.
		if s.tui != nil && s.tui.LoopsRunning() {
			done := make(chan struct{})
			_, interval := getSpinner()
			ticker := time.NewTicker(interval)
			s.done = done
			s.ticker = ticker
			go s.animate(done, ticker.C)
		}
	}
}

// Clear hides the status and stops the spinner. Normal in-turn Clear() calls
// do not block future Show() calls; only SessionEnd() arms the guard that
// prevents late events from re-starting the spinner.
func (s *StatusMsg) Clear() {
	log.Printf("[StatusMsg] Clear() oldText=%q spinning=%v", s.text, s.spinning)
	s.text = ""
	if s.spinning {
		s.spinning = false
		if s.ticker != nil {
			s.ticker.Stop()
			s.ticker = nil
		}
		if s.done != nil {
			close(s.done)
			s.done = nil
		}
	}
	spinnerFrameMu.Lock()
	currentSpinnerFrame = ""
	spinnerFrameMu.Unlock()
}

// SessionEnd marks the session as finished and clears the status. After
// SessionEnd(), Show() is a no-op until Reset() is called.
func (s *StatusMsg) SessionEnd() {
	log.Printf("[StatusMsg] SessionEnd()")
	s.Clear()
	s.sessionEnded = true
}

// Reset clears the post-session guard so that the next Show() can start a
// fresh spinner. It must be called when a new turn begins after a session end.
func (s *StatusMsg) Reset() {
	if s.spinning {
		s.sessionEnded = false
		return
	}
	s.sessionEnded = false
	if s.done != nil {
		select {
		case <-s.done:
		default:
		}
		s.done = nil
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
	updateCurrentSpinnerFrame(frames, s.frameIdx)
	if s.onFrameChange != nil {
		s.onFrameChange()
	}
}

// IsVisible returns whether status is shown.
func (s *StatusMsg) IsVisible() bool { return s.text != "" }

// Render returns a single stable-height line: the spinner text when active,
// or a blank reserved line when idle. It always returns exactly one line
// (when width > 0) so toggling Show/Clear never changes the layer height —
// this prevents the input editor and footer from jumping up and down across
// turn boundaries (Bug 2). The previous ["", line, ""] padding toggled 3↔0
// rows on every turn start/end.
func (s *StatusMsg) Render(width int) []string {
	if width <= 0 {
		return nil
	}
	txt := s.text
	if txt == "" {
		// Idle: reserve a single blank line so the layer height is stable.
		return []string{""}
	}
	frames, _ := getSpinner()
	var prefix string
	if s.spinning && len(frames) > 0 {
		prefix = frames[s.frameIdx%len(frames)]
	} else {
		prefix = "◆"
	}
	return []string{" " + padToWidth(dimText(prefix+" "+txt), width-1)}
}

// HandleInput is a no-op.
func (s *StatusMsg) HandleInput(data string) {}

// Invalidate is a no-op.
func (s *StatusMsg) Invalidate() {}
