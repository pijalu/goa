// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	oauth "github.com/pijalu/goa/internal/agentic/provider/oauth"
)

// Store persists OAuth tokens per provider.
type Store struct {
	mu     sync.RWMutex
	path   string
	tokens map[string]*oauth.Tokens
}

// NewStore creates a token store at the given path.
func NewStore(path string) *Store {
	s := &Store{
		path:   path,
		tokens: make(map[string]*oauth.Tokens),
	}
	_ = s.Load()
	return s
}

// Load reads tokens from disk.
func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read auth store: %w", err)
	}
	var store map[string]*oauth.Tokens
	if err := json.Unmarshal(data, &store); err != nil {
		return fmt.Errorf("parse auth store: %w", err)
	}
	if store != nil {
		s.tokens = store
	}
	return nil
}

// Save persists tokens to disk.
func (s *Store) Save() error {
	s.mu.RLock()
	data, err := json.MarshalIndent(s.tokens, "", "  ")
	s.mu.RUnlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("mkdir auth store: %w", err)
	}
	return os.WriteFile(s.path, data, 0o600)
}

// Get returns tokens for a provider, if any.
func (s *Store) Get(provider string) (*oauth.Tokens, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tokens[provider]
	return t, ok
}

// Set stores tokens for a provider.
func (s *Store) Set(provider string, tokens *oauth.Tokens) error {
	s.mu.Lock()
	s.tokens[provider] = tokens
	s.mu.Unlock()
	return s.Save()
}

// Delete removes tokens for a provider.
func (s *Store) Delete(provider string) error {
	s.mu.Lock()
	delete(s.tokens, provider)
	s.mu.Unlock()
	return s.Save()
}

// Providers returns all stored provider names.
func (s *Store) Providers() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.tokens))
	for p := range s.tokens {
		out = append(out, p)
	}
	return out
}
