// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package multiagent

import (
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// newTestOrchestrator builds a ForegroundOrchestrator with a minimal pool for
// emit-path tests (no live model needed).
func newTestOrchestrator(t *testing.T) *ForegroundOrchestrator {
	t.Helper()
	var m provider.Model
	pool := NewAgentPool(m, provider.StreamOptions{}, nil)
	return NewForegroundOrchestrator(pool)
}

// Structural orchestrator events (stream_start/stream_end, thinking_start/end,
// lifecycle, and especially GATE_APPROVAL) must never be silently dropped when
// the events buffer is full — a dropped stream_end or gate message breaks the
// UI state machine or hangs a gate. Only high-frequency stream/thinking CHUNKS
// may be dropped (they are best-effort deltas, reconciled at stream_end).
// Regression test for C4.
func TestEmitKind_StructuralEventsNotDropped(t *testing.T) {
	o := newTestOrchestrator(t)

	// Fill the buffer to capacity with chunks and DO NOT drain, so a lossy
	// emit (select/default) deterministically drops the next message.
	for i := 0; i < cap(o.events); i++ {
		o.emitKind("coder", "stream_chunk", "delta", "content")
	}
	if len(o.events) != cap(o.events) {
		t.Fatalf("setup: buffer not full (%d/%d)", len(o.events), cap(o.events))
	}

	// Emit structural events into the FULL buffer with no concurrent reader.
	// Lossy emit would drop them (they never enter the buffer); reliable emit
	// blocks, so we run it in a goroutine and give it a moment to enqueue.
	emitDone := make(chan struct{})
	go func() {
		defer close(emitDone)
		o.emitKind("coder", "stream_start", "", "content")
		o.emitKind("coder", "stream_start", "", "thinking_start")
		o.emitKind("coder", "stream_end", "full", "content")
		o.emitKind("coder", "thinking_end", "th", "thinking_end")
		o.emit("gate", "user", "GATE_APPROVAL:stage1|Stage|ok?")
	}()

	// Whether the structural events were dropped or blocked, drain everything
	// now and require all five to be observed.
	want := map[string]bool{
		"stream_start": false, "thinking_start": false, "stream_end": false, "thinking_end": false, "gate": false,
	}
	timeout := time.After(3 * time.Second)
	drained := 0
	for {
		select {
		case m := <-o.events:
			drained++
			if m.To == "stream_start" && m.Kind == "content" {
				want["stream_start"] = true
			}
			if m.Kind == "thinking_start" {
				want["thinking_start"] = true
			}
			if m.To == "stream_end" {
				want["stream_end"] = true
			}
			if m.Kind == "thinking_end" {
				want["thinking_end"] = true
			}
			if m.From == "gate" {
				want["gate"] = true
			}
			if want["stream_start"] && want["thinking_start"] && want["stream_end"] && want["thinking_end"] && want["gate"] {
				return // all structural events survived a full buffer
			}
		case <-timeout:
			t.Fatalf("structural/gate events lost with a full buffer (drained=%d): %v", drained, want)
		}
	}
}

// Stream/thinking chunks MAY be dropped under a full buffer (lossy fanout is
// intentional for high-frequency deltas). This just documents the contract so
// a future change making chunks blocking is a deliberate decision.
func TestEmitKind_ChunksMayDrop(t *testing.T) {
	o := newTestOrchestrator(t)
	for i := 0; i < cap(o.events)+100; i++ {
		o.emitKind("coder", "stream_chunk", "delta", "content")
	}
	// Must not block or panic; buffer holds at most cap() items.
	if len(o.events) > cap(o.events) {
		t.Errorf("chunk fanout exceeded buffer capacity")
	}
}
