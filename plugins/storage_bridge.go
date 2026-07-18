// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// StorageBridge persists per-plugin key/value data as a JSON document on
// disk. Values are JSON strings chosen by the plugin (OAuth tokens, cached
// quota snapshots); the bridge never interprets them.
//
// The file lives at <root>/<pluginID>/storage.json with 0600 permissions
// because it holds credentials. Writes are atomic (tmp + rename) so a crash
// mid-write cannot corrupt tokens.
type StorageBridge struct {
	mu  sync.Mutex
	dir string
}

// NewStorageBridge creates a storage rooted at dir/<pluginID>/.
func NewStorageBridge(root, pluginID string) (*StorageBridge, error) {
	dir := filepath.Join(root, pluginID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir plugin storage: %w", err)
	}
	return &StorageBridge{dir: dir}, nil
}

// path returns the storage file path.
func (s *StorageBridge) path() string {
	return filepath.Join(s.dir, "storage.json")
}

// Get returns the stored string for key, or "" when absent.
func (s *StorageBridge) Get(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.load()
	if err != nil {
		return ""
	}
	return data[key]
}

// Set stores value under key, flushing to disk atomically.
func (s *StorageBridge) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.load()
	if err != nil {
		data = map[string]string{}
	}
	data[key] = value
	return s.save(data)
}

// Delete removes key. Missing keys are not an error.
func (s *StorageBridge) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.load()
	if err != nil {
		return nil
	}
	if _, ok := data[key]; !ok {
		return nil
	}
	delete(data, key)
	return s.save(data)
}

// Keys returns all stored keys (unsorted — callers sort when needed).
func (s *StorageBridge) Keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.load()
	if err != nil {
		return nil
	}
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	return keys
}

// load reads the storage file. A missing file yields an empty map; a corrupt
// file is treated as empty so a plugin can recover by overwriting.
func (s *StorageBridge) load() (map[string]string, error) {
	raw, err := os.ReadFile(s.path())
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	data := map[string]string{}
	if err := json.Unmarshal(raw, &data); err != nil {
		return map[string]string{}, fmt.Errorf("corrupt storage: %w", err)
	}
	return data, nil
}

// save writes data atomically with 0600 permissions.
func (s *StorageBridge) save(data map[string]string) error {
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("encode storage: %w", err)
	}
	tmp := s.path() + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("write storage: %w", err)
	}
	if err := os.Rename(tmp, s.path()); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("commit storage: %w", err)
	}
	return nil
}
