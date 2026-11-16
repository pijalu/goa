// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package ansi

import "sync/atomic"

// stripANSI controls whether Clean removes escape sequences from untrusted
// text instead of escaping them into visible `\e...` text. ANSI stripping is
// enabled by default: goa output must not be polluted by escape sequences
// emitted by child processes (bugs.md: ANSI escapes leak into goa output).
// The zero value of atomic.Bool is false, so init sets the default explicitly.
var stripANSI atomic.Bool

func init() { stripANSI.Store(true) }

// SetStripANSI enables or disables ANSI-sequence removal in Clean. It is
// process-global because untrusted output is cleaned in many tools that do
// not carry config. Called from config load/reload and /config:set.
func SetStripANSI(enabled bool) { stripANSI.Store(enabled) }

// StripANSIEnabled reports the active Clean mode.
func StripANSIEnabled() bool { return stripANSI.Load() }

// Clean makes untrusted text (file contents, command output) safe to show in
// a terminal. With stripping enabled (the default), ANSI escape sequences are
// removed entirely; with it disabled, Sanitize's visible `\e...` rendering is
// preserved. Other control characters are always sanitized.
func Clean(s string) string {
	if StripANSIEnabled() {
		s = Strip(s)
	}
	return Sanitize(s)
}
