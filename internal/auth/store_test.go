// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package auth

import (
	"path/filepath"
	"testing"

	oauth "github.com/pijalu/goa/internal/agentic/provider/oauth"
)

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "tokens.json"))
	tokens := &oauth.Tokens{AccessToken: "abc", TokenType: "bearer"}
	if err := s.Set("github", tokens); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, ok := s.Get("github")
	if !ok || got.AccessToken != "abc" {
		t.Errorf("tokens = %+v", got)
	}
}

func TestStorePersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tokens.json")
	s := NewStore(path)
	_ = s.Set("x", &oauth.Tokens{AccessToken: "tok"})

	s2 := NewStore(path)
	if got, ok := s2.Get("x"); !ok || got.AccessToken != "tok" {
		t.Errorf("persisted tokens = %+v", got)
	}
}
