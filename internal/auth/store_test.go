// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	oauth "github.com/pijalu/goa/internal/agentic/provider/oauth"
)

func TestStore_EncryptsOAuthTokens(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	store := NewStore(path)

	tokens := &oauth.Tokens{
		AccessToken: "secret-token",
		TokenType:   "bearer",
		ExpiresAt:   time.Now().Add(time.Hour),
	}
	if err := store.SetOAuth("copilot", tokens); err != nil {
		t.Fatalf("set: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read store: %v", err)
	}
	if string(data) == "" {
		t.Fatal("store file is empty")
	}
	if contains(string(data), "secret-token") {
		t.Fatal("token stored in plaintext")
	}

	store2 := NewStore(path)
	got, ok := store2.GetOAuth("copilot")
	if !ok {
		t.Fatal("token not found after reload")
	}
	if got.AccessToken != "secret-token" {
		t.Errorf("token mismatch: got %q", got.AccessToken)
	}
}

func TestStore_EncryptsAPIKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	store := NewStore(path)

	if err := store.SetAPIKey("kimi", "sk-abc123"); err != nil {
		t.Fatalf("set api key: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read store: %v", err)
	}
	if contains(string(data), "sk-abc123") {
		t.Fatal("api key stored in plaintext")
	}

	got, ok := store.GetAPIKey("kimi")
	if !ok {
		t.Fatal("api key not found after reload")
	}
	if got != "sk-abc123" {
		t.Errorf("api key mismatch: got %q", got)
	}
}

func TestStore_LegacyOAuthMigration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	legacy := map[string]*oauth.Tokens{
		"copilot": {AccessToken: "legacy-token", TokenType: "bearer"},
	}
	data, _ := json.Marshal(legacy)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	store := NewStore(path)
	got, ok := store.GetOAuth("copilot")
	if !ok {
		t.Fatal("legacy token not found")
	}
	if got.AccessToken != "legacy-token" {
		t.Errorf("legacy token mismatch: got %q", got.AccessToken)
	}

	if err := store.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	stored, _ := os.ReadFile(path)
	if contains(string(stored), "legacy-token") {
		t.Fatal("legacy token still in plaintext after migration")
	}
}

func TestStore_Delete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	store := NewStore(path)
	_ = store.SetAPIKey("copilot", "x")
	if err := store.Delete("copilot"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := store.Get("copilot"); ok {
		t.Fatal("credential still present after delete")
	}
}

func TestStore_OverwriteOAuthWithAPIKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	store := NewStore(path)
	_ = store.SetOAuth("kimi", &oauth.Tokens{AccessToken: "tok"})
	_ = store.SetAPIKey("kimi", "key")

	c, ok := store.Get("kimi")
	if !ok {
		t.Fatal("credential missing")
	}
	if !c.IsAPIKey() {
		t.Fatal("expected api key credential")
	}
	if _, ok := store.GetOAuth("kimi"); ok {
		t.Fatal("oauth still present after overwrite")
	}
}

func contains(s, substr string) bool { return len(s) >= len(substr) && (s == substr || len(substr) == 0 || findSubstr(s, substr)) }

func findSubstr(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
