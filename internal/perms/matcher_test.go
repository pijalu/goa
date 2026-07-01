// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package perms

import "testing"

func TestMatch(t *testing.T) {
	cases := []struct {
		pattern string
		name    string
		want    bool
	}{
		{"bash", "bash", true},
		{"bash", "write", false},
		{"*", "bash", true},
		{"*", "mcp__fs__read", true},
		{"bash", "mcp__fs__read", false},
		{"mcp__*__read", "mcp__fs__read", true},
		{"mcp__*__read", "mcp__fs__write", false},
		{"mcp__*__*", "mcp__fs__read", true},
		{"mcp__**", "mcp__fs__read", true},
		{"mcp__**", "mcp", true},
		{"**__read", "mcp__fs__read", true},
		{"**__read", "bash", false},
		{"**", "anything_at_all", true},
		{"", "anything", false},
		{"mcp__fs__*", "mcp__fs__read_file", true},
		{"mcp__fs__*", "mcp__fs", false},
		{"read", "mcp__fs__read", false},
		{"mcp/fs/read", "mcp__fs__read", true},
	}

	for _, tc := range cases {
		got := Match(tc.pattern, tc.name)
		if got != tc.want {
			t.Errorf("Match(%q, %q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}

func TestMatchEmptyPattern(t *testing.T) {
	if Match("", "bash") {
		t.Error("empty pattern should not match")
	}
}
