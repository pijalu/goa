// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"testing"

	"github.com/pijalu/goa/core/orchestrator"
)

// TestOrchestratorTabPicker_HidesCursor is the RED regression for the bug
// reported in bugs.md: when the switch-tab overlay is open, the hardware cursor
// should not be visible at the underlying editor position (the user saw it
// inside the switch tab list). A capturing overlay without a cursor should hide
// the base editor's cursor.
func TestOrchestratorTabPicker_HidesCursor(t *testing.T) {
	sc := newOrchViewScenario(t, 100, 30)
	sc.app.attachOrchView(newFakeOrchSource())
	sc.flush()
	sc.applyOrchEvents([]orchestrator.Event{
		{Type: orchestrator.EventRunStarted, Payload: map[string]any{"objective": "x", "topology": "hub"}},
	})

	sc.engine.ApplySync(func() { sc.app.subs.getInput().SetText("some typed text before opening picker") })
	sc.engine.ApplySync(func() { sc.app.openAgentTabSelector() })
	frame := sc.frame()

	if frame.Cursor != nil {
		t.Errorf("cursor should be hidden when a capturing overlay is open, got (%d,%d)", frame.Cursor.Row, frame.Cursor.Col)
	}
}
