// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"github.com/pijalu/goa/core"
)

// writeFmt writes a formatted string to the command's output buffer.
// It accepts the narrowest possible interface (OutputWriter) so helpers
// that only emit text do not depend on the full Context.
func writeFmt(w core.OutputWriter, format string, args ...interface{}) {
	w.Writef(format, args...)
}

// writeStr writes a literal string to the command's output buffer.
func writeStr(w core.OutputWriter, s string) {
	w.Writef("%s", s)
}

// writeErr writes an error string to the command's output buffer.
func writeErr(w core.OutputWriter, format string, args ...interface{}) {
	w.Writef(format, args...)
}
