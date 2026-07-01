// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package ansi

import "bytes"

// FindNextUnescaped finds the next occurrence of target that is NOT inside
// an ANSI escape sequence, starting from start. Returns -1 if not found.
//
// ANSI escape sequences begin with \x1b and terminate with a byte in the
// range 0x40–0x7E (typically a letter like 'm'). OSC sequences terminate
// with \x07 (BEL) or \x1b\\ (ST). This function skips over any escape
// sequence and only considers target bytes that appear in normal text.
func FindNextUnescaped(text, target string, start int) int {
	if start < 0 || start >= len(text) {
		return -1
	}
	buf := []byte(text)
	targetBytes := []byte(target)
	i := start

	for i < len(buf) {
		if buf[i] == 0x1b {
			i = skipANSISequence(buf, i)
			continue
		}
		if bytes.HasPrefix(buf[i:], targetBytes) {
			return i
		}
		i++
	}

	return -1
}

// skipANSISequence advances past a single escape sequence starting at i.
// Returns the index of the first byte after the sequence.
func skipANSISequence(buf []byte, i int) int {
	if i+1 >= len(buf) {
		return i + 1
	}
	next := buf[i+1]
	switch {
	case next == '[':
		return skipCSI(buf, i)
	case next == ']':
		return skipOSC(buf, i)
	case next == 'O' || next == 'o':
		return skipSS3(buf, i)
	case next >= 0x40 && next <= 0x7E:
		return i + 2
	default:
		return i + 1
	}
}

// skipCSI advances past a CSI sequence ESC [ ... final byte 0x40-0x7E.
func skipCSI(buf []byte, i int) int {
	i += 2
	for i < len(buf) && buf[i] < 0x40 {
		i++
	}
	if i < len(buf) {
		i++
	}
	return i
}

// skipOSC advances past an OSC sequence terminated by BEL or ST.
func skipOSC(buf []byte, i int) int {
	i += 2
	for i < len(buf) {
		if buf[i] == 0x07 {
			return i + 1
		}
		if buf[i] == 0x1b && i+1 < len(buf) && buf[i+1] == '\\' {
			return i + 2
		}
		i++
	}
	return i
}

// skipSS3 advances past an SS3 sequence ESC O <final byte>.
func skipSS3(buf []byte, i int) int {
	if i+2 < len(buf) && buf[i+2] >= 0x40 && buf[i+2] <= 0x7E {
		return i + 3
	}
	return i + 1
}
