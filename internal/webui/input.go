// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"strconv"
	"strings"
)

// The browser is the keyboard (spec §7.5). The page does not encode anything:
// it ships a normalized description of the physical key event and KeyEncoder —
// the only input logic in the web UI — turns it into the byte sequence a real
// terminal would have written into its pty. The engine therefore decodes the
// very same bytes it decodes in a terminal, which is why text insertion,
// multiline, kill-ring, undo, word motion, history and autocomplete behave
// identically.
//
// Encoding rules, in order:
//
//  1. unmodified special keys use the legacy xterm sequences (the §7.5 table);
//  2. modifier chords on those keys use the legacy parameterized form
//     ("\x1b[1;5A", "\x1b[3;5~"), which tui/keys.go already decodes into
//     "ctrl+up", "ctrl+delete", …;
//  3. keys with no legacy parameterized form (Ctrl/Alt + Enter, Tab, Escape)
//     use the Kitty CSI-u form ("\x1b[13;5u"), which tui/keys.go also decodes;
//  4. Ctrl + a letter is the control byte itself (0x01…0x1a); Ctrl + a
//     punctuation, or Ctrl with Shift/Alt also held, is CSI-u, so "ctrl+-",
//     "ctrl+shift+m" and "ctrl+alt+]" survive as distinct keys.
//
// Every sequence emitted here is covered by a decoding test
// (TestKeyEncoder_RoundTripsThroughTUIDecoder), so the browser and the engine
// cannot drift apart.

// KeyEvent is one browser keydown, normalized for the wire. The browser cannot
// send "the bytes a terminal would send" without reimplementing this table in
// JavaScript; it sends the fields below instead and the server — which owns the
// tested encoder — does the encoding.
type KeyEvent struct {
	// Key is KeyboardEvent.key: a printable character, or a name such as
	// "ArrowUp", "Enter", "F5".
	Key string `json:"key"`
	// Code is KeyboardEvent.code ("KeyA", "ArrowUp", "Minus"). It only
	// disambiguates layouts where key is not a single character.
	Code string `json:"code,omitempty"`
	// Ctrl/Alt/Shift are the modifier flags.
	Ctrl  bool `json:"ctrl,omitempty"`
	Alt   bool `json:"alt,omitempty"`
	Shift bool `json:"shift,omitempty"`
	// Meta is the Command/Windows key. It is never encoded: those chords are
	// browser-level shortcuts and must keep working in the page.
	Meta bool `json:"meta,omitempty"`
	// Repeat marks an auto-repeating key. It is informational — the terminal
	// path has no notion of it either, so repeats encode identically.
	Repeat bool `json:"repeat,omitempty"`
}

// specialKey is one row of the chord table: the byte sequence a terminal writes
// for a key press, plus the forms used when modifiers are held.
type specialKey struct {
	// bytes is the unmodified sequence.
	bytes string
	// csi is the parameterized form "params final" used when a modifier is
	// held ("1A" for arrows, "3~" for Delete, "15~" for F5).
	csi string
	// code is the Kitty CSI-u key code, used for the keys the legacy table
	// cannot parameterize (Enter, Tab, Escape).
	code int
}

// specialKeys is the full chord table: spec §7.5 extended to every key
// tui/keybindings.go binds (F1–F12, Insert, modified Backspace, Home/End).
var specialKeys = map[string]specialKey{
	"Enter":      {bytes: "\r", code: 13},
	"Tab":        {bytes: "\t", code: 9},
	"Escape":     {bytes: "\x1b", code: 27},
	"Backspace":  {bytes: "\x7f", code: 127},
	"Delete":     {bytes: "\x1b[3~", csi: "3~"},
	"Insert":     {bytes: "\x1b[2~", csi: "2~"},
	"PageUp":     {bytes: "\x1b[5~", csi: "5~"},
	"PageDown":   {bytes: "\x1b[6~", csi: "6~"},
	"ArrowUp":    {bytes: "\x1b[A", csi: "1A"},
	"ArrowDown":  {bytes: "\x1b[B", csi: "1B"},
	"ArrowRight": {bytes: "\x1b[C", csi: "1C"},
	"ArrowLeft":  {bytes: "\x1b[D", csi: "1D"},
	"Home":       {bytes: "\x1b[H", csi: "1H"},
	"End":        {bytes: "\x1b[F", csi: "1F"},
	"F1":         {bytes: "\x1bOP", csi: "1P"},
	"F2":         {bytes: "\x1bOQ", csi: "1Q"},
	"F3":         {bytes: "\x1bOR", csi: "1R"},
	"F4":         {bytes: "\x1bOS", csi: "1S"},
	"F5":         {bytes: "\x1b[15~", csi: "15~"},
	"F6":         {bytes: "\x1b[17~", csi: "17~"},
	"F7":         {bytes: "\x1b[18~", csi: "18~"},
	"F8":         {bytes: "\x1b[19~", csi: "19~"},
	"F9":         {bytes: "\x1b[20~", csi: "20~"},
	"F10":        {bytes: "\x1b[21~", csi: "21~"},
	"F11":        {bytes: "\x1b[23~", csi: "23~"},
	"F12":        {bytes: "\x1b[24~", csi: "24~"},
}

// shiftTab is the one chord with no modifier parameter: Shift+Tab is its own
// final byte in both the legacy table and tui/keys.go (KeyShiftTab).
const shiftTab = "\x1b[Z"

// modifierValue is the xterm modifier parameter: 1 + shift + 2·alt + 4·ctrl.
// tui/keys.go maps 2…8 back to "shift+", "alt+", "ctrl+…" prefixes.
func modifierValue(ev KeyEvent) int {
	return 1 + boolBit(ev.Shift) + 2*boolBit(ev.Alt) + 4*boolBit(ev.Ctrl)
}

func boolBit(b bool) int {
	if b {
		return 1
	}
	return 0
}

// EncodeKey maps a browser key event to the bytes a terminal would send.
// It returns nil for keys that must keep their browser default (modifier keys
// themselves, dead keys, Meta chords, F13+); the caller uses an empty result to
// decide not to preventDefault.
func EncodeKey(ev KeyEvent) []byte {
	// The Command/Windows key is the browser's own chord space: swallowing it
	// would break reload, devtools and tab switching.
	if ev.Meta || ev.Key == "" {
		return nil
	}
	if s, ok := specialKeys[ev.Key]; ok {
		return encodeSpecial(ev, s)
	}
	r, ok := printableKey(ev)
	if !ok {
		return nil
	}
	return encodePrintable(ev, r)
}

// encodeSpecial applies the special-key rules: legacy bytes, then the legacy
// parameterized form, then CSI-u for the keys the legacy table cannot
// parameterize.
func encodeSpecial(ev KeyEvent, s specialKey) []byte {
	mod := modifierValue(ev)
	if mod == 1 {
		return []byte(s.bytes)
	}
	// Shift+Tab is a distinct final byte, not a modifier value.
	if ev.Key == "Tab" && ev.Shift && !ev.Alt && !ev.Ctrl {
		return []byte(shiftTab)
	}
	if s.csi == "" {
		return csiu(s.code, mod)
	}
	// "\x1b[1;5A" / "\x1b[3;5~": the parameter goes between the row and the
	// final byte.
	head := s.csi[:len(s.csi)-1]
	return []byte("\x1b[" + head + ";" + strconv.Itoa(mod) + s.csi[len(s.csi)-1:])
}

// csiu renders a Kitty CSI-u key report: ESC [ code ; modifier u.
func csiu(code, mod int) []byte {
	if code <= 0 {
		return nil
	}
	return []byte("\x1b[" + strconv.Itoa(code) + ";" + strconv.Itoa(mod) + "u")
}

// printableKey extracts the single-rune character a key press inserts. It
// rejects named keys ("Enter", "ArrowUp", "Shift") and resolves the few layouts
// where key is not a single character.
func printableKey(ev KeyEvent) (rune, bool) {
	if isRuneKey(ev.Key) {
		r := []rune(ev.Key)
		return r[0], true
	}
	return codeRune(ev.Code)
}

// isRuneKey reports whether s is a single character (printable input) rather
// than a key name.
func isRuneKey(s string) bool {
	return len([]rune(s)) == 1
}

// codeRune maps a KeyboardEvent.code to the character it produces on a US
// layout. It is only consulted when key is not a single character (a dead or
// composed key), so a non-US layout keeps whatever the browser reports.
func codeRune(code string) (rune, bool) {
	suffix := strings.TrimPrefix(strings.TrimPrefix(code, "Key"), "Digit")
	if suffix == code || len(suffix) != 1 {
		return 0, false
	}
	c := suffix[0]
	switch {
	case c >= 'A' && c <= 'Z':
		return rune(c - 'A' + 'a'), true
	case c >= '0' && c <= '9':
		return rune(c), true
	}
	return 0, false
}

// encodePrintable applies the printable rules: Ctrl+letter is the control
// byte, every other Ctrl chord is CSI-u, Alt prefixes ESC, and a bare
// character is inserted as-is.
func encodePrintable(ev KeyEvent, r rune) []byte {
	if ev.Ctrl {
		return encodeCtrlPrintable(ev, r)
	}
	if ev.Alt {
		return encodeAltPrintable(ev, r)
	}
	return []byte(string(r))
}

// encodeCtrlPrintable maps a Ctrl chord on a printable key. Ctrl+letter is the
// control byte when it is the *whole* chord; with Shift or Alt also held the
// byte would silently drop the extra modifier (Ctrl+Shift+M must not arrive as
// Ctrl+M = Enter), so those chords keep their full identity through CSI-u — as
// do punctuation chords, which have no legacy encoding at all ("ctrl+-",
// "ctrl+alt+]", "ctrl+shift+m" are engine bindings).
func encodeCtrlPrintable(ev KeyEvent, r rune) []byte {
	if !ev.Alt && !ev.Shift {
		if b, ok := ctrlLetter(r); ok {
			return []byte{b}
		}
	}
	if r == ' ' {
		return []byte{0x00}
	}
	return csiu(int(baseCodepoint(r)), modifierValue(ev))
}

// encodeAltPrintable prefixes ESC, the way a terminal delivers an Alt chord.
// The two CSI introducers would instead open a truncated escape sequence, so
// they take the CSI-u route ("alt+[" is a bound key).
func encodeAltPrintable(ev KeyEvent, r rune) []byte {
	if r == '[' || r == 'O' {
		return csiu(int(r), modifierValue(ev))
	}
	return append([]byte{0x1b}, []byte(string(r))...)
}

// baseCodepoint is the code point of the physical key, before Shift: terminals
// report the unshifted letter for a shifted letter key ("ctrl+shift+m" is the
// engine's binding, and tui/keys.go decodes code 109 to "m" but 77 to "M").
func baseCodepoint(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r - 'A' + 'a'
	}
	return r
}

// ctrlLetter maps a letter to its C0 control byte (Ctrl+A = 0x01 … Ctrl+Z =
// 0x1a). Uppercase letters arrive only with Shift held, which is handled as a
// CSI-u chord, but the mapping is total so the table cannot surprise anyone.
func ctrlLetter(r rune) (byte, bool) {
	switch {
	case r >= 'a' && r <= 'z':
		return byte(r - 'a' + 1), true
	case r >= 'A' && r <= 'Z':
		return byte(r - 'A' + 1), true
	}
	return 0, false
}
