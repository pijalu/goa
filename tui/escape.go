// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

// Shared escape sequence parsing utilities.
// Consolidates logic that was duplicated across stdin_buffer.go, terminal.go,
// and keys.go. Both StdinBuffer and scanProtoBuf use these functions to split
// raw bytes into complete terminal sequences.

// nextSequence extracts the next complete escape sequence from buf.
// Returns the sequence string and byte count consumed, or ("", 0) if incomplete.
func nextSequence(buf []byte) (string, int) {
	if len(buf) == 0 || buf[0] != 0x1b {
		return "", 0
	}
	if len(buf) < 2 {
		return "", 0
	}
	next := buf[1]

	switch {
	case next == '[':
		return nextCSI(buf[2:])
	case next == 'O':
		return nextSS3(buf[2:])
	case next == ']':
		return nextOSC(buf[2:])
	case next == '_':
		return nextAPC(buf[2:])
	case next == 'P':
		return nextDCS(buf[2:])
	case next >= 0x20:
		// ESC + printable — Alt+key
		return string(buf[:2]), 2
	default:
		// Bare ESC
		return string(buf[:1]), 1
	}
}

// nextCSI extracts a CSI sequence: ESC [ ... final byte.
func nextCSI(params []byte) (string, int) {
	// CSI sequences start at ESC[ (2 bytes), params data is in params[]
	// We need to return the full sequence length including ESC[ prefix.
	for j := 0; j < len(params); j++ {
		b := params[j]
		if b == ';' {
			// Look ahead for final byte after semicolon
			return nextCSIAfter(params, j)
		}
		if isFinalByte(b) {
			// Potential final byte; check for CSI-u (final byte followed by ';')
			if j+1 < len(params) && params[j+1] == ';' {
				continue
			}
			return string(append([]byte{0x1b, '['}, params[:j+1]...)), j + 3
		}
	}
	return "", 0
}

// nextCSIAfter finds the final byte after a semicolon position in a CSI sequence.
func nextCSIAfter(params []byte, semiPos int) (string, int) {
	for k := semiPos + 1; k < len(params); k++ {
		if isFinalByte(params[k]) {
			return string(append([]byte{0x1b, '['}, params[:k+1]...)), k + 3
		}
	}
	return "", 0
}

// nextSS3 extracts an SS3 sequence: ESC O X (exactly 3 bytes).
func nextSS3(params []byte) (string, int) {
	if len(params) >= 1 {
		return string([]byte{0x1b, 'O', params[0]}), 3
	}
	return "", 0
}

// nextOSC extracts an OSC sequence: ESC ] ... BEL (0x07) or ST (ESC \).
func nextOSC(params []byte) (string, int) {
	for j := 0; j < len(params); j++ {
		if params[j] == 0x07 {
			return string(append([]byte{0x1b, ']'}, params[:j+1]...)), j + 3
		}
		if params[j] == 0x1b && j+1 < len(params) && params[j+1] == '\\' {
			return string(append([]byte{0x1b, ']'}, params[:j+2]...)), j + 4
		}
	}
	return "", 0
}

// nextAPC extracts an APC sequence: ESC _ ... BEL or ST.
func nextAPC(params []byte) (string, int) {
	for j := 0; j < len(params); j++ {
		if params[j] == 0x07 {
			return string(append([]byte{0x1b, '_'}, params[:j+1]...)), j + 3
		}
		if params[j] == 0x1b && j+1 < len(params) && params[j+1] == '\\' {
			return string(append([]byte{0x1b, '_'}, params[:j+2]...)), j + 4
		}
	}
	return "", 0
}

// nextDCS extracts a DCS sequence: ESC P ... ST (ESC \).
func nextDCS(params []byte) (string, int) {
	for j := 0; j < len(params); j++ {
		if params[j] == 0x1b && j+1 < len(params) && params[j+1] == '\\' {
			return string(append([]byte{0x1b, 'P'}, params[:j+2]...)), j + 4
		}
	}
	return "", 0
}

// isFinalByte reports whether b is a CSI/SS3 final byte.
func isFinalByte(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || b == '~'
}
