// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"testing"
)

func TestStdinBufferBasic(t *testing.T) {
	sb := NewStdinBuffer()
	events := sb.Process([]byte{0x1b, '[', 'A'}) // Cursor up
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d: %v", len(events), events)
	}
	if events[0] != "\x1b[A" {
		t.Errorf("expected \x1b[A, got %q", events[0])
	}
}

func TestStdinBufferMultiple(t *testing.T) {
	sb := NewStdinBuffer()
	events := sb.Process([]byte{'h', 'e', 'y'})
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d: %v", len(events), events)
	}
	if events[0] != "h" || events[1] != "e" || events[2] != "y" {
		t.Errorf("expected h e y, got %v", events)
	}
}

func TestStdinBufferPartialSequence(t *testing.T) {
	sb := NewStdinBuffer()

	// First read: incomplete CSI
	events := sb.Process([]byte{0x1b, '['})
	if len(events) != 0 {
		t.Fatalf("expected 0 events for incomplete sequence, got %d: %v", len(events), events)
	}

	// Second read: completes the sequence
	events = sb.Process([]byte{'A'})
	if len(events) != 1 {
		t.Fatalf("expected 1 event after completion, got %d: %v", len(events), events)
	}
	if events[0] != "\x1b[A" {
		t.Errorf("expected \x1b[A, got %q", events[0])
	}
}

func TestStdinBufferSS3(t *testing.T) {
	sb := NewStdinBuffer()
	events := sb.Process([]byte{0x1b, 'O', 'P'}) // SS3 F1
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d: %v", len(events), events)
	}
	if events[0] != "\x1bOP" {
		t.Errorf("expected \x1bOP, got %q", events[0])
	}
}

func TestStdinBufferOSC(t *testing.T) {
	sb := NewStdinBuffer()
	// ESC ] 0 ; title BEL
	events := sb.Process([]byte{0x1b, ']', '0', ';', 't', 'e', 's', 't', 0x07})
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d: %v", len(events), events)
	}
	if events[0] != "\x1b]0;test\x07" {
		t.Errorf("expected OSC sequence, got %q", events[0])
	}
}

func TestStdinBufferBracketedPaste(t *testing.T) {
	sb := NewStdinBuffer()
	// \x1b[200~hello\x1b[201~ → should emit single event with "hello"
	data := []byte{0x1b, '[', '2', '0', '0', '~', 'h', 'e', 'l', 'l', 'o', 0x1b, '[', '2', '0', '1', '~'}
	events := sb.Process(data)
	if len(events) != 1 {
		t.Fatalf("expected 1 event (paste content), got %d: %v", len(events), events)
	}
	if events[0] != "hello" {
		t.Errorf("expected paste content 'hello', got %q", events[0])
	}
}

func TestStdinBufferUTF8(t *testing.T) {
	sb := NewStdinBuffer()
	events := sb.Process([]byte("héllo"))
	if len(events) != 5 {
		t.Fatalf("expected 5 runes, got %d: %v", len(events), events)
	}
	if events[0] != "h" {
		t.Errorf("expected 'h', got %q", events[0])
	}
	if events[1] != "é" {
		t.Errorf("expected 'é', got %q", events[1])
	}
}

func TestStdinBufferDefault(t *testing.T) {
	sb := NewStdinBuffer()
	if sb == nil {
		t.Fatal("NewStdinBuffer returned nil")
	}
}

func TestStdinBufferMultipleProcessCalls(t *testing.T) {
	sb := NewStdinBuffer()
	events := sb.Process([]byte{'a'})
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d: %v", len(events), events)
	}
	if events[0] != "a" {
		t.Errorf("expected 'a', got %q", events[0])
	}

	events2 := sb.Process([]byte{'b'})
	if len(events2) != 1 {
		t.Fatalf("expected 1 event on second call, got %d: %v", len(events2), events2)
	}
	if events2[0] != "b" {
		t.Errorf("expected 'b', got %q", events2[0])
	}
}

func TestStdinBufferKittyCSIU(t *testing.T) {
	sb := NewStdinBuffer()
	// Kitty CSI-u: ESC[code;mod u
	events := sb.Process([]byte{0x1b, '[', '9', '7', ';', '1', 'u'})
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d: %v", len(events), events)
	}
	expected := "\x1b[97;1u"
	if events[0] != expected {
		t.Errorf("expected %q, got %q", expected, events[0])
	}
}

func TestForwardToInput_NoDuplication(t *testing.T) {
	// Verify raw Kitty CSI-u sequences are forwarded without decodeKeys.
	pt := NewProcessTerminal()
	received := []string{}
	pt.onInput = func(s string) {
		received = append(received, s)
	}
	pt.forwardToInput(string([]byte{0x1b, '[', '9', '7', ';', '1', 'u'}))
	if len(received) != 1 {
		t.Fatalf("expected 1 event, got %d: %v", len(received), received)
	}
	expected := "\x1b[97;1u"
	if received[0] != expected {
		t.Errorf("expected raw sequence %q, got %q", expected, received[0])
	}
}

func TestFullKittyKeyFlow(t *testing.T) {
	pt := NewProcessTerminal()
	pt.protoPending.Store(false)

	received := []string{}
	pt.onInput = func(s string) {
		received = append(received, s)
	}

	pt.forwardToInput(string([]byte{0x1b, '[', '9', '7', ';', '1', 'u'}))

	if len(received) != 1 {
		t.Fatalf("expected 1 event, got %d: %v", len(received), received)
	}
	expected := "\x1b[97;1u"
	if received[0] != expected {
		t.Errorf("expected raw sequence %q, got %q", expected, received[0])
	}
}

func TestForwardToInput_StdinBufferPersistence(t *testing.T) {
	// Verify split sequences across multiple calls are correctly assembled
	pt := NewProcessTerminal()
	received := []string{}
	pt.onInput = func(s string) {
		received = append(received, s)
	}

	// Split an ESC[97;1u sequence across two calls
	pt.forwardToInput(string([]byte{0x1b, '['}))
	pt.forwardToInput(string([]byte{'9', '7', ';', '1', 'u'}))

	if len(received) != 1 {
		t.Fatalf("expected 1 event after split read, got %d: %v", len(received), received)
	}
	expected := "\x1b[97;1u"
	if received[0] != expected {
		t.Errorf("expected assembled raw sequence %q, got %q", expected, received[0])
	}
}

func TestDecodeKeys_UpArrow(t *testing.T) {
	keys := decodeKeys([]byte("\x1b[A"))
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d: %v", len(keys), keys)
	}
	if keys[0] != "up" {
		t.Errorf("expected 'up', got %q", keys[0])
	}
}

func TestDecodeKeys_CtrlC(t *testing.T) {
	keys := decodeKeys([]byte{0x03})
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d: %v", len(keys), keys)
	}
	if keys[0] != "ctrl+c" {
		t.Errorf("expected 'ctrl+c', got %q", keys[0])
	}
}

func TestDecodeKeys_Enter(t *testing.T) {
	keys := decodeKeys([]byte{0x0d})
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d: %v", len(keys), keys)
	}
	if keys[0] != "enter" {
		t.Errorf("expected 'enter', got %q", keys[0])
	}
}

func TestDecodeKeys_Multiple(t *testing.T) {
	keys := decodeKeys([]byte("\x1b[A\x1b[B"))
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d: %v", len(keys), keys)
	}
	if keys[0] != "up" || keys[1] != "down" {
		t.Errorf("expected [up down], got %v", keys)
	}
}

func TestDecodeKeys_Fallback(t *testing.T) {
	keys := decodeKeys([]byte{0x1b})
	if len(keys) != 1 {
		t.Fatalf("expected 1 key for bare ESC, got %d: %v", len(keys), keys)
	}
}

func TestStdinBuffer_BareEscape(t *testing.T) {
	sb := NewStdinBuffer()
	// A single 0x1b byte should be emitted as a bare Escape event
	events := sb.Process([]byte{0x1b})
	if len(events) != 1 {
		t.Fatalf("expected 1 event for bare ESC, got %d: %v", len(events), events)
	}
	if events[0] != "\x1b" {
		t.Errorf("expected bare \\x1b, got %q", events[0])
	}
}

func TestStdinBuffer_BareEscapeWithSequence(t *testing.T) {
	sb := NewStdinBuffer()
	// Bare ESC followed by CSI in the same read: ESC should be emitted first,
	// then CSI as a separate event.
	events := sb.Process([]byte{0x1b, 0x1b, '[', 'A'})
	if len(events) != 2 {
		t.Fatalf("expected 2 events (bare ESC + Up), got %d: %v", len(events), events)
	}
	if events[0] != "\x1b" {
		t.Errorf("expected bare \\x1b, got %q", events[0])
	}
	if events[1] != "\x1b[A" {
		t.Errorf("expected \\x1b[A, got %q", events[1])
	}
}

func TestStdinBuffer_StillWaitsForPartialSequence(t *testing.T) {
	sb := NewStdinBuffer()
	// ESC + [ is incomplete CSI — should still wait, not emit bare ESC
	events := sb.Process([]byte{0x1b, '['})
	if len(events) != 0 {
		t.Fatalf("expected 0 events for incomplete CSI, got %d: %v", len(events), events)
	}
}

func TestStdinBuffer_EscapeAfterIncompleteCSI(t *testing.T) {
	sb := NewStdinBuffer()
	// Simulate two reads: first leaves stale \\x1b[, then user presses Escape.
	// The stale \\x1b is emitted as Escape, then the remaining bytes are
	// processed as individual chars.
	first := sb.Process([]byte{0x1b, '['})
	if len(first) != 0 {
		t.Fatalf("expected 0 events for incomplete CSI, got %d", len(first))
	}

	// User presses Escape. The buffer now has stale \\x1b[ + new \\x1b.
	// The stale \\x1b is emitted as Escape, then [ is a regular char,
	// then \\x1b is the actual Escape press.
	second := sb.Process([]byte{0x1b})
	if len(second) != 3 {
		t.Fatalf("expected 3 events (stale ESC + [ + actual ESC), got %d: %v", len(second), second)
	}
	// Events: stale \\x1b, literal [, actual \\x1b (Escape key)
	if second[0] != "\x1b" || second[2] != "\x1b" {
		t.Errorf("expected ESC + [ + ESC, got %v", second)
	}
	// Extra check: the remaining stale [ was emitted as literal char
	if second[1] != "[" {
		t.Errorf("expected literal '[', got %q", second[1])
	}
}

func TestStdinBuffer_SafetyValveLargeBuffer(t *testing.T) {
	sb := NewStdinBuffer()
	// Buffer with stale CSI prefix and too much garbage (17 bytes).
	// Safety valve should kick in and emit the stale \\x1b, then
	// remaining bytes are individual chars.
	buf := []byte{0x1b, '['}
	for i := 0; i < 15; i++ {
		buf = append(buf, byte(i))
	}
	events := sb.Process(buf)
	// First event is the stale ESC from the safety valve
	if len(events) < 1 || events[0] != "\x1b" {
		t.Errorf("expected first event to be bare ESC from safety valve, got %v", events)
	}
}
