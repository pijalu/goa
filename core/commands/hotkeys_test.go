// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core"
)

func TestHotkeysCommand_OutputContainsShortcuts(t *testing.T) {
	cmd := &HotkeysCommand{}
	ctx := core.Context{OutputBuffer: &strings.Builder{}}

	if err := cmd.Run(ctx, nil); err != nil {
		t.Fatal(err)
	}
	out := ctx.OutputBuffer.String()

	cases := []string{
		"shift+tab",    // cycle thinking level
		"alt+m",        // cycle major mode
		"alt+o",        // open mode selector
		"ctrl+shift+m", // cycle autonomy level
		"ctrl+l",       // model selector
		"ctrl+t",       // toggle thinking blocks
		"ctrl+g",       // goal bubble
		"enter",        // submit
		"shift+enter",  // newline
		"ctrl+c",       // cancel/quit
		"Cycle major mode",
		"Open the mode selector",
		"Cycle autonomy level",
		"Navigation",
		"Editing",
		"Application",
	}
	for _, want := range cases {
		if !strings.Contains(out, want) {
			t.Errorf("hotkeys output missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestHotkeysCommand_Registered(t *testing.T) {
	reg := core.NewCommandRegistry()
	if err := RegisterAll(reg); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Resolve("hotkeys"); !ok {
		t.Fatal("/hotkeys should be registered")
	}
}
