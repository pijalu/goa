// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"testing"

	"github.com/pijalu/goa/core"
)

func TestUICommand_CompleteArgs(t *testing.T) {
	cmd := &UICommand{}

	cases := []struct {
		prefix string
		want   []string
	}{
		{"", []string{"theme", "pane", "flash"}},
		{"t", []string{"theme"}},
		{"theme", []string{"theme"}},
		{"theme:", []string{"set"}},
		{"theme:s", []string{"set"}},
		{"pane:", []string{"show", "hide"}},
		{"pane:h", []string{"hide"}},
		{"flash:", nil}, // flash takes free text; no further completion
		{"theme:set:x:", nil},
	}
	for _, c := range cases {
		got := cmd.CompleteArgs(core.Context{}, c.prefix)
		gotVals := completionsToValues(got)
		if !sameSet(gotVals, c.want) {
			t.Errorf("CompleteArgs(%q) = %v, want %v", c.prefix, gotVals, c.want)
		}
	}
}

func completionsToValues(cs []core.ArgCompletion) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Value)
	}
	return out
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int)
	for _, v := range a {
		seen[v]++
	}
	for _, v := range b {
		seen[v]--
		if seen[v] < 0 {
			return false
		}
	}
	return true
}
