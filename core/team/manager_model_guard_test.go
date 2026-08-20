// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package team

import (
	"errors"
	"testing"
)

// TestManager_ModelPersistenceSuppressedWhileActive verifies team UI bug RC-5
// fix: while any team governs the session (base activation or goal overlay),
// the session controller's model-persistence guard is ON so the team's model
// can never be written back to home/project config as the user's choice;
// once no team governs anymore the guard is released.
func TestManager_ModelPersistenceSuppressedWhileActive(t *testing.T) {
	cfg := teamsTestConfig()
	m, fs, _, _, _ := newTestManager(cfg)

	if fs.persistenceSuppressed() {
		t.Fatal("persistence suppressed before any team activation")
	}

	if err := m.Activate("pair"); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if !fs.persistenceSuppressed() {
		t.Error("persistence not suppressed after Activate")
	}

	if err := m.ApplyOverlay("crew"); err != nil {
		t.Fatalf("ApplyOverlay: %v", err)
	}
	if !fs.persistenceSuppressed() {
		t.Error("persistence not suppressed after ApplyOverlay")
	}

	// Removing the overlay leaves the base team in charge: still suppressed.
	if err := m.RemoveOverlay(); err != nil {
		t.Fatalf("RemoveOverlay: %v", err)
	}
	if !fs.persistenceSuppressed() {
		t.Error("persistence released while base team still active")
	}

	if err := m.Deactivate(); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}
	if fs.persistenceSuppressed() {
		t.Error("persistence still suppressed after full Deactivate")
	}
}

// TestManager_ModelPersistenceOverlayOnly covers a goal overlay applied with
// no base team: suppression must engage for the overlay and release when it
// is removed.
func TestManager_ModelPersistenceOverlayOnly(t *testing.T) {
	cfg := teamsTestConfig()
	m, fs, _, _, _ := newTestManager(cfg)

	if err := m.ApplyOverlay("crew"); err != nil {
		t.Fatalf("ApplyOverlay: %v", err)
	}
	if !fs.persistenceSuppressed() {
		t.Error("persistence not suppressed under overlay-only team")
	}

	if err := m.RemoveOverlay(); err != nil {
		t.Fatalf("RemoveOverlay: %v", err)
	}
	if fs.persistenceSuppressed() {
		t.Error("persistence still suppressed after overlay removed (no base team)")
	}
}

// TestManager_ModelPersistenceRestoredOnApplyFailure ensures a failed team
// application does not leave persistence suppressed when nothing governs the
// session.
func TestManager_ModelPersistenceRestoredOnApplyFailure(t *testing.T) {
	cfg := teamsTestConfig()
	m, fs, _, _, _ := newTestManager(cfg)
	fs.switchErr = errors.New("boom")

	if err := m.Activate("pair"); err == nil {
		t.Fatal("Activate should fail with injected switch error")
	}
	if fs.persistenceSuppressed() {
		t.Error("persistence left suppressed after failed activation")
	}
}
