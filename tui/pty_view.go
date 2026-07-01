// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/ansi"
)

// PTYView displays a PTY session's output in a scrollable overlay.
//
// Concurrency: the commandLoop is the sole owner of PTYView state. The polling
// goroutine forwards each tick back to the loop via TUI.Apply (Refresh runs on
// the loop); Render/HandleInput also run on the loop. No mutex is required
//
type PTYView struct {
	Container

	sessionID  string
	mgr        *internal.PTYManager
	tui        *TUI
	output     []string
	width      int
	height     int
	autoScroll bool
	lastPoll   time.Time
	pollTicker *time.Ticker
	stopPoll   chan struct{}
}

// NewPTYView creates a PTY viewer for the given session.
func NewPTYView(mgr *internal.PTYManager, sessionID string) *PTYView {
	pv := &PTYView{
		sessionID:  sessionID,
		mgr:        mgr,
		autoScroll: true,
		lastPoll:   time.Now(),
		stopPoll:   make(chan struct{}),
	}
	pv.startPolling()
	return pv
}

// SetTUI stores the TUI reference used to schedule polls on the loop.
func (pv *PTYView) SetTUI(t *TUI) { pv.tui = t }

// startPolling begins polling the PTY for new output.
func (pv *PTYView) startPolling() {
	pv.pollTicker = time.NewTicker(200 * time.Millisecond)
	go func() {
		for {
			select {
			case <-pv.pollTicker.C:
				if pv.tui != nil {
					pv.tui.Apply(pv.Refresh)
				}
			case <-pv.stopPoll:
				pv.pollTicker.Stop()
				return
			}
		}
	}()
}

// Stop stops polling.
func (pv *PTYView) Stop() {
	select {
	case <-pv.stopPoll:
	default:
		close(pv.stopPoll)
	}
}

// Refresh pulls latest output from the PTY manager. Runs on the commandLoop
// (sole owner), so it takes no lock.
func (pv *PTYView) Refresh() {
	if pv.mgr == nil {
		return
	}
	output, err := pv.mgr.Read(pv.sessionID, 0)
	if err != nil {
		return
	}
	lines := strings.Split(output, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	pv.output = lines
}

// Render displays the PTY output as a scrollable overlay.
func (pv *PTYView) Render(width int) []string {
	if width <= 0 {
		return nil
	}
	pv.width = width
	height := pv.height
	output := pv.output

	sessionID := pv.sessionID
	dim := ansi.Fg(TheTheme.ColorHex("system_msg"))
	title := fmt.Sprintf(" PTY Session: %s ", sessionID)
	border := ansi.Fg(TheTheme.ColorHex("border_focused"))
	sep := strings.Repeat("─", width-len(title)-2)

	var lines []string
	// Top border with title
	lines = append(lines, padToWidth(border+"┌─"+title+sep+"─┐"+ansi.Reset, width))

	// PTY output
	if len(output) > 0 {
		// Show last N lines that fit in height
		start := 0
		if len(output) > height-4 {
			start = len(output) - (height - 4)
		}
		for _, line := range output[start:] {
			clean := strings.TrimRight(line, "\r\n")
			lines = append(lines, padToWidth(dim+"│ "+clean+ansi.Reset, width))
		}
	} else {
		lines = append(lines, padToWidth(dim+"│ (waiting for output)"+ansi.Reset, width))
	}

	// Bottom border with controls
	status := "Ctrl+C to close"
	lines = append(lines, padToWidth(border+"├─ "+status+ansi.Reset, width))
	lines = append(lines, padToWidth(border+"└"+strings.Repeat("─", width-2)+"┘"+ansi.Reset, width))

	return lines
}

// HandleInput processes keys for the PTY view.
func (pv *PTYView) HandleInput(data string) {
	if matchesKey(data, "ctrl+c") || matchesKey(data, KeyEscape) {
		pv.Stop()
	}
}

// Invalidate clears cached rendering state.
func (pv *PTYView) Invalidate() {
	pv.Container.Invalidate()
}

// SetPTYViewPort sets the viewport dimensions for the PTY viewer.
func (pv *PTYView) SetPTYViewPort(width, height int) {
	pv.width = width
	pv.height = height
}
