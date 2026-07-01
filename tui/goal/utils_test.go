// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

func TestPadToWidth(t *testing.T) {
	if got := padToWidth("hi", 5); len(ansi.Strip(got)) != 5 {
		t.Errorf("len = %d", len(ansi.Strip(got)))
	}
	if got := padToWidth("hello", 3); len(ansi.Strip(got)) != 3 {
		t.Errorf("len = %d", len(ansi.Strip(got)))
	}
	if got := padToWidth("x", 0); got != "x" {
		t.Errorf("got = %q", got)
	}
}

func TestPadToWidth_WithANSI(t *testing.T) {
	s := ansi.Faint + "hi" + ansi.Reset
	got := padToWidth(s, 5)
	if len(ansi.Strip(got)) != 5 {
		t.Errorf("len = %d", len(ansi.Strip(got)))
	}
	if !strings.Contains(got, ansi.Faint) {
		t.Error("lost ansi")
	}
}
