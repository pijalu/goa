// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestLSPConfig_DefaultEnabled(t *testing.T) {
	var c Config
	if !c.LSP.IsEnabled() {
		t.Error("zero-value LSPConfig must be enabled (OpenCode default)")
	}
}

func TestLSPConfig_ScalarFalse(t *testing.T) {
	var c Config
	if err := yaml.Unmarshal([]byte("lsp: false\n"), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c.LSP.IsEnabled() {
		t.Error("lsp: false must disable all servers")
	}
}

func TestLSPConfig_ServerOverrides(t *testing.T) {
	doc := `
lsp:
  disable_download: true
  servers:
    gopls:
      disabled: true
    myserver:
      command: ["myls", "--stdio"]
      extensions: [".my"]
      env:
        FOO: bar
`
	var c Config
	if err := yaml.Unmarshal([]byte(doc), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !c.LSP.IsEnabled() {
		t.Error("mapping form must leave LSP enabled")
	}
	if !c.LSP.DisableDownload {
		t.Error("disable_download must parse")
	}
	g, ok := c.LSP.Servers["gopls"]
	if !ok || !g.Disabled {
		t.Errorf("gopls override = %+v, want disabled", g)
	}
	m, ok := c.LSP.Servers["myserver"]
	if !ok {
		t.Fatal("myserver override missing")
	}
	if len(m.Command) != 2 || m.Command[0] != "myls" {
		t.Errorf("myserver command = %v", m.Command)
	}
	if m.Env["FOO"] != "bar" {
		t.Errorf("myserver env = %v", m.Env)
	}
}
