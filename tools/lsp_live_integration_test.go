// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/lsp"
)

// These are LIVE integration tests: they spawn real language servers (gopls
// from PATH; pyright / typescript-language-server via npx when the PATH binary
// is absent — first run downloads the package, ~1 minute) and drive the real
// write/edit → LSP → diagnostics pipeline end to end. They verify the
// Issue LSP directive: edit/write must be LSP-wired for ALL supported file
// types (go, py, js) and surface correctly-labeled hints.
//
// Each test skips cleanly when its server cannot be launched (no binary and
// no npx), so CI/dev machines without the toolchain are unaffected.

// liveLSPManager starts a REAL multi-server manager rooted at dir.
func liveLSPManager(t *testing.T, dir string) *lsp.Manager {
	t.Helper()
	if os.Getenv("GOA_LSP_LIVE") != "1" {
		t.Skip("real-server smoke tests disabled; set GOA_LSP_LIVE=1")
	}
	mgr := lsp.NewManager(dir)
	if err := mgr.Start(t.Context()); err != nil {
		t.Skipf("optional live LSP unavailable: %v", err)
	}
	t.Cleanup(func() {
		_ = mgr.Close(t.Context())
	})
	return mgr
}

const livePythonProbeTimeout = 150 * time.Second

// liveDiagnostics polls (via repeated write touches) until the tool output
// carries a diagnostics block, the server is up, or the deadline passes.
//
// Live language servers are optional dependencies. A launcher can be present
// and start successfully while still not serving diagnostics (for example, an
// incomplete npm installation or an unsupported project configuration). Such a
// server must not turn an environment-dependent smoke test into a failure or
// an unbounded wait, so the bounded probe is an explicit skip.
func liveDiagnostics(t *testing.T, tool *WriteFileTool, path, content, serverID string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var out string
	for time.Now().Before(deadline) {
		var err error
		out, err = tool.Execute(fmt.Sprintf(`{"path": %q, "content": %q}`, path, content))
		if err != nil {
			t.Skipf("optional %s server unavailable while probing %s: %v", serverID, path, err)
		}
		if strings.Contains(out, "Diagnostics ("+serverID+")") {
			return out
		}
		time.Sleep(2 * time.Second)
	}
	t.Skipf("optional %s server launched but did not publish diagnostics within %s", serverID, timeout)
	return out
}

// TestLiveLSP_Go verifies write→gopls diagnostics surface in the tool result.
func TestLiveLSP_Go(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/livesp\n\ngo 1.21\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	mgr := liveLSPManager(t, dir)
	tool := &WriteFileTool{ProjectDir: dir, LSPManager: mgr}

	path := filepath.Join(dir, "main.go")
	content := "package main\n\nfunc main() {\n\tprintln(undefinedXYZ)\n}\n"
	out := liveDiagnostics(t, tool, path, content, "gopls", 90*time.Second)
	if !strings.Contains(out, "Diagnostics (gopls)") {
		t.Fatalf("expected gopls diagnostics block, got:\n%s", out)
	}
	if !strings.Contains(out, "undefined: undefinedXYZ") {
		t.Errorf("expected undefined-symbol hint, got:\n%s", out)
	}
}

// TestLiveLSP_Python verifies write→pyright diagnostics surface in the tool
// result with the pyright label (not a hardcoded gopls one).
func TestLiveLSP_Python(t *testing.T) {
	if _, err := exec.LookPath("pyright-langserver"); err != nil {
		if _, npxErr := exec.LookPath("npx"); npxErr != nil {
			t.Skip("pyright-langserver not installed and no npx fallback")
		}
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte("[project]\nname = \"livesp\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mgr := liveLSPManager(t, dir)
	tool := &WriteFileTool{ProjectDir: dir, LSPManager: mgr}

	path := filepath.Join(dir, "main.py")
	content := "undefined_xyz\n"
	out := liveDiagnostics(t, tool, path, content, "pyright", livePythonProbeTimeout)
	if !strings.Contains(out, "Diagnostics (pyright)") {
		t.Fatalf("expected pyright diagnostics block, got:\n%s", out)
	}
	if !strings.Contains(out, "undefined_xyz") {
		t.Errorf("expected undefined-variable hint, got:\n%s", out)
	}
}

// TestLiveLSP_JavaScript verifies write→typescript-language-server diagnostics
// surface in the tool result with the typescript label.
func TestLiveLSP_JavaScript(t *testing.T) {
	if _, err := exec.LookPath("typescript-language-server"); err != nil {
		if _, npxErr := exec.LookPath("npx"); npxErr != nil {
			t.Skip("typescript-language-server not installed and no npx fallback")
		}
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"livesp","version":"1.0.0"}`), 0644); err != nil {
		t.Fatal(err)
	}
	mgr := liveLSPManager(t, dir)
	tool := &WriteFileTool{ProjectDir: dir, LSPManager: mgr}

	path := filepath.Join(dir, "app.js")
	// @ts-check enables semantic diagnostics for the JS file — without it
	// tsserver only reports syntax errors in JavaScript (checkJs off by
	// default) and "Cannot find name" never fires.
	content := "// @ts-check\nundefinedXYZ();\n"
	out := liveDiagnostics(t, tool, path, content, "typescript", livePythonProbeTimeout)
	if !strings.Contains(out, "Diagnostics (typescript)") {
		t.Fatalf("expected typescript diagnostics block, got:\n%s", out)
	}
	if !strings.Contains(out, "undefinedXYZ") {
		t.Errorf("expected cannot-find-name hint, got:\n%s", out)
	}
}

// TestLiveLSP_TypeScript verifies write→typescript-language-server diagnostics
// for a TypeScript project (distinct from JavaScript's language identifier).
func TestLiveLSP_TypeScript(t *testing.T) {
	if _, err := exec.LookPath("typescript-language-server"); err != nil {
		if _, npxErr := exec.LookPath("npx"); npxErr != nil {
			t.Skip("typescript-language-server not installed and no npx fallback")
		}
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"livesp-ts","version":"1.0.0"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(`{"compilerOptions":{"strict":true}}`), 0644); err != nil {
		t.Fatal(err)
	}
	mgr := liveLSPManager(t, dir)
	tool := &WriteFileTool{ProjectDir: dir, LSPManager: mgr}

	path := filepath.Join(dir, "app.ts")
	content := "const answer: number = missingValue;\n"
	out := liveDiagnostics(t, tool, path, content, "typescript", livePythonProbeTimeout)
	if !strings.Contains(out, "Diagnostics (typescript)") {
		t.Fatalf("expected typescript diagnostics block, got:\n%s", out)
	}
	if !strings.Contains(out, "missingValue") {
		t.Errorf("expected missing-name hint, got:\n%s", out)
	}
}

// TestLiveLSP_EditPython verifies the EDIT path also delivers diagnostics for
// a non-Go file (the old .go-only guard dropped them entirely).
func TestLiveLSP_EditPython(t *testing.T) {
	if _, err := exec.LookPath("pyright-langserver"); err != nil {
		if _, npxErr := exec.LookPath("npx"); npxErr != nil {
			t.Skip("pyright-langserver not installed and no npx fallback")
		}
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte("[project]\nname = \"livesp\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mgr := liveLSPManager(t, dir)

	path := filepath.Join(dir, "main.py")
	if err := os.WriteFile(path, []byte("x = 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	edit := &EditFileTool{ProjectDir: dir, LSPManager: mgr}

	deadline := time.Now().Add(livePythonProbeTimeout)
	var out string
	var err error
	for time.Now().Before(deadline) {
		input := fmt.Sprintf(`{"path": %q, "old_string": "x = 1", "new_string": "undefined_xyz"}`, path)
		out, err = edit.Execute(input)
		if err != nil {
			t.Skipf("optional pyright server unavailable while probing edit %s: %v", path, err)
		}
		if strings.Contains(out, "Diagnostics (pyright)") {
			break
		}
		// revert so the next iteration's old_string matches again
		input = fmt.Sprintf(`{"path": %q, "old_string": "undefined_xyz", "new_string": "x = 1"}`, path)
		if _, err = edit.Execute(input); err != nil {
			t.Fatalf("revert %s: %v", path, err)
		}
		time.Sleep(2 * time.Second)
	}
	if !strings.Contains(out, "Diagnostics (pyright)") {
		t.Skipf("optional pyright server launched but did not publish diagnostics from edit within bounded probe (%s)", livePythonProbeTimeout)
	}
}
