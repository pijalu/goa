// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ServerConfig describes how to connect to one MCP server.
type ServerConfig struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// LoadConfig reads MCP server definitions from a JSON file.
func LoadConfig(path string) ([]ServerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read mcp config: %w", err)
	}
	var servers []ServerConfig
	if err := json.Unmarshal(data, &servers); err != nil {
		return nil, fmt.Errorf("parse mcp config: %w", err)
	}
	return servers, nil
}

// ConfigPaths returns candidate config paths.
func ConfigPaths(projectDir, configDir string) []string {
	return []string{
		filepath.Join(projectDir, ".goa", "mcp.json"),
		filepath.Join(configDir, "mcp.json"),
	}
}
