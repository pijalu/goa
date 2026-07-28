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

	// Default (embedded semantics): lsp tool flag on → a manager exists.
	enabled := &config.Config{}
	enabled.Tools.Enabled.LSP = true
	if mgr := newLSPManager(dir, enabled); mgr == nil {
		t.Error("lsp tool flag on: expected an LSP manager")
	}

	// lsp: false → no manager even with the tool flag on.
	var disabled config.Config
	if err := yaml.Unmarshal([]byte("lsp: false\n"), &disabled); err != nil {
		t.Fatalf("unmarshal lsp:false: %v", err)
	}
	disabled.Tools.Enabled.LSP = true
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

// TestNewLSPManager_ToolFlagDisable verifies tools.enabled.lsp: false disables
// the WHOLE integration — no manager, hence no file touches and no background
// server spawns (bugs.md Issue LSP / "Read stuck": the flag used to gate only
// the model-facing tool while the manager kept spawning servers).
func TestNewLSPManager_ToolFlagDisable(t *testing.T) {
	dir := t.TempDir()

	// Zero value = flag off (embedded default flips it on in production).
	if mgr := newLSPManager(dir, &config.Config{}); mgr != nil {
		t.Error("tools.enabled.lsp unset/false: expected nil manager (off means off)")
	}

	enabled := &config.Config{}
	enabled.Tools.Enabled.LSP = true
	if mgr := newLSPManager(dir, enabled); mgr == nil {
		t.Error("tools.enabled.lsp true: expected a manager")
	}
}
