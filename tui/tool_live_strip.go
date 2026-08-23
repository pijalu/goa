// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"time"

	"github.com/pijalu/goa/internal/ansi"
)

// LiveStatusLine renders this widget's CURRENT live status as one plain line:
// header identity + freshly computed elapsed + progress stats. Unlike the
// widget's own duration row — which is only rebuilt when the widget renders —
// this is computed at call time, so a pinned strip can keep ticking for a
// running tool whose rows are already committed to terminal scrollback and
// can never be repainted (bugs.md: tool execution scrolled out of view stops
// updating).
func (tc *ToolExecutionComponent) LiveStatusLine() string {
	parts := []string{ansi.Strip(tc.box.header)}
	if tc.status != ToolRunning && tc.status != ToolPending {
		return strings.Join(parts, " ")
	}
	if tc.startTime.IsZero() {
		return strings.Join(parts, " ")
	}
	if elapsed := time.Since(tc.startTime); elapsed > 10*time.Millisecond {
		parts = append(parts, "elapsed "+formatDuration(elapsed)+tc.progressSuffix())
	}
	return strings.Join(parts, " ")
}

// OffscreenRunningTool returns the oldest tool widget that is still running
// while fully committed to terminal scrollback — its status can never be
// painted again, which is exactly what the pinned live strip exists to
// surface. nil when every running tool is inside the repaintable window.
// Runs on the command loop (Render of the strip, which the layout renders
// after the viewport in the same pass).
func (cv *ChatViewport) OffscreenRunningTool() *ToolExecutionComponent {
	for i := range cv.entries {
		tc, ok := cv.entries[i].View.(*ToolExecutionComponent)
		if !ok || tc.Status() != ToolRunning {
			continue
		}
		if cv.IsScrolledOff(tc) {
			return tc
		}
	}
	return nil
}

// ToolLiveStrip is a one-row pinned chrome line that mirrors the live status
// of a running tool whose widget scrolled into terminal scrollback. It
// renders ZERO rows while no such tool exists (chrome height unchanged), and
// one row — recomputed each frame with a fresh elapsed — while one does, so
// the user always sees current progress without scrolling up. At completion
// the strip disappears and the boundary scrollback resync rewrites the
// widget's historical rows with the true final duration.
type ToolLiveStrip struct {
	Container
	viewport *ChatViewport
}

// NewToolLiveStrip builds the strip bound to the conversation viewport.
func NewToolLiveStrip(cv *ChatViewport) *ToolLiveStrip {
	return &ToolLiveStrip{viewport: cv}
}

// Render returns the strip's rows: nil (chrome unchanged) when no running
// tool is off-screen, otherwise the single live status line styled with the
// tool's running background so it reads as the widget's own status row.
func (s *ToolLiveStrip) Render(width int) []string {
	tc := s.viewport.OffscreenRunningTool()
	if tc == nil || width <= 0 {
		return nil
	}
	return []string{padToWidthStyled(" "+tc.LiveStatusLine(), width, tc.bgANSI())}
}

// HandleInput is a no-op: the strip is informational only.
func (s *ToolLiveStrip) HandleInput(string) {}
