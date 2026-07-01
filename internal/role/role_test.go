// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package role

import "testing"

func TestValidRoles(t *testing.T) {
	for _, r := range []string{Main, Companion, Planner, Coder, Reviewer} {
		if !IsValid(r) {
			t.Errorf("IsValid(%q) = false, want true", r)
		}
	}
	if IsValid("main_agent") {
		t.Error("IsValid(\"main_agent\") should be false")
	}
}

func TestValidRoles_Unknown(t *testing.T) {
	if IsValid("compaion") {
		t.Error("typo role should not be valid")
	}
}
