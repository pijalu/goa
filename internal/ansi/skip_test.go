// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package ansi

import "testing"

func TestFindNextUnescaped_Basic(t *testing.T) {
	n := FindNextUnescaped("hello [world]", "[", 0)
	if n != 6 {
		t.Errorf("expected 6, got %d", n)
	}
}

func TestFindNextUnescaped_NotFound(t *testing.T) {
	n := FindNextUnescaped("hello world", "[", 0)
	if n != -1 {
		t.Errorf("expected -1, got %d", n)
	}
}

func TestFindNextUnescaped_SkipsANSIBracket(t *testing.T) {
	// \x1b[1m contains '[' inside the ANSI sequence — should NOT match
	text := "\x1b[1m[real](url)\x1b[0m"
	n := FindNextUnescaped(text, "[", 0)
	// Should skip the \x1b[1m sequence and find the real '[' after it
	if n < 0 {
		t.Fatal("expected to find '[' after ANSI sequence")
	}
	if text[n] != '[' {
		t.Errorf("expected '[', got %c at position %d", text[n], n)
	}
	// \\x1b[1m = 4 bytes, real '[' at position 4
	if n != 4 {
		t.Errorf("expected '[' at position 4, got %d", n)
	}
}

func TestFindNextUnescaped_SkipsMultipleANSI(t *testing.T) {
	text := "\x1b[1m\x1b[31m[text]"
	n := FindNextUnescaped(text, "[", 0)
	if n < 0 {
		t.Fatal("expected to find '[' after multiple ANSI sequences")
	}
	// \\x1b[1m = 4 bytes, \\x1b[31m = 5 bytes, total = 9
	if n != 9 {
		t.Errorf("expected '[' at position 9, got %d", n)
	}
}

func TestFindNextUnescaped_SkipsOSC(t *testing.T) {
	// OSC sequence terminated by BEL
	text := "\x1b]0;title\x07[real]"
	n := FindNextUnescaped(text, "[", 0)
	if n < 0 {
		t.Fatal("expected to find '[' after OSC")
	}
	// \\x1b]0;title\x07 = \\x1b (1) + ] (1) + 0;title (7) + \\x07 (1) = 10 bytes
	if n != 10 {
		t.Errorf("expected '[' at position 10, got %d", n)
	}
}

func TestFindNextUnescaped_StartOffset(t *testing.T) {
	text := "abc\x1b[1mdef[ghi]"
	// Starting at 0 should skip the ANSI sequence
	n := FindNextUnescaped(text, "[", 0)
	// abc(3) + \\x1b[1m(4) + def(3) = 10, '[' at 10
	if n != 10 {
		t.Errorf("expected 10, got %d", n)
	}
	// Starting at 11 should find nothing (already past the '[' at 10)
	n2 := FindNextUnescaped(text, "[", 11)
	if n2 != -1 {
		t.Errorf("expected -1 from offset 11, got %d", n2)
	}
}

func TestFindNextUnescaped_EmptyTarget(t *testing.T) {
	n := FindNextUnescaped("text", "", 0)
	if n != 0 {
		t.Errorf("expected 0 for empty target, got %d", n)
	}
}

func TestFindNextUnescaped_StartOutOfBounds(t *testing.T) {
	n := FindNextUnescaped("text", "t", 10)
	if n != -1 {
		t.Errorf("expected -1 for out-of-bounds start, got %d", n)
	}
	n2 := FindNextUnescaped("text", "t", -1)
	if n2 != -1 {
		t.Errorf("expected -1 for negative start, got %d", n2)
	}
}

func TestFindNextUnescaped_SS3Sequence(t *testing.T) {
	// SS3: ESC O P (F1 key)
	text := "\x1bOP[real]"
	n := FindNextUnescaped(text, "[", 0)
	if n < 0 {
		t.Fatal("expected to find '[' after SS3")
	}
	// \x1bOP = 3 bytes, '[' at 3
	if n != 3 {
		t.Errorf("expected 3, got %d", n)
	}
}

func TestFindNextUnescaped_TwoCharEscape(t *testing.T) {
	// Two-character escape: ESC 7 (DECSC)
	text := "\x1b7[real]"
	n := FindNextUnescaped(text, "[", 0)
	if n < 0 {
		t.Fatal("expected to find '[' after two-char escape")
	}
	// \x1b7 = 2 bytes, '[' at 2
	if n != 2 {
		t.Errorf("expected 2, got %d", n)
	}
}

func TestFindNextUnescaped_BoldThenLink(t *testing.T) {
	// Real-world case: bold ANSI followed by a markdown link
	text := "\x1b[1m[text](url)\x1b[22m"
	n := FindNextUnescaped(text, "[", 0)
	if n < 0 {
		t.Fatal("expected to find '[' in link after bold")
	}
	// \x1b[1m = 4 bytes, '[' at 4
	if n != 4 {
		t.Errorf("expected '[' at position 4 for link start, got %d", n)
	}
}
