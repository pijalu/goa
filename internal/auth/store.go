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

// Store persists provider credentials, encrypted at rest with AES-GCM.
// A random 32-byte key is stored alongside the token file with restricted
// file permissions (0600). The key file itself is protected by OS permissions,
// which provides encryption at rest without requiring a user password.
type Store struct {
	mu      sync.RWMutex
	path    string
	keyPath string
	key     []byte
	creds   map[string]Credential
}

// NewStore creates a credential store at the given path. The encryption key is
// stored next to the token file with a `.key` suffix.
func NewStore(path string) *Store {
	s := &Store{
		path:    path,
		keyPath: path + ".key",
		creds:   make(map[string]Credential),
	}
	_ = s.loadKey()
	_ = s.Load()
	return s
}

// loadKey loads or generates the AES encryption key.
func (s *Store) loadKey() error {
	data, err := os.ReadFile(s.keyPath)
	if err == nil {
		key, err := decodeKey(string(data))
		if err != nil {
			return fmt.Errorf("decode key: %w", err)
		}
		s.key = key
		return nil
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("read key: %w", err)
	}
	key, err := generateKey()
	if err != nil {
		return fmt.Errorf("generate key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.keyPath), 0o700); err != nil {
		return fmt.Errorf("mkdir key: %w", err)
	}
	if err := os.WriteFile(s.keyPath, []byte(encodeKey(key)), 0o600); err != nil {
		return fmt.Errorf("write key: %w", err)
	}
	s.key = key
	return nil
}

// Load reads credentials from disk and decrypts them.
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

	var store map[string]string
	if err := json.Unmarshal(data, &store); err != nil {
		return s.loadLegacy(data, err)
	}
	return s.loadEncrypted(store)
}

func (s *Store) loadLegacy(data []byte, parseErr error) error {
	var legacyTokens map[string]*oauth.Tokens
	if lerr := json.Unmarshal(data, &legacyTokens); lerr == nil && legacyTokens != nil {
		s.creds = make(map[string]Credential, len(legacyTokens))
		for provider, tokens := range legacyTokens {
			s.creds[provider] = NewOAuthCredential(provider, tokens)
		}
		return nil
	}
	var legacyCreds map[string]Credential
	if lerr := json.Unmarshal(data, &legacyCreds); lerr == nil && legacyCreds != nil {
		s.creds = legacyCreds
		return nil
	}
	return fmt.Errorf("parse auth store: %w", parseErr)
}

func (s *Store) loadEncrypted(store map[string]string) error {
	if s.key == nil {
		if err := s.loadKey(); err != nil {
			return fmt.Errorf("load key: %w", err)
		}
	}

	s.creds = make(map[string]Credential, len(store))
	for provider, ciphertext := range store {
		plain, err := decrypt(s.key, ciphertext)
		if err != nil {
			return fmt.Errorf("decrypt %s: %w", provider, err)
		}
		var cred Credential
		if err := json.Unmarshal(plain, &cred); err != nil {
			return fmt.Errorf("parse credential %s: %w", provider, err)
		}
		s.creds[provider] = cred
	}
	return nil
}

// Save persists encrypted credentials to disk.
func (s *Store) Save() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.key == nil {
		if err := s.loadKey(); err != nil {
			return fmt.Errorf("load key: %w", err)
		}
	}

	store := make(map[string]string, len(s.creds))
	for provider, cred := range s.creds {
		plain, err := json.Marshal(cred)
		if err != nil {
			return fmt.Errorf("marshal credential %s: %w", provider, err)
		}
		ciphertext, err := encrypt(s.key, plain)
		if err != nil {
			return fmt.Errorf("encrypt credential %s: %w", provider, err)
		}
		store[provider] = ciphertext
	}

	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("mkdir auth store: %w", err)
	}
	return os.WriteFile(s.path, data, 0o600)
}

// Get returns the credential for a provider, if any.
func (s *Store) Get(provider string) (Credential, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.creds[provider]
	return c, ok
}

// GetOAuth returns OAuth tokens for a provider, if present.
func (s *Store) GetOAuth(provider string) (*oauth.Tokens, bool) {
	c, ok := s.Get(provider)
	if !ok || !c.IsOAuth() {
		return nil, false
	}
	return c.Tokens, true
}

// GetAPIKey returns an API key for a provider, if present.
func (s *Store) GetAPIKey(provider string) (string, bool) {
	c, ok := s.Get(provider)
	if !ok || !c.IsAPIKey() {
		return "", false
	}
	return c.APISecret, true
}

// Set stores a credential for a provider.
func (s *Store) Set(provider string, cred Credential) error {
	s.mu.Lock()
	s.creds[provider] = cred
	s.mu.Unlock()
	return s.Save()
}

// SetOAuth stores OAuth tokens for a provider.
func (s *Store) SetOAuth(provider string, tokens *oauth.Tokens) error {
	return s.Set(provider, NewOAuthCredential(provider, tokens))
}

// SetAPIKey stores an API key for a provider.
func (s *Store) SetAPIKey(provider, key string) error {
	return s.Set(provider, NewAPIKeyCredential(provider, key))
}

// Delete removes the credential for a provider.
func (s *Store) Delete(provider string) error {
	s.mu.Lock()
	delete(s.creds, provider)
	s.mu.Unlock()
	return s.Save()
}

// Providers returns all stored provider names.
func (s *Store) Providers() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.creds))
	for p := range s.creds {
		out = append(out, p)
	}
	return out
}

// HasAuth returns true if the store has any OAuth or API key for the provider.
func (s *Store) HasAuth(provider string) bool {
	_, ok := s.Get(provider)
	return ok
}
