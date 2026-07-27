// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"testing"

	"github.com/pijalu/goa/config"
	"gopkg.in/yaml.v3"
)

// TestNewLSPManager_GlobalDisable verifies the global `lsp: false` flag returns
// no manager at all — which removes the lsp tool (registration gate) and
// disables all file-action linking (read/edit/write get a nil LSPManager).
func TestNewLSPManager_GlobalDisable(t *testing.T) {
	dir := t.TempDir()

	// Default (zero config): enabled → a manager exists.
	if mgr := newLSPManager(dir, &config.Config{}); mgr == nil {
		t.Error("default config: expected an LSP manager (enabled by default)")
	}

	// lsp: false → no manager.
	var disabled config.Config
	if err := yaml.Unmarshal([]byte("lsp: false\n"), &disabled); err != nil {
		t.Fatalf("unmarshal lsp:false: %v", err)
	}
	if disabled.LSP.IsEnabled() {
		t.Fatal("precondition: lsp: false should disable LSP")
	}
	if mgr := newLSPManager(dir, &disabled); mgr != nil {
		t.Error("lsp: false must return nil manager (disables tool + file linking)")
	}

	// nil config: treated as enabled.
	if mgr := newLSPManager(dir, nil); mgr == nil {
		t.Error("nil config: expected an LSP manager (enabled)")
	}
}
