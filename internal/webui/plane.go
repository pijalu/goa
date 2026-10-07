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

// ParsePlane resolves a client's requested plane name against a default. The
// empty name means "no preference" and yields def; an unknown name is treated
// the same way rather than refused — the plane is a rendering hint, and a
// client that cannot name one is served the server's default. This is what
// lets a native client ("plane=cells") and a browser (no preference) share
// one session.
func ParsePlane(name string, def Plane) Plane {
	switch name {
	case "cells":
		return PlaneCells
	case "blocks":
		return PlaneBlocks
	default:
		return def
	}
}
