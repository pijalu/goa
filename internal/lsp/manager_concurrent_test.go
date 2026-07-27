// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package lsp

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// TestManager_ConcurrentDidChange reproduces bugs.md Issue 19: the
// ToolScheduler executes independent tool calls in parallel goroutines, and
// every read/edit/write tool notifies the LSP manager (touchLSP). Files of
// the same language share one serverClient, so parallel DidChange calls hit
// the per-client versions map concurrently — before the fix this was an
// unguarded map and produced `fatal error: concurrent map writes`.
//
// Run with -race: pre-fix the detector fires immediately; without -race the
// runtime's own concurrent-map-write check is likely to abort the test.
func TestManager_ConcurrentDidChange(t *testing.T) {
	sink := &syncBuffer{}
	mgr := startedManager(t, t.TempDir(), &fakeServerRecorder{}, sink, testSpecs)
	ctx := context.Background()

	const goroutines = 8
	const opsPerGoroutine = 50
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				// A few files per goroutine, all .go → same gopls client.
				path := fmt.Sprintf("g%d_file%d.go", g, i%4)
				if err := mgr.DidChange(ctx, path, "package main\n"); err != nil {
					t.Errorf("didChange %s: %v", path, err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
}

// TestManager_ConcurrentOpenSameFileSingleDidOpen hammers DidChange on ONE
// not-yet-open file from many goroutines. Exactly one DidOpen may reach the
// server (a duplicate DidOpen for the same uri violates the LSP protocol);
// the racing openers must degrade to a full-content DidChange.
func TestManager_ConcurrentOpenSameFileSingleDidOpen(t *testing.T) {
	sink := &syncBuffer{}
	mgr := startedManager(t, t.TempDir(), &fakeServerRecorder{}, sink, testSpecs)
	ctx := context.Background()

	const goroutines = 16
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := mgr.DidChange(ctx, "same.go", "package main\n"); err != nil {
				t.Errorf("didChange: %v", err)
			}
		}()
	}
	wg.Wait()

	opens := strings.Count(sink.String(), `"method":"textDocument/didOpen"`)
	if opens != 1 {
		t.Fatalf("expected exactly 1 didOpen for same.go, got %d", opens)
	}
}

// TestManager_ConcurrentOpenAndChange mixes OpenDocument, DidChange and
// ensureOpen-driven position requests across files on one server — the
// pre-fix unguarded versions map aborted the process under this pattern.
func TestManager_ConcurrentOpenAndChange(t *testing.T) {
	sink := &syncBuffer{}
	dir := t.TempDir()
	mgr := startedManager(t, dir, &fakeServerRecorder{}, sink, testSpecs)
	ctx := context.Background()

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			path := fmt.Sprintf("mix%d.go", g%3)
			for i := 0; i < 30; i++ {
				switch i % 3 {
				case 0:
					_ = mgr.OpenDocument(ctx, path, "package main\n")
				case 1:
					_ = mgr.DidChange(ctx, path, "package main\n// x\n")
				default:
					// ensureOpen path via a position request (file absent on
					// disk → error is fine; the race is in versions access).
					_, _ = mgr.Hover(ctx, path, 0, 0)
				}
			}
		}(g)
	}
	wg.Wait()
}
