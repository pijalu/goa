// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

// Plane selects what the server ships to browsers (specs/webui.md §22).
//
//	PlaneCells  the v1 pipeline: the whole screen as cell rows (the browser
//	            is the terminal). The zero value, so every existing test,
//	            the no-JS page and `?mode=cells` keep their exact behaviour.
//	PlaneBlocks semantic conversation blocks plus the bottom chrome band as
//	            cells: the page renders HTML flow content, resize is browser
//	            reflow, and the transcript cells/scrollback are never shipped.
type Plane int

const (
	PlaneCells Plane = iota
	PlaneBlocks
)

// String names the plane (logs, tests).
func (p Plane) String() string {
	if p == PlaneBlocks {
		return "blocks"
	}
	return "cells"
}
