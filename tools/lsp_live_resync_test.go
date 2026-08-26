// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLiveLSP_ResyncExternal_FreshensGoplsOverlay replays the reported
// stale-gopls incident against a REAL gopls: a document is opened with
// content A (deliberate compile error), then mutated on disk behind the
// manager's back exactly like a shell command would (sed -i / git checkout),
// swapping the error's identifier. Without ResyncExternal, gopls keeps its
// overlay of A and reports `undefinedOne` forever; after the reconcile it
// must analyze B and report `undefinedTwo`.
func TestLiveLSP_ResyncExternal_FreshensGoplsOverlay(t *testing.T) {
	dir := t.TempDir()
	mgr := liveLSPManager(t, dir)

	// Minimal Go module so gopls has a package to load.
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module live\n\ngo 1.25\n"), 0644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "main.go")
	staleContent := "package main\n\nfunc main() { _ = undefinedOne }\n"
	if err := os.WriteFile(path, []byte(staleContent), 0644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Block until the real server is up (waitClientFor), then open like the
	// read tool does: with a point-in-time snapshot of disk.
	if _, err := mgr.PullDiagnostics(ctx, path); err != nil {
		t.Skipf("gopls unavailable: %v", err)
	}
	if err := mgr.OpenDocument(ctx, path, staleContent); err != nil {
		t.Fatalf("open: %v", err)
	}

	// External mutation — replaces the erroneous identifier, i.e. the exact
	// sed -i 'NNN,MMM d' + rewrite shape from the incident.
	freshContent := "package main\n\nfunc main() { _ = undefinedTwo }\n"
	if err := os.WriteFile(path, []byte(freshContent), 0644); err != nil {
		t.Fatal(err)
	}

	if got := mgr.ResyncExternal(ctx); got != 1 {
		t.Fatalf("ResyncExternal refreshed=%d, want 1", got)
	}

	// gopls republishes asynchronously; publications for the pre-sync
	// analysis may legitimately land after the reconcile — so never
	// fail-fast on stale text. Success = a publication referencing the fresh
	// identifier within the deadline.
	deadline := time.Now().Add(20 * time.Second)
	var lastMsgs string
	for time.Now().Before(deadline) {
		pollCtx, cancelPoll := context.WithTimeout(context.Background(), 2*time.Second)
		snap := mgr.WaitDiagnostics(pollCtx, path)
		cancelPoll()
		var msgs []string
		for _, d := range snap.Diagnostics {
			msgs = append(msgs, d.Message)
			if strings.Contains(d.Message, "undefinedTwo") {
				return // gopls analyzed FRESH content: overlay reconciled
			}
		}
		lastMsgs = strings.Join(msgs, "; ")
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("deadline exceeded without diagnostics for fresh content (last: %q)", lastMsgs)
}
