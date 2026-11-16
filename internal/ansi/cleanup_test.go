// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package ansi

import "testing"

func TestClean_StripEnabled(t *testing.T) {
	prev := StripANSIEnabled()
	SetStripANSI(true)
	t.Cleanup(func() { SetStripANSI(prev) })

	if got := Clean("\x1b[32m✓\x1b[39m done\x1b[2K\n"); got != "✓ done\n" {
		t.Errorf("Clean with stripping enabled = %q, want %q", got, "✓ done\n")
	}
	if got := Clean("literal \\e[2K stays"); got != "literal \\e[2K stays" {
		t.Errorf("Clean removed literal escape text: %q", got)
	}
	if got := Clean("a\x00b"); got != "a?b" {
		t.Errorf("Clean did not sanitize other controls: %q", got)
	}
}

func TestClean_StripDisabledKeepsVisibleEscapes(t *testing.T) {
	prev := StripANSIEnabled()
	SetStripANSI(false)
	t.Cleanup(func() { SetStripANSI(prev) })

	if got := Clean("\x1b[2Kwiped\n"); got != `\e[2Kwiped`+"\n" {
		t.Errorf("Clean with stripping disabled = %q, want visible escape", got)
	}
	if got := Clean("\x1b[31mred\x1b[0m"); got != `\e[31mred\e[0m` {
		t.Errorf("Clean with stripping disabled = %q, want visible SGR pair", got)
	}
}

func TestSetStripANSI(t *testing.T) {
	prev := StripANSIEnabled()
	t.Cleanup(func() { SetStripANSI(prev) })

	SetStripANSI(false)
	if StripANSIEnabled() {
		t.Error("StripANSIEnabled = true after SetStripANSI(false)")
	}
	SetStripANSI(true)
	if !StripANSIEnabled() {
		t.Error("StripANSIEnabled = false after SetStripANSI(true)")
	}
}
