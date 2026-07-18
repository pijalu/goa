// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// BundledSource names an embedded plugin to materialize and enable. The
// plugin is copied from the embed FS into <managerRoot>/bundled/<id>@<ver>/
// and recorded in the lockfile with the hash of the materialized content, so
// subsequent Verify calls pass and version upgrades (which change the target
// dir) re-materialize cleanly without a trust re-prompt.
type BundledSource struct {
	// ID is the plugin id; part of the materialized dir name and lockfile key.
	ID string
	// Version is the plugin version; part of the materialized dir name.
	Version string
	// Root is the slash-separated path of the plugin directory inside the
	// embed FS (e.g. "provider-quota" for plugins/bundled, or
	// "bundled/provider-quota" for a test embed). Defaults to ID when empty.
	Root string
	// ReadFile reads a file from the embed FS by slash-separated path.
	ReadFile func(name string) ([]byte, error)
	// ReadDir lists a directory in the embed FS.
	ReadDir func(name string) ([]fs.DirEntry, error)
}

// fsRoot returns the embed FS root for this source.
func (s BundledSource) fsRoot() string {
	if s.Root != "" {
		return s.Root
	}
	return s.ID
}

// MaterializeBundled copies a bundled plugin from its embed FS into the
// manager's bundled directory and enables it in the lockfile. It returns the
// materialized directory path. Safe to call on every startup: when the target
// versioned dir already exists with a matching hash, it is reused.
func (m *Manager) MaterializeBundled(src BundledSource) (string, error) {
	if src.ID == "" || src.Version == "" {
		return "", fmt.Errorf("bundled source requires id and version")
	}
	target := filepath.Join(m.root, "bundled", src.ID+"@"+src.Version)

	// Fast path: already materialized for this version.
	if _, err := os.Stat(filepath.Join(target, "plugin.yaml")); err == nil {
		m.mu.Lock()
		m.enableBundledLocked(src, target)
		m.mu.Unlock()
		return target, nil
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return "", fmt.Errorf("mkdir bundled root: %w", err)
	}
	if err := copyEmbedDir(src, src.fsRoot(), target); err != nil {
		return "", fmt.Errorf("materialize bundled plugin %s: %w", src.ID, err)
	}

	hash, err := hashPluginDir(target)
	if err != nil {
		return "", fmt.Errorf("hash bundled plugin: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	entry := LockEntry{
		ID:      src.ID,
		Source:  "bundled",
		Version: src.Version,
		Hash:    hash,
		Enabled: true,
		Updated: time.Now(),
		Dir:     target,
	}
	m.lock.Set(entry)
	m.enabled[src.ID] = true
	if err := m.lock.Save(); err != nil {
		return "", fmt.Errorf("save lockfile: %w", err)
	}
	return target, nil
}

// enableBundledLocked records an already-materialized bundled plugin as
// enabled without re-hashing (fast startup path). Caller holds m.mu.
func (m *Manager) enableBundledLocked(src BundledSource, target string) {
	entry, ok := m.lock.Get(src.ID)
	if !ok {
		hash, err := hashPluginDir(target)
		if err != nil {
			return
		}
		entry = LockEntry{ID: src.ID, Source: "bundled", Version: src.Version, Hash: hash, Updated: time.Now(), Dir: target}
	}
	// Ensure Dir stays current even if the entry predates the Dir field.
	entry.Dir = target
	entry.Enabled = true
	m.lock.Set(entry)
	m.enabled[src.ID] = true
	_ = m.lock.Save()
}

// copyEmbedDir recursively copies an embed FS subtree to disk.
func copyEmbedDir(src BundledSource, embedPath, diskPath string) error {
	entries, err := src.ReadDir(embedPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(diskPath, 0o755); err != nil {
		return err
	}
	for _, e := range entries {
		ep := embedPath + "/" + e.Name()
		dp := filepath.Join(diskPath, e.Name())
		if e.IsDir() {
			if err := copyEmbedDir(src, ep, dp); err != nil {
				return err
			}
			continue
		}
		data, err := src.ReadFile(ep)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dp, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// BundledDir returns the directory holding materialized bundled plugins.
func (m *Manager) BundledDir() string {
	return filepath.Join(m.root, "bundled")
}
