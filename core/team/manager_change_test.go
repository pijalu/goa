// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package team

import (
	"testing"
)

// TestManager_ChangeCallback verifies team UI bug RC-4 fix: every
// visibility-relevant transition (activate / overlay / overlay-remove /
// deactivate / sync) fires the change callback with the NEW effective team
// and a reason, so the app can announce the team and refresh the footer —
// no team may ever be hidden from the user.
func TestManager_ChangeCallback(t *testing.T) {
	cfg := teamsTestConfig()
	m, _, _, _, _ := newTestManager(cfg)

	type transition struct {
		effective string
		reason    string
	}
	var got []transition
	m.SetChangeCallback(func(effective, reason string) {
		got = append(got, transition{effective, reason})
	})

	if err := m.Activate("pair"); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if err := m.ApplyOverlay("crew"); err != nil {
		t.Fatalf("ApplyOverlay: %v", err)
	}
	if err := m.RemoveOverlay(); err != nil {
		t.Fatalf("RemoveOverlay: %v", err)
	}
	if err := m.Deactivate(); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}
	// Deactivate with nothing active: no notification (no-op, no event).
	if err := m.Deactivate(); err != nil {
		t.Fatalf("Deactivate noop: %v", err)
	}

	want := []transition{
		{"pair", "activated"},
		{"crew", "overlay"},
		{"pair", "overlay removed"}, // session team governs again
		{"", "deactivated"},
	}
	if len(got) != len(want) {
		t.Fatalf("transitions = %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("transition %d = %v, want %v (all: %v)", i, got[i], w, got)
		}
	}
}

// TestManager_ChangeCallbackNilSafe ensures transitions without an installed
// callback do not panic.
func TestManager_ChangeCallbackNilSafe(t *testing.T) {
	cfg := teamsTestConfig()
	m, _, _, _, _ := newTestManager(cfg)
	if err := m.Activate("pair"); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if err := m.Deactivate(); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}
}

// TestManager_ChangeCallbackOnSync verifies Sync reports the re-applied team.
func TestManager_ChangeCallbackOnSync(t *testing.T) {
	cfg := teamsTestConfig()
	m, _, _, _, _ := newTestManager(cfg)
	var reasons []string
	m.SetChangeCallback(func(_, reason string) { reasons = append(reasons, reason) })
	if err := m.Activate("pair"); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if err := m.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(reasons) != 2 || reasons[0] != "activated" || reasons[1] != "synced" {
		t.Errorf("reasons = %v, want [activated synced]", reasons)
	}
}
