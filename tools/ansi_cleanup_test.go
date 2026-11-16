// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

// setStripANSIForTest pins the process-wide ANSI cleanup gate for a test and
// restores it afterwards. Tool tests must be explicit: the production default
// is on, while legacy sanitization assertions need the off path pinned too.
func setStripANSIForTest(t *testing.T, enabled bool) {
	t.Helper()
	prev := ansi.StripANSIEnabled()
	ansi.SetStripANSI(enabled)
	t.Cleanup(func() { ansi.SetStripANSI(prev) })
}
