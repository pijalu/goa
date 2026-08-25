// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"testing"
)

// TestChatViewport_RenderReturnsOwnedSnapshot pins the publish contract of
// Render: every returned slice is owned by the caller, never a live view over
// the internal frame cache. Scenes built from Render output are composed
// asynchronously on the render goroutine while the command loop keeps
// mutating component state — returning the cache slice itself let a later
// fullRebuild append overwrite rows a concurrent compose was still reading
// through Layer.Content (data race in filmstrip scenarios).
func TestChatViewport_RenderReturnsOwnedSnapshot(t *testing.T) {
	cv := NewChatViewport()
	cv.AddUserMessage("hello")

	first := cv.Render(60)
	if len(first) == 0 {
		t.Fatal("expected non-empty rendered frame")
	}

	// Unchanged state → fast path. The second frame must still be a fresh,
	// independent allocation, not the same backing array.
	second := cv.Render(60)
	if len(second) != len(first) {
		t.Fatalf("fast-path render length changed: %d -> %d", len(first), len(second))
	}
	if &first[0] == &second[0] {
		t.Fatal("Render returned the same backing array twice; published frames are not owned copies")
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("line %d changed between identical renders: %q vs %q", i, first[i], second[i])
		}
	}
}

// TestChatViewport_PublishedFrameSafeDuringRebuild mirrors the production
// goroutine topology that exposed the race: one goroutine owns the viewport
// and repeatedly mutates + renders (the command loop building scene
// snapshots), while another continuously reads previously published frames
// (the render loop composing an older scene). Under -race any write to memory
// still referenced by a published frame is flagged.
func TestChatViewport_PublishedFrameSafeDuringRebuild(t *testing.T) {
	cv := NewChatViewport()

	published := make(chan []string, 8) // scenes awaiting composition
	done := make(chan struct{})

	// Composer: reads published frames end-to-end, like placeLayer walking
	// Layer.Content.
	go func() {
		defer close(done)
		for frame := range published {
			sum := 0
			for _, line := range frame {
				sum += len(line)
			}
			if sum < 0 {
				t.Errorf("impossible checksum %d", sum)
			}
		}
	}()

	const frames = 300
	for i := 0; i < frames; i++ {
		cv.AddAssistantMessage(fmt.Sprintf("streaming delta %d", i))
		published <- cv.Render(60)
	}
	close(published)
	<-done
}
