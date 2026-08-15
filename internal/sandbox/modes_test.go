// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package sandbox

import (
	"reflect"
	"testing"
)

// TestModeRank verifies the confinement strictness ordering.
func TestModeRank(t *testing.T) {
	cases := []struct {
		mode Mode
		want int
	}{
		{ModeReadOnly, 1},
		{ModeWorkspaceWrite, 2},
		{ModeDangerFullAccess, 3},
		{Mode(""), 0},
		{Mode("unknown"), 0},
	}
	for _, tc := range cases {
		if got := tc.mode.Rank(); got != tc.want {
			t.Errorf("%q Rank() = %d, want %d", tc.mode, got, tc.want)
		}
	}
}

func TestMode_IsValid(t *testing.T) {
	if !ModeReadOnly.IsValid() || !ModeWorkspaceWrite.IsValid() || !ModeDangerFullAccess.IsValid() {
		t.Error("all vocabulary modes must be valid")
	}
	if Mode("").IsValid() || Mode("nope").IsValid() {
		t.Error("unknown modes must be invalid")
	}
}

// TestMode_StrictlyWider verifies the strict widening relation: the exact
// condition behind "non-widening requests fail without prompting".
func TestMode_StrictlyWider(t *testing.T) {
	cases := []struct {
		name  string
		m     Mode
		other Mode
		want  bool
	}{
		{name: "danger over workspace", m: ModeDangerFullAccess, other: ModeWorkspaceWrite, want: true},
		{name: "danger over read-only", m: ModeDangerFullAccess, other: ModeReadOnly, want: true},
		{name: "workspace over read-only", m: ModeWorkspaceWrite, other: ModeReadOnly, want: true},
		{name: "equal workspace", m: ModeWorkspaceWrite, other: ModeWorkspaceWrite, want: false},
		{name: "equal danger", m: ModeDangerFullAccess, other: ModeDangerFullAccess, want: false},
		{name: "workspace over danger", m: ModeWorkspaceWrite, other: ModeDangerFullAccess, want: false},
		{name: "read-only over workspace", m: ModeReadOnly, other: ModeWorkspaceWrite, want: false},
		{name: "unknown never wider", m: Mode("x"), other: ModeWorkspaceWrite, want: false},
		{name: "against unknown never wider", m: ModeWorkspaceWrite, other: Mode("x"), want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.m.StrictlyWider(tc.other); got != tc.want {
				t.Errorf("%q.StrictlyWider(%q) = %v, want %v", tc.m, tc.other, got, tc.want)
			}
		})
	}
}

// TestEscalationVocabulary verifies the closed target vocabulary is stable and
// never cut down to the executor's default.
func TestEscalationVocabulary(t *testing.T) {
	want := []string{string(ModeWorkspaceWrite), string(ModeDangerFullAccess)}
	if !reflect.DeepEqual(EscalationVocabulary, want) {
		t.Errorf("EscalationVocabulary = %v, want %v", EscalationVocabulary, want)
	}
}
