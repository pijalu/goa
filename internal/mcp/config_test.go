// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	data := `[{"name":"fs","command":"npx","args":["-y","@modelcontextprotocol/server-filesystem","/tmp"]}]`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	servers, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(servers) != 1 || servers[0].Name != "fs" {
		t.Errorf("servers = %v", servers)
	}
}

func TestLoadConfigMissing(t *testing.T) {
	servers, err := LoadConfig(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(servers) != 0 {
		t.Errorf("servers = %v", servers)
	}
}
