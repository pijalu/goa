// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"context"
	"sync"
	"testing"
)

// fakeLSPSyncer records ResyncExternal calls made by the bash tool.
type fakeLSPSyncer struct {
	mu    sync.Mutex
	calls int
	ctxs  []context.Context
}

func (f *fakeLSPSyncer) ResyncExternal(ctx context.Context) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.ctxs = append(f.ctxs, ctx)
	return 0
}

func (f *fakeLSPSyncer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func newTestBashWithSyncer(syncer ExternalLSPSyncer) *BashTool {
	return &BashTool{ProjectDir: "", LSPSyncer: syncer}
}

// TestBashTool_ResyncsLSPAfterRun pins the stale-gopls fix: every executed
// command reconciles open language-server overlays with disk, because shell
// commands mutate files behind goa's structured file tools.
func TestBashTool_ResyncsLSPAfterRun(t *testing.T) {
	syncer := &fakeLSPSyncer{}
	tool := newTestBashWithSyncer(syncer)
	out, err := tool.Execute(`{"command":"echo hi"}`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out == "" {
		t.Fatal("expected command output")
	}
	if got := syncer.count(); got != 1 {
		t.Fatalf("ResyncExternal calls=%d, want 1", got)
	}
}

// TestBashTool_ResyncRunsEvenWhenCommandFails checks reconciliation happens
// for failing commands too: `cmd && false` style mutations occur before the
// non-zero exit, so the overlay refresh must not be skipped on error.
func TestBashTool_ResyncRunsEvenWhenCommandFails(t *testing.T) {
	syncer := &fakeLSPSyncer{}
	tool := newTestBashWithSyncer(syncer)
	if _, err := tool.Execute(`{"command":"exit 3"}`); err == nil {
		t.Fatal("expected non-zero exit to surface as a bash error")
	}
	if got := syncer.count(); got != 1 {
		t.Fatalf("ResyncExternal calls after failed command=%d, want 1", got)
	}
}

// TestBashTool_NoSyncerNoPanic keeps bash usable wherever no LSP manager is
// wired (tests, embeddoc hosts, tool-less harnesses).
func TestBashTool_NoSyncerNoPanic(t *testing.T) {
	tool := &BashTool{}
	if _, err := tool.Execute(`{"command":"echo ok"}`); err != nil {
		t.Fatalf("execute without syncer: %v", err)
	}
}

// TestBashTool_ValidationFailureSkipsResync ensures the hook fires only when
// a command actually ran — an invalid payload never reaches the shell.
func TestBashTool_ValidationFailureSkipsResync(t *testing.T) {
	syncer := &fakeLSPSyncer{}
	tool := newTestBashWithSyncer(syncer)
	if _, err := tool.Execute(`{"command":""}`); err == nil {
		t.Fatal("expected missing-command validation error")
	}
	if got := syncer.count(); got != 0 {
		t.Fatalf("ResyncExternal calls=%d, want 0 for rejected input", got)
	}
}
