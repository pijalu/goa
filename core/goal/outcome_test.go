// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"testing"
)

func TestPluralize(t *testing.T) {
	if got := Pluralize(1, "turn", "turns"); got != "1 turn" {
		t.Errorf("Pluralize(1) = %q", got)
	}
	if got := Pluralize(3, "turn", "turns"); got != "3 turns" {
		t.Errorf("Pluralize(3) = %q", got)
	}
}

func TestBuildCancellationReminder(t *testing.T) {
	if got := BuildCancellationReminder(); got == "" {
		t.Error("cancellation reminder empty")
	}
}

func TestBuildForkClearedReminder(t *testing.T) {
	if got := BuildForkClearedReminder(); got == "" {
		t.Error("fork cleared reminder empty")
	}
}
