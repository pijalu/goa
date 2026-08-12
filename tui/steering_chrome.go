// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

// SteeringChrome renders the pending steering queue as a pinned bottom-chrome
// bubble, in the same band as the status bar, goal bubble, input editor, and
// footer. Unlike the previous design — which kept the bubble as a
// ConsoleSteeringPending transcript entry that ChatViewport.Append removed and
// re-appended on every new arrival to keep it bottom-most — this component is
// NOT part of the scrollable transcript at all.
//
// Why: the compositor's exactly-once scrollback invariant requires the
// transcript to be append-only. The remove/re-append shuffle invalidated the
// shared render cache and reset the moved entry's lineOffset on every stream
// chunk, desyncing the scrollback watermark so already-scrolled rows (the
// bubble's "(alt+e to edit)" box and trailing "└───┘" borders) were
// re-emitted — the duplicated/garbled frames seen when a slash command such as
// /quota landed mid-stream ("/quota request during streaming corrupts
// the TUI"). As chrome, the bubble never enters the transcript canvas: the
// watermark advances monotonically and nothing is re-emitted or lost.
//
// Lifecycle: the bubble is shown while steering is queued (Add), merged when
// more steering arrives (Add again), and hidden when the steering is consumed
// (the app then appends the text to the transcript as a user message) or
// restored to the input line (Clear).
//
// Concurrency: the commandLoop is the sole owner of SteeringChrome state;
// every method runs on the commandLoop. No mutex is required.
type SteeringChrome struct {
	inner *steeringPending
}

// NewSteeringChrome creates a steering chrome component with no pending
// steering (renders zero rows until the first Add).
func NewSteeringChrome() *SteeringChrome { return &SteeringChrome{} }

// Add appends a steering message to the pending bubble, creating it if absent.
// When a bubble is already present the new message is merged in: the bubble
// displays the queue with a "(N messages)" stat so the user sees every queued
// steering message, not just the last one.
func (s *SteeringChrome) Add(text string) {
	if s.inner == nil {
		s.inner = newSteeringPending(text)
		return
	}
	s.inner.SetMessages(append(s.inner.Messages(), text))
}

// Clear removes the pending steering bubble, if any. The component then
// renders zero rows, freeing the chrome band.
func (s *SteeringChrome) Clear() { s.inner = nil }

// HasPending reports whether a pending steering bubble is present.
func (s *SteeringChrome) HasPending() bool { return s.inner != nil }

// HandleInput is a no-op: the bubble is display-only.
func (s *SteeringChrome) HandleInput(string) {}

// Invalidate is a no-op: the bubble re-renders from its message queue.
func (s *SteeringChrome) Invalidate() {}

// Render implements Component. It returns nil (zero chrome rows) when no
// steering is pending, so the band costs nothing in the common case.
func (s *SteeringChrome) Render(width int) []string {
	if s.inner == nil {
		return nil
	}
	return s.inner.Render(width)
}
