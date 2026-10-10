// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import "testing"

// decodeOneKey decodes a single sequence and returns the one key it stands for.
func decodeOneKey(t *testing.T, seq string) string {
	t.Helper()
	keys := decodeKeys([]byte(seq))
	if len(keys) == 0 {
		return ""
	}
	if len(keys) > 1 {
		t.Fatalf("%q decoded to %d keys (%v), want one", seq, len(keys), keys)
	}
	return keys[0]
}

// TestDecodeKeys_KittyModifiersAreNamed pins the whole Kitty modifier bitmask.
//
// The reported bug: a terminal that speaks the Kitty keyboard protocol reports
// Cmd+V as ESC [ 118 ; 9 u (code point "v", modifier 9 = 1 + super). The decoder
// knew modifier values 2..8 only, so super fell into a default that returned an
// empty prefix and the *bare* "v" — which the editor then typed into the input
// line instead of pasting.
func TestDecodeKeys_KittyModifiersAreNamed(t *testing.T) {
	cases := []struct {
		seq  string
		want string
		why  string
	}{
		{"\x1b[118;1u", "v", "no modifier: a plain letter stays text"},
		{"\x1b[118;5u", KeyCtrlV, "ctrl+v (1+ctrl)"},
		{"\x1b[118;9u", KeySuperV, "Cmd+V (1+super) — the reported chord"},
		{"\x1b[86;6u", "ctrl+shift+v", "shifted code point normalised to the base letter"},
		{"\x1b[86;2u", "shift+V", "shift only: the shifted code point is kept"},
		{"\x1b[77;6u", "ctrl+shift+m", "app-level ctrl+shift chords stay reachable"},
		{"\x1b[118;3u", "alt+v", "1+alt"},
		{"\x1b[118;7u", "ctrl+alt+v", "1+ctrl+alt"},
		{"\x1b[118;15u", "ctrl+alt+super+v", "named in binding order, super last"},
		{"\x1b[118;17u", "hyper+v", "1+hyper"},
		{"\x1b[118;33u", "meta+v", "1+meta"},
		{"\x1b[118;73u", "super+v", "caps lock reported alongside: Cmd+V with Caps Lock on"},
		{"\x1b[118;201u", "super+v", "caps + num lock reported alongside"},
		{"\x1b[118:86;9u", "super+v", "alternate key reported: the base key names the chord"},
		{"\x1b[13;9u", "super+enter", "named key with a modifier"},
		{"\x1b[3~;9u", "super+delete", "tilde-style named key with a modifier"},
		{"\x1b[A;9u", "super+up", "cursor key with a modifier"},
		{"\x1b[OP;9u", "super+f1", "SS3-style function key with a modifier"},
	}
	for _, tc := range cases {
		if got := decodeOneKey(t, tc.seq); got != tc.want {
			t.Errorf("%q decoded to %q, want %q (%s)", tc.seq, got, tc.want, tc.why)
		}
	}
}

// TestDecodeKeys_UnnameableModifierIsDropped: a modifier bit the decoder has no
// name for must drop the key, never emit the bare key. The bare key is printable
// text, so emitting it is indistinguishable from typing that character — the
// mechanism behind "Cmd+V inserts a v".
func TestDecodeKeys_UnnameableModifierIsDropped(t *testing.T) {
	for _, seq := range []string{
		"\x1b[118;257u", // a bit above the defined mask
		"\x1b[118;9999u",
		"\x1b[1;257A", // standard CSI cursor key: the old table leaked the bare "up"
		"\x1b[A;257u",
	} {
		if got := decodeOneKey(t, seq); got != "" {
			t.Errorf("%q decoded to %q, want nothing: an unnamed modifier must not leak the bare key", seq, got)
		}
	}
}

// TestDecodeKeys_BareKeyStillText guards the other direction: with no modifier at
// all the decoded name must remain the character itself, so typing a letter keeps
// working (and so a shifted letter the terminal reports as text is untouched).
func TestDecodeKeys_BareKeyStillText(t *testing.T) {
	for _, tc := range []struct{ seq, want string }{
		{"\x1b[118u", "v"},
		{"\x1b[86u", "V"},
		{"v", "v"},
		{"+", "+"},
	} {
		if got := decodeOneKey(t, tc.seq); got != tc.want {
			t.Errorf("%q decoded to %q, want %q", tc.seq, got, tc.want)
		}
	}
}

// TestIsChordName pins the vocabulary that separates a decoded chord name from
// text a component must insert. Only modifier-prefixed names are chords: an
// unmodified name spells a word a user may legitimately paste ("insert",
// "delete"), so it must stay insertable.
func TestIsChordName(t *testing.T) {
	chords := []string{
		KeySuperV, "super+q", KeyCtrlV, "ctrl+shift+v", "alt+up", "hyper+v",
		"ctrl+backspace", "shift+enter", KeyShiftTab, "meta+a",
	}
	for _, name := range chords {
		if !isChordName(name) {
			t.Errorf("isChordName(%q) = false, want true", name)
		}
	}
	notChords := []string{
		// Unmodified key names double as ordinary words.
		KeyEnter, KeyBackspace, KeyTab, KeyEscape, KeyDelete, "insert",
		KeyUp, KeyLeft, KeyHome, KeyEnd, KeyPageUp, "f1", "f5", "f12",
		// Text that merely looks like a chord, or that contains "+".
		"v", "V", "+", "-", "hello", "hello world", "super", "cmd+v", "Opt+V",
		"ctrl", "a+b", "f13", "µ", "…", "\x1b[118;9u",
	}
	for _, text := range notChords {
		if isChordName(text) {
			t.Errorf("isChordName(%q) = true, want false: that is text, not a chord", text)
		}
	}
}
