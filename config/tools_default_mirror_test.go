// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"reflect"
	"testing"

	"github.com/pijalu/goa/tools"
)

// TestConfigurableTools_AllDefaultsMirrorEmbeddedConfig generalizes the goal
// mirror (TestConfigurableTools_GoalDefaultMatchesEmbeddedConfig) to EVERY
// runtime-toggleable tool: the `Default` field ConfigurableTools reports must
// equal the tools.enabled.<name> value the embedded default config ships.
// /config, /docs completions and the Tools menu render these Defaults, so a
// stale Default lies to the user about the state of a fresh install
// (bugs.md B51: B46 flipped default.yaml but left the registry Defaults on
// the pre-B46 values).
//
// ask_user_question is the documented inverted flag: its Default tracks
// !clarify_disabled. Every other yaml key equals the tool name.
func TestConfigurableTools_AllDefaultsMirrorEmbeddedConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cfg, err := NewCascadeLoader(t.TempDir(), "", nil).Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Build yaml-tag → shipped value from the loaded ToolEnabledConfig so a
	// future flag is covered the moment it gains a yaml key.
	flags := map[string]bool{}
	en := reflect.ValueOf(cfg.Tools.Enabled)
	for i := 0; i < en.NumField(); i++ {
		f := en.Type().Field(i)
		tag := f.Tag.Get("yaml")
		if tag == "" || tag == "-" || f.Type.Kind() != reflect.Bool {
			continue
		}
		flags[tag] = en.Field(i).Bool()
	}

	for _, ct := range tools.ConfigurableTools() {
		if ct.Name == "smartsearch" {
			// Not a tools.enabled.* flag: smartsearch is gated by its own
			// tools.smartsearch.enabled key (documented restart-only tool).
			continue
		}
		key, invert := ct.Name, false
		if ct.Name == "ask_user_question" {
			key, invert = "clarify_disabled", true
		}
		shipped, ok := flags[key]
		if !ok {
			t.Fatalf("ToolEnabledConfig has no yaml flag %q for configurable tool %q", key, ct.Name)
		}
		if invert {
			shipped = !shipped
		}
		if ct.Default != shipped {
			t.Errorf("ConfigurableTools() %s Default = %v, want %v (embedded default config tools.enabled.%s)",
				ct.Name, ct.Default, shipped, key)
		}
	}
}
