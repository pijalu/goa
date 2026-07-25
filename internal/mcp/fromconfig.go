// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package mcp

import (
	"path/filepath"
	"time"

	"github.com/pijalu/goa/config"
)

// FromConfig converts a config.MCPServerConfig (YAML form) into the runtime
// ServerConfig used to connect, resolving the working directory against
// projectDir and parsing the timeout. Shared by startup wiring and the /mcp
// commands so a server is always connected the same way regardless of whether
// it came from config load or an interactive /mcp:add.
func FromConfig(name, projectDir string, srv config.MCPServerConfig) ServerConfig {
	sc := ServerConfig{
		Name:    name,
		Type:    srv.Type,
		URL:     srv.URL,
		Headers: srv.Headers,
		Env:     srv.Environment,
		Timeout: parseTimeout(srv.Timeout),
	}
	if len(srv.Command) > 0 {
		sc.Command = srv.Command[0]
		sc.Args = srv.Command[1:]
	}
	sc.Cwd = resolveCwd(projectDir, srv.Cwd)
	return sc
}

// resolveCwd resolves a server's working directory: empty uses the project
// directory; relative paths resolve from it; absolute paths pass through.
func resolveCwd(projectDir, cwd string) string {
	if cwd == "" {
		return projectDir
	}
	if filepath.IsAbs(cwd) {
		return cwd
	}
	return filepath.Join(projectDir, cwd)
}

// parseTimeout parses a duration string, returning 0 (use default) when empty
// or invalid. Config validation already rejects bad values; this is defensive.
func parseTimeout(s string) time.Duration {
	if s == "" {
		return 0
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0
	}
	return d
}
