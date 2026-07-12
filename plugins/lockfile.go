// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// LockEntry records the installed state of a plugin.
type LockEntry struct {
	ID      string    `json:"id"`
	Source  string    `json:"source"`
	Version string    `json:"version,omitempty"`
	Hash    string    `json:"hash"`
	Enabled bool      `json:"enabled"`
	Updated time.Time `json:"updated"`
}

// Lockfile tracks installed plugins and their content hashes. All methods
// are safe for concurrent use.
type Lockfile struct {
	mu     sync.RWMutex
	path   string
	Plugins map[string]LockEntry `json:"plugins"`
}

// NewLockfile creates a lockfile for the given path.
func NewLockfile(path string) *Lockfile {
	return &Lockfile{
		path:    path,
		Plugins: make(map[string]LockEntry),
	}
}

// Load reads the lockfile from disk.
func (l *Lockfile) Load() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	data, err := os.ReadFile(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read lockfile: %w", err)
	}
	var parsed struct {
		Plugins map[string]LockEntry `json:"plugins"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return fmt.Errorf("parse lockfile: %w", err)
	}
	if parsed.Plugins != nil {
		l.Plugins = parsed.Plugins
	}
	return nil
}

// Save persists the lockfile to disk.
func (l *Lockfile) Save() error {
	l.mu.RLock()
	defer l.mu.RUnlock()
	data, err := json.MarshalIndent(struct {
		Plugins map[string]LockEntry `json:"plugins"`
	}{Plugins: l.Plugins}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return fmt.Errorf("mkdir lockfile: %w", err)
	}
	return os.WriteFile(l.path, data, 0o600)
}

// Get returns the entry for a plugin, if present.
func (l *Lockfile) Get(id string) (LockEntry, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	entry, ok := l.Plugins[id]
	return entry, ok
}

// Set records or updates an entry.
func (l *Lockfile) Set(entry LockEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.Plugins[entry.ID] = entry
}

// Remove deletes an entry.
func (l *Lockfile) Remove(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.Plugins, id)
}

// InstalledIDs returns sorted plugin IDs.
func (l *Lockfile) InstalledIDs() []string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	ids := make([]string, 0, len(l.Plugins))
	for id := range l.Plugins {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// hashPluginDir computes a SHA-256 content hash of a plugin directory by
// hashing all regular files below it, sorted by path. This is a simple
// integrity check, not a cryptographic guarantee.
func hashPluginDir(dir string) (string, error) {
	h := sha256.New()
	files, err := listFiles(dir)
	if err != nil {
		return "", fmt.Errorf("list files: %w", err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", f, err)
		}
		rel, _ := filepath.Rel(dir, f)
		_, _ = h.Write([]byte(rel + "\n"))
		_, _ = h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func listFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}
