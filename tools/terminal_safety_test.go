// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"sort"
	"testing"
)

func TestFindBlockedCommands(t *testing.T) {
	cases := []struct {
		cmd  string
		want []string
	}{
		{"rm -rf /", []string{"rm"}},
		{"echo curl", nil},
		{"bash -c 'sudo id'", []string{"sudo"}},
		{"xargs rm", []string{"rm"}},
		{"find . -exec rm {} ;", []string{"rm"}},
		{"echo done; rm -rf x", []string{"rm"}},
		{"grep -r rm .", nil},
	}
	for _, tc := range cases {
		got := findBlockedCommands(tc.cmd, nil)
		sort.Strings(got)
		if !sliceEqual(got, tc.want) {
			t.Errorf("findBlockedCommands(%q) = %v, want %v", tc.cmd, got, tc.want)
		}
	}
}

func TestFindBlockedCommandsExtra(t *testing.T) {
	got := findBlockedCommands("custom-tool", []string{"custom-tool"})
	if len(got) != 1 || got[0] != "custom-tool" {
		t.Fatalf("expected custom-tool blocked, got %v", got)
	}
}

func sliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
