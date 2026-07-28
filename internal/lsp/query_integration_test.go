// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package lsp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeGoWorkspace creates a minimal Go module with two files: a definition in
// helper.go and a use in main.go. Returns the dir.
func writeGoWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/lspq\n\ngo 1.21\n",
		"helper.go": `package main

// Greeting returns a friendly message.
func Greeting() string { return "hello" }
`,
		"main.go": `package main

func main() {
	println(Greeting())
}
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// startRealManager starts a real gopls manager on dir, skipping when gopls is
// unavailable.
func startRealManager(t *testing.T, dir string) *Manager {
	t.Helper()
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	mgr := NewManager(dir)
	// NOTE: do NOT cancel the Start ctx on return — the server subprocess is tied
	// to it (exec.CommandContext); cancelling here would kill gopls as soon as
	// the helper returns, breaking every later query (the "broken pipe"
	// failures). The manager is shut down via Close in the cleanup below.
	ctx := context.Background()
	if err := mgr.Start(ctx); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer ccancel()
		_ = mgr.Close(cctx)
	})
	return mgr
}

// openAndSettle opens a document explicitly and gives gopls a moment to load the
// package before position queries. Querying immediately after didOpen can hit a
// cold package load and (on some gopls versions) crash the server; a short
// settle makes the integration tests deterministic. The server spawn is
// asynchronous (touches never block), so this first waits for readiness.
func openAndSettle(t *testing.T, mgr *Manager, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if c := mgr.waitClientFor(ctx, path); c == nil {
		t.Fatalf("server for %s did not start", path)
	}
	if err := mgr.OpenDocument(ctx, path, string(data)); err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	time.Sleep(2 * time.Second)
}

// TestManager_Definition_RealGopls drives textDocument/definition against a
// real gopls and expects the Greeting symbol's definition in helper.go.
func TestManager_Definition_RealGopls(t *testing.T) {
	dir := writeGoWorkspace(t)
	mgr := startRealManager(t, dir)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// main.go line 3 (0-indexed): `	println(Greeting())` — Greeting at char 10.
	mainPath := filepath.Join(dir, "main.go")
	openAndSettle(t, mgr, mainPath)
	var locs []Location
	var err error
	// gopls may need a moment to load the package; poll briefly.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		locs, err = mgr.Definition(ctx, mainPath, 3, 10)
		if err == nil && len(locs) > 0 {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("Definition: %v", err)
	}
	if len(locs) == 0 {
		t.Fatalf("expected at least one definition location")
	}
	if !strings.HasSuffix(locs[0].URI, "helper.go") {
		t.Errorf("definition URI = %q, want helper.go", locs[0].URI)
	}
}

// TestManager_References_RealGopls expects references to Greeting in main.go.
func TestManager_References_RealGopls(t *testing.T) {
	dir := writeGoWorkspace(t)
	mgr := startRealManager(t, dir)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	helperPath := filepath.Join(dir, "helper.go")
	openAndSettle(t, mgr, helperPath)
	var locs []Location
	var err error
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		// helper.go line 3: `func Greeting() string { return "hello" }` — name at char 5.
		locs, err = mgr.References(ctx, helperPath, 3, 5)
		if err == nil && len(locs) > 0 {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("References: %v", err)
	}
	if len(locs) == 0 {
		t.Fatalf("expected at least one reference")
	}
	var sawMain bool
	for _, l := range locs {
		if strings.HasSuffix(l.URI, "main.go") {
			sawMain = true
		}
	}
	if !sawMain {
		t.Errorf("expected a reference in main.go, got %+v", locs)
	}
}

// TestManager_Hover_RealGopls expects hover info for Greeting.
func TestManager_Hover_RealGopls(t *testing.T) {
	dir := writeGoWorkspace(t)
	mgr := startRealManager(t, dir)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	helperPath := filepath.Join(dir, "helper.go")
	openAndSettle(t, mgr, helperPath)
	var h *Hover
	var err error
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		h, err = mgr.Hover(ctx, helperPath, 3, 5)
		if err == nil && h != nil {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("Hover: %v", err)
	}
	if h == nil {
		t.Fatalf("expected hover info for Greeting")
	}
}

// TestManager_DocumentSymbols_RealGopls expects Greeting + main symbols.
func TestManager_DocumentSymbols_RealGopls(t *testing.T) {
	dir := writeGoWorkspace(t)
	mgr := startRealManager(t, dir)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	mainPath := filepath.Join(dir, "main.go")
	openAndSettle(t, mgr, mainPath)
	var syms []DocumentSymbol
	var err error
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		syms, err = mgr.DocumentSymbols(ctx, mainPath)
		if err == nil && len(syms) > 0 {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("DocumentSymbols: %v", err)
	}
	var sawMain bool
	for _, s := range syms {
		if s.Name == "main" {
			sawMain = true
		}
	}
	if !sawMain {
		t.Errorf("expected a 'main' symbol, got %+v", syms)
	}
}
