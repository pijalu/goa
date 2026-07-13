// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package background

import "testing"

func TestRingBuffer_SetsSize(t *testing.T) {
	rb := newRingBuffer(10)
	if rb.size != 10 {
		t.Errorf("expected size 10, got %d", rb.size)
	}
	if rb.count != 0 {
		t.Errorf("expected count 0, got %d", rb.count)
	}
}

func TestRingBuffer_WriteAndRead_FewerThanCapacity(t *testing.T) {
	rb := newRingBuffer(5)
	rb.Write("line1")
	rb.Write("line2")
	rb.Write("line3")

	lines := rb.ReadLast(2)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(lines), lines)
	}
	if lines[0] != "line2" {
		t.Errorf("expected line2, got %q", lines[0])
	}
	if lines[1] != "line3" {
		t.Errorf("expected line3, got %q", lines[1])
	}
}

func TestRingBuffer_ReadAll_FewerThanCapacity(t *testing.T) {
	rb := newRingBuffer(10)
	rb.Write("a")
	rb.Write("b")
	rb.Write("c")

	lines := rb.ReadLast(10)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (clamped to count), got %d: %v", len(lines), lines)
	}
}

func TestRingBuffer_Write_OverCapacity(t *testing.T) {
	rb := newRingBuffer(3)
	rb.Write("a")
	rb.Write("b")
	rb.Write("c")
	rb.Write("d") // overflows, should evict "a"
	rb.Write("e") // overflows, should evict "b"

	lines := rb.ReadLast(3)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %v", len(lines), lines)
	}
	if lines[0] != "c" {
		t.Errorf("expected c, got %q", lines[0])
	}
	if lines[1] != "d" {
		t.Errorf("expected d, got %q", lines[1])
	}
	if lines[2] != "e" {
		t.Errorf("expected e, got %q", lines[2])
	}
}

func TestRingBuffer_ReadLast_LargerThanCount(t *testing.T) {
	rb := newRingBuffer(10)
	rb.Write("only")

	lines := rb.ReadLast(100)
	if len(lines) != 1 {
		t.Errorf("expected 1 line (clamped), got %d", len(lines))
	}
}

func TestRingBuffer_ReadLast_FromEmpty(t *testing.T) {
	rb := newRingBuffer(5)

	lines := rb.ReadLast(3)
	if len(lines) != 0 {
		t.Errorf("expected 0 lines from empty buffer, got %d", len(lines))
	}
}

func TestRingBuffer_Write_AfterOverflow(t *testing.T) {
	rb := newRingBuffer(2)
	rb.Write("a")
	rb.Write("b")
	rb.Write("c")
	rb.Write("d")

	lines := rb.ReadLast(2)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(lines), lines)
	}
	// After [a,b] -> [c,b] -> [c,d], ReadLast(2) should return [c,d].
	if lines[0] != "c" || lines[1] != "d" {
		t.Errorf("expected [c,d], got %v", lines)
	}
}
