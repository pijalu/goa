// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package internal

import (
	"strings"
	"sync"
	"testing"
)

func TestNewRingBuffer_DefaultSize(t *testing.T) {
	rb := NewRingBuffer(0)
	if rb == nil {
		t.Fatal("NewRingBuffer should return non-nil")
	}
	if rb.Len() != 0 {
		t.Errorf("New buffer should be empty, got Len() = %d", rb.Len())
	}
}

func TestRingBuffer_WriteAndRead(t *testing.T) {
	rb := NewRingBuffer(10)
	rb.Write("line1")
	rb.Write("line2")
	rb.Write("line3")

	if rb.Len() != 3 {
		t.Errorf("Expected 3 lines, got %d", rb.Len())
	}

	lines := rb.Read(3)
	if len(lines) != 3 {
		t.Fatalf("Expected 3 lines back, got %d", len(lines))
	}
	if lines[0] != "line1" || lines[1] != "line2" || lines[2] != "line3" {
		t.Errorf("Lines out of order: got %v", lines)
	}
}

func TestRingBuffer_ReadTail(t *testing.T) {
	rb := NewRingBuffer(10)
	for i := 0; i < 10; i++ {
		rb.Write("line")
	}

	lines := rb.Read(3)
	if len(lines) != 3 {
		t.Errorf("Expected 3 lines with tail=3, got %d", len(lines))
	}
}

func TestRingBuffer_ReadAll(t *testing.T) {
	rb := NewRingBuffer(10)
	rb.Write("a")
	rb.Write("b")
	rb.Write("c")

	lines := rb.ReadAll()
	if len(lines) != 3 {
		t.Fatalf("Expected 3 lines, got %d", len(lines))
	}
	if strings.Join(lines, ",") != "a,b,c" {
		t.Errorf("Expected order a,b,c, got: %v", lines)
	}
}

func TestRingBuffer_CircularOverwrite(t *testing.T) {
	rb := NewRingBuffer(3)
	for i := 0; i < 5; i++ {
		rb.Write("line")
	}
	if rb.Len() != 3 {
		t.Errorf("Buffer should have at most 3 entries after overwrite, got %d", rb.Len())
	}
}

func TestRingBuffer_ReadEmpty(t *testing.T) {
	rb := NewRingBuffer(10)
	lines := rb.Read(5)
	if lines != nil {
		t.Errorf("Reading from empty buffer should return nil, got %v", lines)
	}
}

func TestRingBuffer_ReadAllEmpty(t *testing.T) {
	rb := NewRingBuffer(10)
	lines := rb.ReadAll()
	if lines != nil {
		t.Errorf("ReadAll on empty buffer should return nil, got %v", lines)
	}
}

func TestRingBuffer_ReadZeroTail_ReturnsAll(t *testing.T) {
	rb := NewRingBuffer(10)
	rb.Write("a")
	rb.Write("b")
	lines := rb.Read(0)
	if len(lines) != 2 {
		t.Errorf("Read(0) should return all lines, got %d", len(lines))
	}
}

func TestRingBuffer_ReadNegativeTail_ReturnsAll(t *testing.T) {
	rb := NewRingBuffer(10)
	rb.Write("a")
	lines := rb.Read(-1)
	if len(lines) != 1 {
		t.Errorf("Read(-1) should return all lines, got %d", len(lines))
	}
}

func TestRingBuffer_Clear(t *testing.T) {
	rb := NewRingBuffer(10)
	rb.Write("a")
	rb.Write("b")
	rb.Clear()
	if rb.Len() != 0 {
		t.Errorf("After clear, Len() should be 0, got %d", rb.Len())
	}
}

func TestRingBuffer_ReadExactCapacity(t *testing.T) {
	rb := NewRingBuffer(3)
	rb.Write("a")
	rb.Write("b")
	rb.Write("c")
	lines := rb.Read(3)
	if len(lines) != 3 {
		t.Fatalf("Expected 3 lines, got %d", len(lines))
	}
	expected := []string{"a", "b", "c"}
	for i, line := range lines {
		if line != expected[i] {
			t.Errorf("Line %d: got %q, want %q", i, line, expected[i])
		}
	}
}

func TestRingBuffer_ConcurrentAccess(t *testing.T) {
	rb := NewRingBuffer(100)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				rb.Write("data")
			}
		}()
	}
	wg.Wait()
	if rb.Len() == 0 {
		t.Error("Buffer should not be empty after concurrent writes")
	}
}

func TestRingBuffer_ReadAfterFullOverwrite(t *testing.T) {
	rb := NewRingBuffer(2)
	rb.Write("a")
	rb.Write("b")
	rb.Write("c") // overwrites "a"
	lines := rb.Read(2)
	if len(lines) != 2 {
		t.Fatalf("Expected 2 lines, got %d", len(lines))
	}
	if lines[0] != "b" || lines[1] != "c" {
		t.Errorf("Expected [b, c], got %v", lines)
	}
}
