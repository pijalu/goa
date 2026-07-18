// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// testBundledFS embeds the real provider-quota plugin for materialization
// tests, mirroring how plugins/bundled exposes it to the app layer.
//
//go:embed bundled/provider-quota
var testBundledFS embed.FS

func bundledReadFile(name string) ([]byte, error) { return testBundledFS.ReadFile(name) }
func bundledReadDir(name string) ([]fs.DirEntry, error) {
	return testBundledFS.ReadDir(name)
}

// TestMaterializeBundled uses the real embedded provider-quota plugin.
func TestMaterializeBundled_RealPlugin(t *testing.T) {
	root := t.TempDir()
	mgr, err := NewManager(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	src := BundledSource{
		ID:       "provider-quota",
		Version:  "1.0.0",
		Root:     "bundled/provider-quota",
		ReadFile: bundledReadFile,
		ReadDir:  bundledReadDir,
	}
	target, err := mgr.MaterializeBundled(src)
	if err != nil {
		t.Fatalf("MaterializeBundled: %v", err)
	}
	// Materialized under bundled/<id>@<version>/ with plugin.yaml + plugin.js.
	if _, err := os.Stat(filepath.Join(target, "plugin.yaml")); err != nil {
		t.Fatalf("plugin.yaml missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "plugin.js")); err != nil {
		t.Fatalf("plugin.js missing: %v", err)
	}
	// Enabled + verifiable in the lockfile.
	if !mgr.IsEnabled("provider-quota") {
		t.Fatal("bundled plugin not enabled")
	}
	if err := mgr.Verify("provider-quota"); err != nil {
		t.Fatalf("Verify after materialize: %v", err)
	}
}

// TestMaterializeBundled_Idempotent confirms the fast path reuses the dir.
func TestMaterializeBundled_Idempotent(t *testing.T) {
	root := t.TempDir()
	mgr, _ := NewManager(root, nil)
	src := BundledSource{ID: "provider-quota", Version: "1.0.0", Root: "bundled/provider-quota", ReadFile: bundledReadFile, ReadDir: bundledReadDir}
	t1, err := mgr.MaterializeBundled(src)
	if err != nil {
		t.Fatal(err)
	}
	t2, err := mgr.MaterializeBundled(src)
	if err != nil {
		t.Fatal(err)
	}
	if t1 != t2 {
		t.Fatalf("idempotent materialize returned different dirs: %q vs %q", t1, t2)
	}
	if !mgr.IsEnabled("provider-quota") {
		t.Fatal("not enabled after re-materialize")
	}
}

// TestMaterializeBundled_RequiresIDAndVersion validates inputs.
func TestMaterializeBundled_RequiresIDAndVersion(t *testing.T) {
	mgr, _ := NewManager(t.TempDir(), nil)
	if _, err := mgr.MaterializeBundled(BundledSource{ID: "", Version: "1"}); err == nil {
		t.Fatal("expected error for empty id")
	}
	if _, err := mgr.MaterializeBundled(BundledSource{ID: "x", Version: ""}); err == nil {
		t.Fatal("expected error for empty version")
	}
}
