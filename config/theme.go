// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Theme defines named color and style overrides.
type Theme struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Colors      map[string]string `json:"colors"`
}

// DefaultTheme returns the built-in default theme.
func DefaultTheme() *Theme {
	return &Theme{
		Name:        "default",
		Description: "Default Goa colors",
		Colors: map[string]string{
			"primary":   "cyan",
			"success":   "green",
			"warning":   "yellow",
			"error":     "red",
			"muted":     "gray",
			"highlight": "magenta",
		},
	}
}

// ThemeStore loads and saves user themes.
type ThemeStore struct {
	mu     sync.RWMutex
	dir    string
	active *Theme
}

// NewThemeStore creates a theme store.
func NewThemeStore(dir string) *ThemeStore {
	s := &ThemeStore{dir: dir, active: DefaultTheme()}
	_, _ = s.Load("default")
	return s
}

// Active returns the current theme.
func (s *ThemeStore) Active() *Theme {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.active
}

// SetActive changes the active theme by loading from disk or falling back to default.
func (s *ThemeStore) SetActive(name string) error {
	t, err := s.Load(name)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.active = t
	s.mu.Unlock()
	return nil
}

// Load reads a theme from disk.
func (s *ThemeStore) Load(name string) (*Theme, error) {
	if name == "default" {
		return DefaultTheme(), nil
	}
	s.mu.RLock()
	path := filepath.Join(s.dir, name+".json")
	s.mu.RUnlock()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultTheme(), nil
		}
		return nil, fmt.Errorf("read theme: %w", err)
	}
	var t Theme
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("parse theme: %w", err)
	}
	return &t, nil
}

// Save persists a theme.
func (s *ThemeStore) Save(t *Theme) error {
	if t.Name == "" {
		return fmt.Errorf("theme name required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("mkdir themes: %w", err)
	}
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.dir, t.Name+".json"), data, 0o644)
}

// Dir returns the themes directory.
func (s *ThemeStore) Dir() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.dir
}

// List returns available theme names.
func (s *ThemeStore) List() ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{"default"}, nil
		}
		return nil, err
	}
	names := []string{"default"}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if filepath.Ext(e.Name()) == ".json" {
			names = append(names, e.Name()[:len(e.Name())-len(".json")])
		}
	}
	return names, nil
}
