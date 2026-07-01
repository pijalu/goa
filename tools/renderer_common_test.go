// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

func TestRendererHelpers_UsePartialResets(t *testing.T) {
	// These helpers are used inside background-colored tool blocks. They must
	// use partial resets (foreground/intensity only) so the outer background
	// color is preserved across styled fragments.
	cases := []struct {
		name   string
		render func() string
	}{
		{"rToolTitle", func() string { return rToolTitle("title") }},
		{"rToolOutput", func() string { return rToolOutput("output") }},
		{"rMuted", func() string { return rMuted("muted") }},
		{"rWarning", func() string { return rWarning("warning") }},
		{"rError", func() string { return rError("error") }},
		{"rAccent", func() string { return rAccent("accent") }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.render()
			if strings.Contains(got, ansi.Reset) {
				t.Errorf("%s contains a full ANSI reset, which would kill an outer background color: %q", tc.name, got)
			}
		})
	}
}

func TestHighlightBash_UsePartialResets(t *testing.T) {
	// Comments use faint and commands use foreground color. Both must reset
	// with partial codes to preserve an outer background color.
	comment := highlightBash("echo hi # comment")
	if strings.Contains(comment, ansi.Reset) {
		t.Errorf("highlighted bash comment contains a full ANSI reset: %q", comment)
	}

	cmd := highlightBash("ls -la")
	if strings.Contains(cmd, ansi.Reset) {
		t.Errorf("highlighted bash command contains a full ANSI reset: %q", cmd)
	}
}
