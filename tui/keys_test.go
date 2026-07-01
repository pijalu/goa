// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"
)

func TestDecodeKeys_DA_ResponseDoesNotLeak(t *testing.T) {
	data := []byte{0x1b, '[', '?', '6', '2', ';', '2', '2', ';', '5', '2', 'c'}
	keys := decodeKeys(data)
	for _, key := range keys {
		if strings.Contains(key, "escape") || strings.Contains(key, "62") {
			t.Errorf("DA response leaked visible key: %q", key)
		}
	}
}

func TestDecodeKeys_Slash_NoDuplication(t *testing.T) {
	// Kitty CSI-u format for '/' (code 47, no modifier): \x1b[47;1u
	data := []byte{0x1b, '[', '4', '7', ';', '1', 'u'}
	keys := decodeKeys(data)
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d: %v", len(keys), keys)
	}
	if keys[0] != "/" {
		t.Errorf("expected '/', got %q", keys[0])
	}
}

func TestDecodeKeys_Slash_Standard_NoDuplication(t *testing.T) {
	// Standard '/' byte
	data := []byte{'/'}
	keys := decodeKeys(data)
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d: %v", len(keys), keys)
	}
	if keys[0] != "/" {
		t.Errorf("expected '/', got %q", keys[0])
	}
}

func TestDecodeKeys_A_NoDuplication(t *testing.T) {
	// Kitty CSI-u format for 'a' (code 97, no modifier): \x1b[97;1u
	data := []byte{0x1b, '[', '9', '7', ';', '1', 'u'}
	keys := decodeKeys(data)
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d: %v", len(keys), keys)
	}
	if keys[0] != "a" {
		t.Errorf("expected 'a', got %q", keys[0])
	}
}

func TestDecodeKeys_MultipleKeys_NoDuplication(t *testing.T) {
	// Simulate typing 'ab' with Kitty protocol
	// \x1b[97;1u (a) followed by \x1b[98;1u (b)
	data := []byte{
		0x1b, '[', '9', '7', ';', '1', 'u',
		0x1b, '[', '9', '8', ';', '1', 'u',
	}
	keys := decodeKeys(data)
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d: %v", len(keys), keys)
	}
	if keys[0] != "a" || keys[1] != "b" {
		t.Errorf("expected ['a','b'], got %v", keys)
	}
}

func TestDecodeKeys_CtrlEnter(t *testing.T) {
	// Kitty CSI-u format for Ctrl+Enter (code 13, modifier 5): \x1b[13;5u
	data := []byte{0x1b, '[', '1', '3', ';', '5', 'u'}
	keys := decodeKeys(data)
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d: %v", len(keys), keys)
	}
	if keys[0] != "ctrl+enter" {
		t.Errorf("expected 'ctrl+enter', got %q", keys[0])
	}
}

func TestDecodeKeys_Space_Standard(t *testing.T) {
	// Standard space byte
	data := []byte{' '}
	keys := decodeKeys(data)
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d: %v", len(keys), keys)
	}
	if keys[0] != " " {
		t.Errorf("expected ' ', got %q", keys[0])
	}
}

func TestDecodeKeys_Space_Kitty(t *testing.T) {
	// Kitty CSI-u format for space (code 32, no modifier): \x1b[32;1u
	data := []byte{0x1b, '[', '3', '2', ';', '1', 'u'}
	keys := decodeKeys(data)
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d: %v", len(keys), keys)
	}
	if keys[0] != " " {
		t.Errorf("expected ' ', got %q", keys[0])
	}
}

func TestDecodeKeys_MixedStandardAndKitty(t *testing.T) {
	// Standard 'a' followed by Kitty CSI-u 'b'
	data := []byte{'a', 0x1b, '[', '9', '8', ';', '1', 'u'}
	keys := decodeKeys(data)
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d: %v", len(keys), keys)
	}
	if keys[0] != "a" || keys[1] != "b" {
		t.Errorf("expected ['a','b'], got %v", keys)
	}
}

func TestDecodeKeys_KittyArrow_NoDuplication(t *testing.T) {
	// Kitty CSI-u format for Down arrow with shift: \x1b[1;2B
	data := []byte{0x1b, '[', '1', ';', '2', 'B'}
	keys := decodeKeys(data)
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d: %v", len(keys), keys)
	}
	if keys[0] != "shift+down" {
		t.Errorf("expected 'shift+down', got %q", keys[0])
	}
}

func TestDecodeKeys_PlainText(t *testing.T) {
	keys := decodeKeys([]byte("hello"))
	got := strings.Join(keys, "")
	if got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}
}

func TestDecodeKeys_ArrowUp(t *testing.T) {
	data := []byte{0x1b, '[', 'A'}
	keys := decodeKeys(data)
	if len(keys) != 1 || keys[0] != KeyUp {
		t.Errorf("arrow up should produce %q, got %v", KeyUp, keys)
	}
}

func TestDecodeKeys_MultipleEvents(t *testing.T) {
	data := []byte{'h', 0x1b, '[', 'A', 'i'}
	keys := decodeKeys(data)
	if len(keys) != 3 {
		t.Fatalf("expected 3 keys, got %d: %v", len(keys), keys)
	}
	if keys[0] != "h" || keys[1] != KeyUp || keys[2] != "i" {
		t.Errorf("expected ['h','up','i'], got %v", keys)
	}
}

func TestDecodeKeys_EscapeOnly(t *testing.T) {
	data := []byte{0x1b}
	keys := decodeKeys(data)
	if len(keys) != 1 || keys[0] != KeyEscape {
		t.Errorf("bare ESC should produce %q, got %v", KeyEscape, keys)
	}
}

func TestDecodeKeys_KittyArrowUp(t *testing.T) {
	// Kitty CSI-u format for Up arrow: \x1b[A;1u (letter A, mod 1)
	data := []byte{0x1b, '[', 'A', ';', '1', 'u'}
	keys := decodeKeys(data)
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d: %v", len(keys), keys)
	}
	if keys[0] != KeyUp {
		t.Errorf("expected 'up', got %q", keys[0])
	}
}

func TestDecodeKeys_KittyArrowDown(t *testing.T) {
	// Kitty CSI-u format for Down arrow: \x1b[B;1u
	data := []byte{0x1b, '[', 'B', ';', '1', 'u'}
	keys := decodeKeys(data)
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d: %v", len(keys), keys)
	}
	if keys[0] != KeyDown {
		t.Errorf("expected 'down', got %q", keys[0])
	}
}

func TestDecodeKeys_KittyArrowLeft(t *testing.T) {
	// Kitty CSI-u format for Left arrow: \x1b[D;1u
	data := []byte{0x1b, '[', 'D', ';', '1', 'u'}
	keys := decodeKeys(data)
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d: %v", len(keys), keys)
	}
	if keys[0] != KeyLeft {
		t.Errorf("expected 'left', got %q", keys[0])
	}
}

func TestDecodeKeys_KittyArrowRight(t *testing.T) {
	// Kitty CSI-u format for Right arrow: \x1b[C;1u
	data := []byte{0x1b, '[', 'C', ';', '1', 'u'}
	keys := decodeKeys(data)
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d: %v", len(keys), keys)
	}
	if keys[0] != KeyRight {
		t.Errorf("expected 'right', got %q", keys[0])
	}
}

func TestDecodeKeys_KittyCtrlArrowUp(t *testing.T) {
	// Kitty CSI-u format for Ctrl+Up: \x1b[A;5u
	data := []byte{0x1b, '[', 'A', ';', '5', 'u'}
	keys := decodeKeys(data)
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d: %v", len(keys), keys)
	}
	if keys[0] != "ctrl+up" {
		t.Errorf("expected 'ctrl+up', got %q", keys[0])
	}
}

func TestDecodeKeys_KittyPageUp(t *testing.T) {
	// Kitty CSI-u format for PageUp: \x1b[5~;1u
	data := []byte{0x1b, '[', '5', '~', ';', '1', 'u'}
	keys := decodeKeys(data)
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d: %v", len(keys), keys)
	}
	if keys[0] != KeyPageUp {
		t.Errorf("expected 'pageUp', got %q", keys[0])
	}
}

func TestDecodeKeys_KittyPageDown(t *testing.T) {
	// Kitty CSI-u format for PageDown: \x1b[6~;1u
	data := []byte{0x1b, '[', '6', '~', ';', '1', 'u'}
	keys := decodeKeys(data)
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d: %v", len(keys), keys)
	}
	if keys[0] != KeyPageDown {
		t.Errorf("expected 'pageDown', got %q", keys[0])
	}
}

func TestDecodeKeys_KittyDelete(t *testing.T) {
	// Kitty CSI-u format for Delete: \x1b[3~;1u
	data := []byte{0x1b, '[', '3', '~', ';', '1', 'u'}
	keys := decodeKeys(data)
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d: %v", len(keys), keys)
	}
	if keys[0] != KeyDelete {
		t.Errorf("expected 'delete', got %q", keys[0])
	}
}

func TestDecodeKeys_KittyHome(t *testing.T) {
	// Kitty CSI-u format for Home: \x1b[H;1u (letter H, mod 1)
	data := []byte{0x1b, '[', 'H', ';', '1', 'u'}
	keys := decodeKeys(data)
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d: %v", len(keys), keys)
	}
	if keys[0] != KeyHome {
		t.Errorf("expected 'home', got %q", keys[0])
	}
}

func TestDecodeKeys_KittyEnd(t *testing.T) {
	// Kitty CSI-u format for End: \x1b[F;1u (letter F, mod 1)
	data := []byte{0x1b, '[', 'F', ';', '1', 'u'}
	keys := decodeKeys(data)
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d: %v", len(keys), keys)
	}
	if keys[0] != KeyEnd {
		t.Errorf("expected 'end', got %q", keys[0])
	}
}

func TestDecodeKeys_KittyBackspace(t *testing.T) {
	// Kitty CSI-u format for Backspace (code 127): \x1b[127;1u
	data := []byte{0x1b, '[', '1', '2', '7', ';', '1', 'u'}
	keys := decodeKeys(data)
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d: %v", len(keys), keys)
	}
	if keys[0] != KeyBackspace {
		t.Errorf("expected 'backspace', got %q", keys[0])
	}
}

func TestDecodeKeys_KittyEscape(t *testing.T) {
	// Kitty CSI-u format for Escape (code 27): \x1b[27;1u
	data := []byte{0x1b, '[', '2', '7', ';', '1', 'u'}
	keys := decodeKeys(data)
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d: %v", len(keys), keys)
	}
	if keys[0] != KeyEscape {
		t.Errorf("expected 'escape', got %q", keys[0])
	}
}

func TestDecodeKeys_KittyF1(t *testing.T) {
	// Kitty CSI-u format for F1: \x1b[OP;1u
	data := []byte{0x1b, '[', 'O', 'P', ';', '1', 'u'}
	keys := decodeKeys(data)
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d: %v", len(keys), keys)
	}
	if keys[0] != "f1" {
		t.Errorf("expected 'f1', got %q", keys[0])
	}
}

func TestDecodeKeys_KittyShiftTab(t *testing.T) {
	// Kitty CSI-u format for Shift+Tab (code 9, mod 2): \x1b[9;2u
	data := []byte{0x1b, '[', '9', ';', '2', 'u'}
	keys := decodeKeys(data)
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d: %v", len(keys), keys)
	}
	if keys[0] != KeyShiftTab {
		t.Errorf("expected 'shift+tab', got %q", keys[0])
	}
}

func TestAltKeyName(t *testing.T) {
	cases := []struct {
		char string
		want string
	}{
		{"µ", "alt+m"},
		{"¬", "alt+l"},
		{"†", "alt+t"},
		{"a", ""},
		{"", ""},
	}
	for _, tc := range cases {
		got := altKeyName(tc.char)
		if got != tc.want {
			t.Errorf("altKeyName(%q) = %q, want %q", tc.char, got, tc.want)
		}
	}
}

func TestDecodeKeyForRouting_OptionKeyAlias(t *testing.T) {
	// macOS Option+m emits the UTF-8 sequence for µ.
	key := decodeKeyForRouting("µ")
	if key != "µ" {
		t.Errorf("decodeKeyForRouting(\"µ\") = %q, want %q", key, "µ")
	}
}
