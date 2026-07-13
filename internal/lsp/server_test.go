// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package lsp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServer_StartAndClose(t *testing.T) {
	bindir := t.TempDir()
	script := filepath.Join(bindir, "gopls")
	content := `#!/bin/sh
# Minimal fake gopls: read one initialize request and respond, then exit.
read -r line
read -r line
read -r body
printf 'Content-Length: 78\r\n\r\n{"jsonrpc":"2.0","id":1,"result":{"capabilities":{"definitionProvider":true}}}'\n`
	if err := os.WriteFile(script, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	server, err := Start(ctx, ServerConfig{Command: script, Args: []string{}})
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if server.Client() == nil {
		t.Fatal("expected client")
	}
	if err := server.Close(ctx); err != nil {
		t.Errorf("close failed: %v", err)
	}
}

func TestServer_CommandNotFound(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := Start(ctx, ServerConfig{Command: "/nonexistent/gopls", Args: []string{}})
	if err == nil {
		t.Fatal("expected error for missing command")
	}
	if !contains(err.Error(), "nonexistent") {
		t.Errorf("unexpected error: %v", err)
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0) }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestServer_StartWithEnv(t *testing.T) {
	bindir := t.TempDir()
	script := filepath.Join(bindir, "gopls")
	content := `#!/bin/sh
read -r line
read -r line
read -r body
printf 'Content-Length: 78\r\n\r\n{"jsonrpc":"2.0","id":1,"result":{"capabilities":{"definitionProvider":true}}}'\n`
	if err := os.WriteFile(script, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	server, err := Start(ctx, ServerConfig{Command: script, Args: []string{}, Env: []string{"PATH=/usr/bin"}})
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if err := server.Close(ctx); err != nil {
		t.Errorf("close failed: %v", err)
	}
}
