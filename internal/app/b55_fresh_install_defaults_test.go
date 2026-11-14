// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/tools"
)

// freshInstallConfig loads the cascade a fresh install resolves to: a
// sandboxed GOA_HOME with no ~/.goa config and a project dir with no .goa
// layer, so the embedded defaults are the only source (bugs.md B55).
func freshInstallConfig(t *testing.T) *config.Config {
	t.Helper()
	t.Setenv("GOA_HOME", t.TempDir())
	cfg, err := config.NewCascadeLoader(t.TempDir(), "", nil).Load()
	if err != nil {
		t.Fatalf("load fresh-install cascade: %v", err)
	}
	return cfg
}

// freshInstallRegistry runs the production boot registration path over the
// fresh-install config (registerTools plus the session/goal-adjacent
// registrations subsystems.go makes) — not a re-implementation.
func freshInstallRegistry(t *testing.T, cfg *config.Config) *tools.ToolRegistry {
	t.Helper()
	sessionStore := core.NewSessionStore(t.TempDir())
	reg := tools.NewToolRegistry()
	registerTools(reg, nil, nil, t.TempDir(), cfg, nil, false)
	registerWebFetchTool(reg, sessionStore, cfg, t.TempDir())
	registerSessionQueryTools(reg, sessionStore, cfg)
	return reg
}

// TestFreshInstall_FlagsPinsDefaultOnAndOff pins layer 1: the fresh config
// itself carries the default pins (mcp default-ON per B46, lsp default-ON,
// smartsearch off, todo_list default-OFF, the rest of the default-ON set
// intact).
func TestFreshInstall_FlagsPinsDefaultOnAndOff(t *testing.T) {
	cfg := freshInstallConfig(t)

	if !cfg.Tools.Enabled.AgentMCP {
		t.Error("fresh install tools.enabled.mcp = false, want true (B46 default-ON)")
	}
	if !cfg.Tools.Enabled.LSP {
		t.Error("fresh install tools.enabled.lsp = false, want true (default-ON)")
	}
	if cfg.Tools.SmartSearch.Enabled {
		t.Error("fresh install tools.smartsearch.enabled = true, want false (minimal default set)")
	}
	if !cfg.Tools.Enabled.WebFetch || !cfg.Tools.Enabled.Goal {
		t.Error("fresh install lost the default-ON webfetch/goal flags")
	}
	if cfg.Tools.Enabled.Todo {
		t.Error("fresh install tools.enabled.todo_list = true, want false (default-OFF)")
	}
}

// TestFreshInstall_RegistryHasMCPOnlyDefaults pins layer 2: the boot
// registration path over the fresh config registers mcp, webfetch and lsp,
// and none of the tools that ship off by default.
func TestFreshInstall_RegistryHasMCPOnlyDefaults(t *testing.T) {
	cfg := freshInstallConfig(t)
	reg := freshInstallRegistry(t, cfg)

	if _, ok := reg.Get("mcp"); !ok {
		t.Error("fresh install did not register the mcp tool")
	}
	if _, ok := reg.Get("webfetch"); !ok {
		t.Error("fresh install did not register webfetch (default-ON)")
	}
	if _, ok := reg.Get("lsp"); !ok {
		t.Error("fresh install did not register lsp (default-ON)")
	}
	for _, absent := range []string{
		"smartsearch", "python", "run_code", "terminals", "verify",
		"bg_exec", "ssh_bash", "memento", "todo_list", "session_search",
		"session_event_read", "schedule_create", "agent",
	} {
		if _, ok := reg.Get(absent); ok {
			t.Errorf("fresh install registered %q — it ships off by default", absent)
		}
	}
}

// TestFreshInstall_DefaultMetadataMirrorsFlags pins layer 3: the Default
// flags /config and /docs render must agree with the fresh-install reality
// (same inverted-key rule the config mirror test documents:
// ask_user_question tracks !clarify_disabled; smartsearch ships via its own
// tools.smartsearch.enabled gate, pinned by TestFreshInstall_Flags).
func TestFreshInstall_DefaultMetadataMirrorsFlags(t *testing.T) {
	cfg := freshInstallConfig(t)

	for _, ct := range tools.ConfigurableTools() {
		if ct.Name == "smartsearch" {
			continue
		}
		key, invert := ct.Name, false
		if ct.Name == "ask_user_question" {
			key, invert = "clarify_disabled", true
		}
		shipped := cfg.Tools.Enabled.GetEnabled(key)
		if invert {
			shipped = !shipped
		}
		if ct.Default != shipped {
			t.Errorf("ConfigurableTools %q Default=%v disagrees with the fresh-install flag tools.enabled.%s=%v", ct.Name, ct.Default, key, shipped)
		}
	}
}
