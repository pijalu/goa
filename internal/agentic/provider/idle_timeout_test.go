// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"
)

func TestIdleTimeoutReader_PassesThroughData(t *testing.T) {
	data := []byte("hello world")
	r := NewIdleTimeoutReader(io.NopCloser(bytes.NewReader(data)), time.Second)

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("expected %q, got %q", data, got)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close error: %v", err)
	}
}

func TestIdleTimeoutReader_DisabledWithZeroTimeout(t *testing.T) {
	// A reader that never sends data would hang forever, but with the guard
	// disabled we just return it unchanged and never call it in this test.
	r := NewIdleTimeoutReader(io.NopCloser(io.LimitReader(&neverReader{}, 0)), 0)
	if _, ok := r.(*idleTimeoutReader); ok {
		t.Fatal("expected wrapper to be elided when timeout is zero")
	}
}

func TestIdleTimeoutReader_FiresOnStall(t *testing.T) {
	r := NewIdleTimeoutReader(io.NopCloser(&neverReader{}), 50*time.Millisecond)

	buf := make([]byte, 8)
	start := time.Now()
	_, err := r.Read(buf)
	elapsed := time.Since(start)

	if !errors.Is(err, ErrStreamIdle) {
		t.Fatalf("expected ErrStreamIdle, got %v", err)
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("idle timeout took too long: %v", elapsed)
	}
}

func TestIdleTimeoutReader_ResetsAfterData(t *testing.T) {
	// First byte arrives immediately, then the connection stalls until the
	// test releases it. The timeout is measured from the last byte, not the
	// start of the stream.
	release := make(chan struct{})
	src := &stallingReader{first: []byte("x"), release: release}
	r := NewIdleTimeoutReader(io.NopCloser(src), 80*time.Millisecond)

	buf := make([]byte, 1)
	if _, err := r.Read(buf); err != nil {
		t.Fatalf("first read failed: %v", err)
	}
	if string(buf) != "x" {
		t.Fatalf("expected first byte 'x', got %q", buf)
	}

	// Keep the connection stalled for longer than the idle timeout before
	// releasing it. The read must fail with ErrStreamIdle before release.
	time.AfterFunc(200*time.Millisecond, func() { close(release) })

	start := time.Now()
	_, err := r.Read(buf)
	elapsed := time.Since(start)

	if !errors.Is(err, ErrStreamIdle) {
		t.Fatalf("expected ErrStreamIdle, got %v", err)
	}
	if elapsed < 50*time.Millisecond {
		t.Fatalf("timeout fired too early: %v", elapsed)
	}
}

func TestIdleTimeoutReader_CloseAfterTimeout(t *testing.T) {
	r := NewIdleTimeoutReader(io.NopCloser(&neverReader{}), 10*time.Millisecond)
	_, _ = r.Read(make([]byte, 1))

	// Closing after a timeout must be safe and idempotent.
	if err := r.Close(); err != nil {
		t.Fatalf("close error: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("second close error: %v", err)
	}
}

// neverReader never returns data.
type neverReader struct{}

func (neverReader) Read([]byte) (int, error) {
	select {}
}

// stallingReader returns first immediately and then blocks until release is closed.
type stallingReader struct {
	first   []byte
	release chan struct{}
	done    bool
}

func (s *stallingReader) Read(p []byte) (int, error) {
	if !s.done {
		s.done = true
		n := copy(p, s.first)
		return n, nil
	}
	<-s.release
	return 0, io.EOF
}
