// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/lsp"
)

// pollFakeManager delivers diagnostics after a configurable delay, simulating
// gopls's asynchronous publishDiagnostics arriving after a cold package load.
type pollFakeManager struct {
	diags   []lsp.Diagnostic
	delay   time.Duration
	started time.Time
}

func (f *pollFakeManager) OpenDocument(ctx context.Context, path, text string) error { return nil }
func (f *pollFakeManager) DidChange(ctx context.Context, path, text string) error    { return nil }
func (f *pollFakeManager) DiagnosticsFor(ctx context.Context, path string) []lsp.Diagnostic {
	if time.Since(f.started) < f.delay {
		return nil
	}
	return f.diags
}

// TestCollectLSPDiagnostics_WaitsForLateDiagnostics is the regression for the
// fixed-150ms-sleep race (bugs.md L1): diagnostics arriving after 300ms must
// still be collected (the old code returned empty).
func TestCollectLSPDiagnostics_WaitsForLateDiagnostics(t *testing.T) {
	mgr := &pollFakeManager{
		diags:   []lsp.Diagnostic{{Severity: 1, Message: "undefined: x"}},
		delay:   300 * time.Millisecond,
		started: time.Now(),
	}
	diags := collectLSPDiagnostics(context.Background(), mgr, "/tmp/x.go")
	if len(diags) != 1 || diags[0].Message != "undefined: x" {
		t.Fatalf("expected late diagnostics to be collected, got %v", diags)
	}
}

// TestCollectLSPDiagnostics_FastPath returns immediately when diagnostics are
// already available (no needless waiting).
func TestCollectLSPDiagnostics_FastPath(t *testing.T) {
	mgr := &pollFakeManager{
		diags:   []lsp.Diagnostic{{Severity: 2, Message: "warn"}},
		delay:   0,
		started: time.Now(),
	}
	start := time.Now()
	diags := collectLSPDiagnostics(context.Background(), mgr, "/tmp/x.go")
	if len(diags) != 1 {
		t.Fatalf("expected diagnostics, got %v", diags)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Errorf("fast path should return immediately, took %v", elapsed)
	}
}

// TestCollectLSPDiagnostics_EmptyTimesOut confirms the collector returns empty
// (bounded, not hanging) when gopls never publishes for the file.
func TestCollectLSPDiagnostics_EmptyTimesOut(t *testing.T) {
	mgr := &pollFakeManager{diags: nil, delay: 0, started: time.Now()}
	start := time.Now()
	diags := collectLSPDiagnostics(context.Background(), mgr, "/tmp/x.go")
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics, got %v", diags)
	}
	if elapsed := time.Since(start); elapsed < lspPollTimeout {
		t.Errorf("should wait the full timeout for late diags, returned after %v", elapsed)
	}
}

// TestCollectLSPDiagnostics_NilManager is a no-op guard.
func TestCollectLSPDiagnostics_NilManager(t *testing.T) {
	if diags := collectLSPDiagnostics(context.Background(), nil, "/tmp/x.go"); diags != nil {
		t.Errorf("nil manager must yield nil diagnostics, got %v", diags)
	}
}

// TestCollectLSPDiagnostics_RespectsContextCancel stops waiting when the turn
// is cancelled rather than blocking for the full timeout.
func TestCollectLSPDiagnostics_RespectsContextCancel(t *testing.T) {
	mgr := &pollFakeManager{diags: nil, delay: 0, started: time.Now()}
	ctx, cancel := context.WithCancel(context.Background())
	var calls int64
	go func() {
		time.Sleep(100 * time.Millisecond)
		atomic.AddInt64(&calls, 1)
		cancel()
	}()
	start := time.Now()
	_ = collectLSPDiagnostics(ctx, mgr, "/tmp/x.go")
	if elapsed := time.Since(start); elapsed >= lspPollTimeout {
		t.Errorf("should stop early on cancel, took %v", elapsed)
	}
}
