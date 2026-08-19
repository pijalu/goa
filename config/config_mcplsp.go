// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import "gopkg.in/yaml.v3"

const (
	// MCPTypeLocal is a stdio MCP server spawned as a local child process.
	MCPTypeLocal = "local"
	// MCPTypeRemote is an HTTP/SSE MCP server reached over the network.
	MCPTypeRemote = "remote"
)

// MCPServerConfig describes how Goa (as an MCP client) connects to one MCP
// server. Exactly one of Command (local) or URL (remote) applies, discriminated
// by Type. ${VAR} and ${VAR:-default} in Environment/Headers are expanded by
// the config loader.
type MCPServerConfig struct {
	// Type selects the transport: "local" (stdio child process) or "remote" (HTTP/SSE).
	Type string `yaml:"type" json:"type"`
	// Command is the argv for a local server; Command[0] is the executable.
	Command []string `yaml:"command,omitempty" json:"command,omitempty"`
	// Cwd is the working directory for a local server. Relative paths resolve
	// from the project directory. Empty uses the project directory.
	Cwd string `yaml:"cwd,omitempty" json:"cwd,omitempty"`
	// Environment is merged over the process environment for a local server.
	Environment map[string]string `yaml:"environment,omitempty" json:"environment,omitempty"`
	// URL is the endpoint of a remote server.
	URL string `yaml:"url,omitempty" json:"url,omitempty"`
	// Headers are sent with every request to a remote server.
	Headers map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	// Enabled toggles the server at startup. nil means enabled (tri-state).
	Enabled *bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	// Timeout bounds each MCP request (e.g. "30s"). Empty uses the default.
	Timeout string `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	// OAuth configures OAuth authentication for a remote server. Nil uses
	// auto-discovery when the server requires it.
	OAuth *MCPOAuthConfig `yaml:"oauth,omitempty" json:"oauth,omitempty"`
}

// MCPOAuthConfig configures OAuth for a remote MCP server.
type MCPOAuthConfig struct {
	// ClientID is a pre-registered OAuth client ID. Empty triggers dynamic
	// client registration (RFC 7591).
	ClientID string `yaml:"clientId,omitempty" json:"clientId,omitempty"`
	// ClientSecret is the OAuth client secret, if the authorization server requires one.
	ClientSecret string `yaml:"clientSecret,omitempty" json:"clientSecret,omitempty"`
	// Scope is the OAuth scope to request.
	Scope string `yaml:"scope,omitempty" json:"scope,omitempty"`
	// CallbackPort is the local OAuth callback listener port (default 19876).
	CallbackPort int `yaml:"callbackPort,omitempty" json:"callbackPort,omitempty"`
	// RedirectURI overrides the OAuth redirect URI (default http://127.0.0.1:<port>/mcp/oauth/callback).
	RedirectURI string `yaml:"redirectUri,omitempty" json:"redirectUri,omitempty"`
	// Disabled turns off OAuth auto-detection for this server.
	Disabled bool `yaml:"disabled,omitempty" json:"disabled,omitempty"`
}

// IsEnabled reports whether the server should connect at startup. A nil
// Enabled pointer means enabled (the default).
func (m MCPServerConfig) IsEnabled() bool { return m.Enabled == nil || *m.Enabled }

// LSPServerConfig mirrors OpenCode's per-server lsp config entry
// (packages/core/src/config/lsp.ts). A server may be disabled, or customized
// with its own command/extensions/env/initialization. When ID matches a
// builtin server the entry overrides that server; otherwise it defines a new
// custom server.
type LSPServerConfig struct {
	// Command is the argv to launch the server (Command[0] is the executable).
	Command []string `yaml:"command,omitempty" json:"command,omitempty"`
	// Extensions overrides the file extensions the server handles.
	Extensions []string `yaml:"extensions,omitempty" json:"extensions,omitempty"`
	// Disabled removes this server from the active set.
	Disabled bool `yaml:"disabled,omitempty" json:"disabled,omitempty"`
	// Env is merged over the process environment for the server process.
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	// Initialization is sent as initializationOptions at initialize.
	Initialization map[string]any `yaml:"initialization,omitempty" json:"initialization,omitempty"`
	// Markers overrides the project-root marker files (custom servers).
	Markers []string `yaml:"markers,omitempty" json:"markers,omitempty"`
	// LanguageID overrides the LSP language id sent in didOpen.
	LanguageID string `yaml:"language_id,omitempty" json:"language_id,omitempty"`
}

// LSPConfig mirrors OpenCode's top-level lsp config. It supports two forms:
//
//	lsp: false                      # disable all language servers
//	lsp:
//	  disable_download: true        # never auto-install servers
//	  servers:
//	    gopls: { disabled: true }   # disable one server
//	    myserver: { command: [...] } # define/override a server
//
// The zero value (no servers, nothing disabled) means "all builtins enabled".
type LSPConfig struct {
	// disabledAll is true when the user wrote `lsp: false`. Unexported so the
	// zero value is "enabled" (OpenCode's default).
	disabledAll bool
	// DisableDownload turns off on-demand server installation (OpenCode's
	// disableLspDownload). When true, only already-installed binaries are used.
	DisableDownload bool `yaml:"disable_download,omitempty" json:"disable_download,omitempty"`
	// Servers overrides/disables builtin servers or defines custom ones, keyed
	// by server id.
	Servers map[string]LSPServerConfig `yaml:"servers,omitempty" json:"servers,omitempty"`
}

// UnmarshalYAML implements yaml.Unmarshaler so `lsp: false` disables all
// servers while a mapping configures them. The default (absent) is enabled.
func (l *LSPConfig) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		var b bool
		if err := node.Decode(&b); err == nil {
			l.disabledAll = !b
			return nil
		}
	}
	type raw LSPConfig
	var r raw
	if err := node.Decode(&r); err != nil {
		return err
	}
	*l = LSPConfig(r)
	return nil
}

// IsEnabled reports whether LSP integration is active at all (`lsp: false`
// disables everything). Zero value is enabled.
func (l LSPConfig) IsEnabled() bool { return !l.disabledAll }

// MultiAgentConfig controls multi-agent collaboration settings.
