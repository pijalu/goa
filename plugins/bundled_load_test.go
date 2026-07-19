// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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

// TestMaterializeBundled_RematerializesOnContentDrift is the regression for
// the stale-plugin bug: a dev build changed the embedded kimi fetcher but the
// on-disk materialization kept serving the old code because the fast path
// only checked plugin.yaml exists. Now, when the materialized content no
// longer matches the EMBEDDED source, MaterializeBundled must re-copy.
func TestMaterializeBundled_RematerializesOnContentDrift(t *testing.T) {
	root := t.TempDir()
	mgr, _ := NewManager(root, nil)
	src := BundledSource{ID: "provider-quota", Version: "1.0.0", Root: "bundled/provider-quota", ReadFile: bundledReadFile, ReadDir: bundledReadDir}
	target, err := mgr.MaterializeBundled(src)
	if err != nil {
		t.Fatal(err)
	}
	// Tamper the materialized copy on disk (simulates a stale or edited copy).
	kimiPath := filepath.Join(target, "fetchers", "kimi.js")
	if err := os.WriteFile(kimiPath, []byte("// stale old fetcher\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Fast path must detect the drift and re-materialize.
	if _, err := mgr.MaterializeBundled(src); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(kimiPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "// stale old fetcher\n" {
		t.Fatal("materialized copy not refreshed after content drift")
	}
	if !strings.Contains(string(data), "api.kimi.com") {
		t.Fatalf("re-materialized kimi fetcher missing real endpoint: %q", data[:80])
	}
}

// TestMaterializeBundled_RematerializesOnSourceChange covers the dev-build
// scenario exactly: the embedded source changes between runs while the
// lockfile and the on-disk copy remain mutually consistent (the blind spot of
// the hash-vs-lockfile check). The fast path must compare against the
// embedded source, not the lockfile.
func TestMaterializeBundled_RematerializesOnSourceChange(t *testing.T) {
	root := t.TempDir()
	mgr, _ := NewManager(root, nil)
	filesV1 := map[string]string{
		"plugin.yaml": "id: p\nname: P\nversion: 1.0.0\nentry: plugin.js\n",
		"plugin.js":   "// v1\n",
	}
	filesV2 := map[string]string{
		"plugin.yaml": "id: p\nname: P\nversion: 1.0.0\nentry: plugin.js\n",
		"plugin.js":   "// v2 — changed in dev build\n",
	}
	srcFor := func(files map[string]string) BundledSource {
		return BundledSource{
			ID: "p", Version: "1.0.0", Root: ".",
			ReadFile: func(name string) ([]byte, error) {
				data, ok := files[strings.TrimPrefix(name, "./")]
				if !ok {
					return nil, fs.ErrNotExist
				}
				return []byte(data), nil
			},
			ReadDir: func(name string) ([]fs.DirEntry, error) {
				var out []fs.DirEntry
				for k := range files {
					out = append(out, fakeDirEntry(k))
				}
				return out, nil
			},
		}
	}
	target, err := mgr.MaterializeBundled(srcFor(filesV1))
	if err != nil {
		t.Fatal(err)
	}
	// Same version, new embedded content → must re-materialize.
	if _, err := mgr.MaterializeBundled(srcFor(filesV2)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(target, "plugin.js"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "v2") {
		t.Fatalf("stale plugin served after embedded source change: %q", data)
	}
}

// fakeDirEntry implements fs.DirEntry for in-memory embed FS tests.
type fakeDirEntry string

func (f fakeDirEntry) Name() string               { return string(f) }
func (f fakeDirEntry) IsDir() bool                { return false }
func (f fakeDirEntry) Type() fs.FileMode          { return 0 }
func (f fakeDirEntry) Info() (fs.FileInfo, error) { return nil, nil }
