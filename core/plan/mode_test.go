// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plan

import "testing"

func TestPlanState(t *testing.T) {
	s := NewState()
	if s.IsActive() {
		t.Error("new state should be inactive")
	}
	s.Enable()
	if !s.IsActive() {
		t.Error("expected active")
	}
	if !s.IsAllowedPath("PLAN.md") {
		t.Error("PLAN.md should be allowed")
	}
	if s.IsAllowedPath("main.go") {
		t.Error("main.go should be blocked in plan mode")
	}
	s.Disable()
	if !s.IsAllowedPath("main.go") {
		t.Error("main.go should be allowed when inactive")
	}
}
