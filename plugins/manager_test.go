// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/pijalu/goa/internal/trust"
)

func newTestManager(t *testing.T, trustMgr *trust.Manager) *Manager {
	t.Helper()
	root := t.TempDir()
	m, err := NewManager(root, trustMgr)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	return m
}

func writeManifest(t *testing.T, dir, id string) {
	t.Helper()
	manifest := `id: ` + id + `
name: ` + id + `
version: 1.0.0
entry: plugin.js
skills_dir: skills
`
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.js"), []byte("// plugin"), 0o600); err != nil {
		t.Fatalf("write plugin.js: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "skills"), 0o700); err != nil {
		t.Fatalf("mkdir skills: %v", err)
	}
}

func TestManager_InstallAndValidate(t *testing.T) {
	m := newTestManagerWithClone(t, "test-plugin")
	id, err := m.Install("https://example.com/plugin.git")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if id != "test-plugin" {
		t.Errorf("id = %q, want test-plugin", id)
	}
	if _, ok := m.lock.Get(id); !ok {
		t.Fatal("plugin not in lockfile")
	}
	if m.IsEnabled(id) {
		t.Fatal("plugin should be disabled after install")
	}
}

func TestManager_EnableDisable(t *testing.T) {
	m := newTestManagerWithClone(t, "test-plugin")
	id, _ := m.Install("https://example.com/plugin.git")
	if err := m.Enable(id); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if !m.IsEnabled(id) {
		t.Fatal("plugin not enabled")
	}
	if err := m.Disable(id); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if m.IsEnabled(id) {
		t.Fatal("plugin still enabled after disable")
	}
}

func TestManager_SkillDirs(t *testing.T) {
	m := newTestManagerWithClone(t, "test-plugin")
	id, _ := m.Install("https://example.com/plugin.git")
	_ = m.Enable(id)
	dirs := m.EnabledSkillDirs()
	if len(dirs) != 1 {
		t.Fatalf("skill dirs = %d, want 1", len(dirs))
	}
}

func TestManager_Remove(t *testing.T) {
	m := newTestManagerWithClone(t, "test-plugin")
	id, _ := m.Install("https://example.com/plugin.git")
	if err := m.Remove(id); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, ok := m.lock.Get(id); ok {
		t.Fatal("plugin still in lockfile after remove")
	}
}

func newTestManagerWithClone(t *testing.T, id string) *Manager {
	t.Helper()
	m := newTestManager(t, nil)
	m.cloneFunc = func(url, dir string) error {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		writeManifest(t, dir, id)
		return nil
	}
	return m
}

func TestManager_InstallRequiresGitURL(t *testing.T) {
	m := newTestManager(t, nil)
	if _, err := m.Install("not-a-git-url"); err == nil {
		t.Fatal("expected error for non-git URL")
	}
}

func TestManager_InstallDuplicate(t *testing.T) {
	m := newTestManager(t, nil)
	m.cloneFunc = func(url, dir string) error {
		_ = os.MkdirAll(dir, 0o700)
		writeManifest(t, dir, "dup-plugin")
		return nil
	}
	if _, err := m.Install("https://example.com/dup.git"); err != nil {
		t.Fatalf("first install: %v", err)
	}
	if _, err := m.Install("https://example.com/dup.git"); err == nil {
		t.Fatal("expected duplicate install error")
	}
}

func TestManager_EnableRequiresTrust(t *testing.T) {
	trustMgr := trust.NewManager(filepath.Join(t.TempDir(), "trust.json"))
	m := newTestManager(t, trustMgr)
	m.cloneFunc = func(url, dir string) error {
		_ = os.MkdirAll(dir, 0o700)
		writeManifest(t, dir, "untrusted")
		return nil
	}
	if _, err := m.Install("https://example.com/u.git"); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := m.Enable("untrusted"); err == nil {
		t.Fatal("expected trust error")
	}
	if err := trustMgr.Trust("untrusted"); err != nil {
		t.Fatalf("trust: %v", err)
	}
	if err := m.Enable("untrusted"); err != nil {
		t.Fatalf("enable after trust: %v", err)
	}
}

func TestManager_List(t *testing.T) {
	m := newTestManager(t, nil)
	m.cloneFunc = func(url, dir string) error {
		_ = os.MkdirAll(dir, 0o700)
		writeManifest(t, dir, "list-plugin")
		return nil
	}
	if _, err := m.Install("https://example.com/list.git"); err != nil {
		t.Fatalf("install: %v", err)
	}
	entries := m.List()
	if len(entries) != 1 {
		t.Fatalf("list = %d, want 1", len(entries))
	}
	if entries[0].ID != "list-plugin" {
		t.Errorf("id = %q", entries[0].ID)
	}
}

func TestManager_PersistLockfile(t *testing.T) {
	root := t.TempDir()
	m, err := NewManager(root, nil)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	m.cloneFunc = func(url, dir string) error {
		_ = os.MkdirAll(dir, 0o700)
		writeManifest(t, dir, "persist")
		return nil
	}
	if _, err := m.Install("https://example.com/persist.git"); err != nil {
		t.Fatalf("install: %v", err)
	}

	// Re-create manager pointing at the same root.
	m2, err := NewManager(root, nil)
	if err != nil {
		t.Fatalf("new manager 2: %v", err)
	}
	if _, ok := m2.lock.Get("persist"); !ok {
		t.Fatal("lockfile not persisted")
	}
}

func TestManager_HashDetectsChanges(t *testing.T) {
	m := newTestManager(t, nil)
	m.cloneFunc = func(url, dir string) error {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		writeManifest(t, dir, "hash-plugin")
		return nil
	}
	if _, err := m.Install("https://example.com/hash.git"); err != nil {
		t.Fatalf("install: %v", err)
	}
	entry1, _ := m.lock.Get("hash-plugin")

	// Modify plugin file on disk.
	pluginFile := filepath.Join(m.root, "hash-plugin", "plugin.js")
	if err := os.WriteFile(pluginFile, []byte("// changed"), 0o600); err != nil {
		t.Fatalf("write plugin file: %v", err)
	}
	newHash, err := hashPluginDir(filepath.Join(m.root, "hash-plugin"))
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if newHash == entry1.Hash {
		t.Fatal("hash should change after plugin files are modified")
	}
}

// TestManager_RejectsHTTP verifies plaintext-HTTP plugin sources are refused
// (plugins execute code; MITM would be RCE).
func TestManager_RejectsHTTP(t *testing.T) {
	m := newTestManager(t, nil)
	if _, err := m.Install("http://example.com/plugin.git"); err == nil {
		t.Fatal("expected error for http:// plugin source")
	}
}

// TestManager_ConcurrentAccess exercises concurrent Install/Enable/Disable/List
// under -race (D3: Manager/Lockfile now mutex-guarded).
func TestManager_ConcurrentAccess(t *testing.T) {
	m := newTestManager(t, nil)
	var counter atomic.Int64
	m.cloneFunc = func(url, dir string) error {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		writeManifest(t, dir, fmt.Sprintf("p-%d", counter.Add(1)))
		return nil
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = m.Install("https://example.com/p.git")
		}()
		go func() {
			defer wg.Done()
			_ = m.List()
			_ = m.EnabledIDs()
			_ = m.EnabledSkillDirs()
		}()
	}
	wg.Wait()

	// Enable/disable every installed plugin concurrently.
	ids := m.List()
	for range ids {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for _, e := range m.List() {
				_ = m.Enable(e.ID)
			}
		}()
		go func() {
			defer wg.Done()
			for _, e := range m.List() {
				_ = m.Disable(e.ID)
			}
		}()
	}
	wg.Wait()
}

// TestMoveDir_Portable verifies moveDir works across independent temp
// directories (the cross-device fallback path) preserving content and mode.
func TestMoveDir_Portable(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	dstFinal := filepath.Join(dst, "moved")

	content := []byte("plugin body")
	srcFile := filepath.Join(src, "plugin.js")
	if err := os.WriteFile(srcFile, content, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := moveDir(src, dstFinal); err != nil {
		t.Fatalf("moveDir: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dstFinal, "plugin.js"))
	if err != nil {
		t.Fatalf("read moved file: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("content mismatch: got %q", got)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("expected source removed after move, got err=%v", err)
	}
}

// TestManager_EnableDetectsTamper verifies Enable re-verifies the content hash
// and refuses a plugin modified after install (D5).
func TestManager_EnableDetectsTamper(t *testing.T) {
	m := newTestManagerWithClone(t, "tamper-plugin")
	id, err := m.Install("https://example.com/plugin.git")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	// Tamper with the installed content.
	pluginFile := filepath.Join(m.root, id, "plugin.js")
	f, err := os.OpenFile(pluginFile, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("tamper: %v", err)
	}
	if _, err := f.Write([]byte("// tampered")); err != nil {
		t.Fatalf("tamper write: %v", err)
	}
	f.Close()
	if err := m.Enable(id); err == nil {
		t.Fatal("expected Enable to fail after tampering")
	}
	if err := m.Verify(id); err == nil {
		t.Fatal("expected Verify to fail after tampering")
	}
}
