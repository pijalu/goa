// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// DefaultTimeout bounds an MCP request when the server config does not set one.
const DefaultTimeout = 30 * time.Second

// ServerConfig describes how to connect to one MCP server. It is the
// transport-agnostic runtime form; see config.MCPServerConfig for the YAML
// representation.
type ServerConfig struct {
	Name string `json:"name"`
	// Type is "local" (stdio) or "remote" (HTTP/SSE). Empty defaults to local
	// when Command is set.
	Type string `json:"type,omitempty"`
	// Command is the executable for a local server.
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	// Env is merged over the process environment for a local server.
	Env map[string]string `json:"env,omitempty"`
	// Cwd is the working directory for a local server (already resolved).
	Cwd string `json:"cwd,omitempty"`
	// URL is the endpoint of a remote server.
	URL string `json:"url,omitempty"`
	// Headers are sent with every request to a remote server.
	Headers map[string]string `json:"headers,omitempty"`
	// Timeout bounds each MCP request. Zero uses DefaultTimeout.
	Timeout time.Duration `json:"timeout,omitempty"`
}

// IsRemote reports whether this is a remote (HTTP/SSE) server.
func (c ServerConfig) IsRemote() bool { return c.Type == "remote" }

// EffectiveTimeout returns the configured timeout or DefaultTimeout.
func (c ServerConfig) EffectiveTimeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return DefaultTimeout
}

// LoadConfig reads MCP server definitions from a JSON file.
//
// Deprecated: MCP servers are configured via the YAML cascade (mcp: section).
// This JSON loader remains for backwards compatibility with early adopters.
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
//
// Deprecated: see LoadConfig.
func ConfigPaths(projectDir, configDir string) []string {
	return []string{
		filepath.Join(projectDir, ".goa", "mcp.json"),
		filepath.Join(configDir, "mcp.json"),
	}
}
