// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"bytes"
	"strings"
)

// StdinBuffer splits raw terminal input into individual sequences.
type StdinBuffer struct {
	buf      []byte
	events   []string
	inPaste  bool
	pasteBuf strings.Builder
}

// NewStdinBuffer creates a new StdinBuffer.
func NewStdinBuffer() *StdinBuffer {
	return &StdinBuffer{}
}

// Process accepts raw bytes and returns decoded key events.
func (sb *StdinBuffer) Process(data []byte) []string {
	sb.buf = append(sb.buf, data...)
	sb.events = nil
	sb.processBuffer()
	events := sb.events
	sb.events = nil
	return events
}

func (sb *StdinBuffer) processBuffer() {
	pasteStart := []byte{0x1b, '[', '2', '0', '0', '~'}
	pasteEnd := []byte{0x1b, '[', '2', '0', '1', '~'}

	for len(sb.buf) > 0 {
		// Check for bracketed paste markers
		if sb.handlePaste(pasteStart, pasteEnd) {
			continue
		}

		// Normal mode — use shared escape sequence parser
		seq, n := nextSequence(sb.buf)
		if n > 0 {
			sb.appendEvent(seq)
			sb.buf = sb.buf[n:]
			continue
		}

		if sb.handleEscapeByte() {
			continue
		}
		if len(sb.buf) == 0 || sb.buf[0] == 0x1b {
			return
		}
		sb.handlePlainByte()
	}
}

func (sb *StdinBuffer) handleEscapeByte() bool {
	if sb.buf[0] != 0x1b {
		return false
	}
	// If the buffer contains only a bare 0x1b, it's a standalone Escape
	// key press, not the start of a longer sequence. Emit it immediately.
	if len(sb.buf) == 1 {
		sb.appendEvent(string(sb.buf[:1]))
		sb.buf = sb.buf[1:]
		return true
	}
	// If there's another \x1b later in the buffer, the current byte is a
	// standalone Escape key, not part of a stale CSI prefix. Emit it and
	// continue processing the rest.
	if idx := bytes.IndexByte(sb.buf[1:], 0x1b); idx >= 0 {
		sb.appendEvent(string(sb.buf[:1]))
		sb.buf = sb.buf[1:]
		return true
	}
	// Safety valve: if the buffer exceeds 16 bytes, the leading \x1b
	// was likely a standalone Escape mixed with garbage. Emit it.
	if len(sb.buf) > 16 {
		sb.appendEvent(string(sb.buf[:1]))
		sb.buf = sb.buf[1:]
		return true
	}
	// Escape sequence started but incomplete — signal caller to wait.
	return false
}

func (sb *StdinBuffer) handlePlainByte() {
	b := sb.buf[0]
	if b < 128 {
		sb.appendEvent(string(sb.buf[:1]))
		sb.buf = sb.buf[1:]
		return
	}
	r, size := decodeRune(sb.buf)
	if size > 0 {
		sb.appendEvent(string(sb.buf[:size]))
		sb.buf = sb.buf[size:]
	} else {
		sb.appendEvent(string(sb.buf[:1]))
		sb.buf = sb.buf[1:]
	}
	_ = r
}

func (sb *StdinBuffer) handlePaste(pasteStart, pasteEnd []byte) bool {
	if !sb.inPaste && bytes.HasPrefix(sb.buf, pasteStart) {
		sb.inPaste = true
		sb.buf = sb.buf[len(pasteStart):]
		sb.pasteBuf.Reset()
		return true
	}
	if sb.inPaste {
		if idx := bytes.Index(sb.buf, pasteEnd); idx >= 0 {
			sb.pasteBuf.Write(sb.buf[:idx])
			sb.appendEvent(sb.pasteBuf.String())
			sb.inPaste = false
			sb.pasteBuf.Reset()
			sb.buf = sb.buf[idx+len(pasteEnd):]
			return true
		}
		sb.pasteBuf.Write(sb.buf)
		sb.buf = nil
		return true // returns true but caller must stop (buf is nil)
	}
	return false
}

func (sb *StdinBuffer) appendEvent(s string) {
	if s != "" {
		sb.events = append(sb.events, s)
	}
}

func decodeRune(buf []byte) (rune, int) {
	if len(buf) == 0 {
		return 0, 0
	}
	b := buf[0]
	var size int
	switch {
	case b < 0x80:
		size = 1
	case b < 0xE0:
		size = 2
	case b < 0xF0:
		size = 3
	default:
		size = 4
	}
	if len(buf) < size {
		return 0, 0
	}
	var r rune
	switch size {
	case 1:
		r = rune(b)
	case 2:
		r = rune(b&0x1F)<<6 | rune(buf[1]&0x3F)
	case 3:
		r = rune(b&0x0F)<<12 | rune(buf[1]&0x3F)<<6 | rune(buf[2]&0x3F)
	case 4:
		r = rune(b&0x07)<<18 | rune(buf[1]&0x3F)<<12 | rune(buf[2]&0x3F)<<6 | rune(buf[3]&0x3F)
	}
	return r, size
}
