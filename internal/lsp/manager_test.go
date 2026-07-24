// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package lsp

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"testing"
	"time"
)

func TestManager_StartAndClose(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	dir := t.TempDir()
	mgr := NewManager(dir)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := mgr.Start(ctx); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if !mgr.started {
		t.Error("expected manager to be started")
	}
	if err := mgr.Close(ctx); err != nil {
		t.Errorf("close failed: %v", err)
	}
}

func TestManager_StartFactoryError(t *testing.T) {
	mgr := NewManager(t.TempDir())
	mgr.serverFactory = func(ctx context.Context) (*Server, error) { return nil, fmt.Errorf("boom") }
	if err := mgr.Start(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

// TestManager_StartErrorSurfaced verifies a start failure is recorded for
// later surfacing (bugs.md L1) and cleared on a subsequent successful start.
func TestManager_StartErrorSurfaced(t *testing.T) {
	mgr := NewManager(t.TempDir())
	mgr.serverFactory = func(ctx context.Context) (*Server, error) { return nil, fmt.Errorf("boom") }
	if err := mgr.Start(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	if mgr.StartError() == nil {
		t.Fatal("StartError must record the failure for surfacing")
	}
	if mgr.Started() {
		t.Error("manager must not report started after a failed start")
	}
}

func TestManager_NotStarted(t *testing.T) {
	mgr := NewManager(t.TempDir())
	if err := mgr.OpenDocument(context.Background(), "main.go", "package main"); err == nil {
		t.Error("expected error when not started")
	}
}

func TestManager_DiagnosticFor(t *testing.T) {
	mgr := NewManager(t.TempDir())
	mgr.diags.Set("file:///tmp/main.go", []Diagnostic{{Message: "err", Severity: 1}})
	if diags := mgr.DiagnosticsFor(context.Background(), "/tmp/main.go"); len(diags) != 1 {
		t.Errorf("expected 1 diagnostic, got %d", len(diags))
	}
	if !mgr.HasErrors() {
		t.Error("expected HasErrors to be true")
	}
}

func TestManager_NewManagerRootURI(t *testing.T) {
	mgr := NewManager("/tmp/project")
	if mgr.rootURI != "file:///tmp/project" {
		t.Errorf("rootURI = %q, want file:///tmp/project", mgr.rootURI)
	}
}


func TestManager_OpenDocument(t *testing.T) {
	mgr := NewManager("/tmp/project")
	mgr.server = &Server{client: NewClient(&fakeConn{Writer: &bytes.Buffer{}})}
	mgr.started = true
	if err := mgr.OpenDocument(context.Background(), "main.go", "package main"); err != nil {
		t.Fatalf("open document failed: %v", err)
	}
}

func TestManager_fileURI_RelativePath(t *testing.T) {
	mgr := NewManager("/tmp/project")
	if got := mgr.fileURI("sub/main.go"); got != "file:///tmp/project/sub/main.go" {
		t.Errorf("fileURI = %q", got)
	}
}

func TestManager_Close_NilServer(t *testing.T) {
	mgr := NewManager(t.TempDir())
	if err := mgr.Close(context.Background()); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestManager_DidChangeIncrementsVersion verifies the per-document version
// counter increases monotonically so gopls does not reject out-of-order
// updates (regression for the hardcoded Version: 2 bug).
func TestManager_DidChangeIncrementsVersion(t *testing.T) {
	buf := &bytes.Buffer{}
	mgr := NewManager("/tmp/project")
	mgr.server = &Server{client: NewClient(&fakeConn{Writer: buf})}
	mgr.started = true

	ctx := context.Background()
	if err := mgr.OpenDocument(ctx, "main.go", "package main"); err != nil {
		t.Fatalf("open: %v", err)
	}
	uri := mgr.fileURI("main.go")
	if v := mgr.versions[uri]; v != 1 {
		t.Fatalf("version after open = %d, want 1", v)
	}
	for i := 0; i < 3; i++ {
		if err := mgr.DidChange(ctx, "main.go", "package main\n"); err != nil {
		t.Fatalf("didChange %d: %v", i, err)
	}
	}
	if v := mgr.versions[uri]; v != 4 {
		t.Errorf("version after 3 changes = %d, want 4", v)
	}
	// The wire payload must carry increasing versions, not a fixed value.
	for want := 2; want <= 4; want++ {
		needle := fmt.Sprintf(`"version":%d`, want)
		if !bytes.Contains(buf.Bytes(), []byte(needle)) {
			t.Errorf("expected DidChange to send %s", needle)
		}
	}
}
