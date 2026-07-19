// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"bytes"
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
// versioned dir already exists AND its content hash still matches the
// lockfile entry, it is reused; otherwise it is re-materialized so an
// embedded-content change (dev builds, patched plugins) never runs stale
// code silently.
func (m *Manager) MaterializeBundled(src BundledSource) (string, error) {
	if src.ID == "" || src.Version == "" {
		return "", fmt.Errorf("bundled source requires id and version")
	}
	target := filepath.Join(m.root, "bundled", src.ID+"@"+src.Version)

	// Fast path: already materialized for this version with intact content
	// matching the embedded source.
	if _, err := os.Stat(filepath.Join(target, "plugin.yaml")); err == nil {
		if m.bundledContentIntact(src, target) {
			m.mu.Lock()
			m.enableBundledLocked(src, target)
			m.mu.Unlock()
			return target, nil
		}
		// Content drift (stale or tampered copy): fall through and
		// re-materialize from the trusted embedded source.
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return "", fmt.Errorf("mkdir bundled root: %w", err)
	}
	// Clear any previous materialization so deleted files do not linger.
	if err := os.RemoveAll(target); err != nil {
		return "", fmt.Errorf("reset bundled plugin dir: %w", err)
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

// bundledContentIntact reports whether the on-disk materialized copy still
// matches the EMBEDDED source content. The copy is reused only when every
// embedded file exists on disk with identical content and no extra files
// linger; otherwise the copy is stale (older embedded build) or was modified,
// and must be re-materialized. This is what lets dev builds ship plugin
// changes without a version bump.
func (m *Manager) bundledContentIntact(src BundledSource, target string) bool {
	match, err := embedDirMatches(src, src.fsRoot(), target)
	return err == nil && match
}

// embedDirMatches reports whether the directory tree at diskPath is byte-for-
// byte identical to the embed FS subtree at embedPath (same files, same
// contents, same set — extras on disk count as drift).
func embedDirMatches(src BundledSource, embedPath, diskPath string) (bool, error) {
	entries, err := src.ReadDir(embedPath)
	if err != nil {
		return false, err
	}
	diskEntries, err := os.ReadDir(diskPath)
	if err != nil {
		return false, err
	}
	if len(entries) != len(diskEntries) {
		return false, nil
	}
	diskNames := make(map[string]bool, len(diskEntries))
	for _, de := range diskEntries {
		diskNames[de.Name()] = true
	}
	for _, e := range entries {
		ok, err := embedEntryMatches(src, e, embedPath, diskPath, diskNames)
		if err != nil || !ok {
			return ok, err
		}
	}
	return true, nil
}

// embedEntryMatches compares one embed FS entry (file or dir) against its
// on-disk counterpart.
func embedEntryMatches(src BundledSource, e fs.DirEntry, embedPath, diskPath string, diskNames map[string]bool) (bool, error) {
	if !diskNames[e.Name()] {
		return false, nil
	}
	ep := embedPath + "/" + e.Name()
	dp := filepath.Join(diskPath, e.Name())
	if e.IsDir() {
		return embedDirMatches(src, ep, dp)
	}
	want, err := src.ReadFile(ep)
	if err != nil {
		return false, err
	}
	got, err := os.ReadFile(dp)
	if err != nil || !bytes.Equal(want, got) {
		return false, nil
	}
	return true, nil
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
