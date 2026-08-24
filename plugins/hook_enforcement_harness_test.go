// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Harness-level M6 enforcement proof: a real JS plugin with a manifest,
// loaded via LoadFrom through the same bridge path production uses.
package plugins

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// m6HarnessFixture writes a plugin whose manifest declares exactly one hook
// (message:pre-send intercept) while its JS registers that hook plus an
// UNDECLARED one, then proves liveness via goa.output.
func m6HarnessFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	yaml := "id: m6-fixture\nname: M6 Fixture\nversion: 1.0.0\n" +
		"hooks:\n  - point: message:pre-send\n    mode: intercept\n    description: upper-case text\n"
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	js := `
goa.registerHook({
  name: "approved",
  point: "message:pre-send",
  mode: "intercept",
  handler: function(p) { return undefined; }
});
goa.registerHook({
  name: "undeclared",
  point: "session:start",
  mode: "notify",
  handler: function(p) { }
});
goa.output("survived");
`
	if err := os.WriteFile(filepath.Join(dir, "plugin.js"), []byte(js), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestHarness_EnforcementThroughBridge is the §7 step 4 positive/negative
// proof: granted+declared registers, undeclared is rejected by the enforcer
// installed in the M2 validator slot, and a rejection never kills the rest of
// the plugin script.
func TestHarness_EnforcementThroughBridge(t *testing.T) {
	dir := m6HarnessFixture(t)
	def, err := loadManifest(filepath.Join(dir, "plugin.yaml"))
	if err != nil {
		t.Fatalf("fixture manifest must be valid: %v", err)
	}

	env := newHooksTestEnv(t)
	store := NewGrantStore(t.TempDir())
	enf := NewHookEnforcer(store, false)
	enf.Declare(def, false)
	env.registry.SetAllow(enf.Allow)

	// Scenario A — approved install: declared hook lands, undeclared refused.
	if err := ApplyHookDecision(store, def, []string{"intercept|message:pre-send"}); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFrom(dir, env.context()); err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	chain := env.registry.Snapshot("message:pre-send")
	if len(chain) != 1 || chain[0].Spec.Name != "approved" || chain[0].Spec.PluginID != "m6-fixture" {
		t.Fatalf("granted hook must register: %+v", chain)
	}
	if len(env.registry.Snapshot("session:start")) != 0 {
		t.Fatal("undeclared hook must never reach the registry")
	}

	// Scenario B — fresh store (no grant): everything refused, plugin lives.
	env2 := newHooksTestEnv(t)
	enf2 := NewHookEnforcer(NewGrantStore(t.TempDir()), false)
	enf2.Declare(def, false)
	env2.registry.SetAllow(enf2.Allow)
	if _, err := LoadFrom(dir, env2.context()); err != nil {
		t.Fatalf("LoadFrom (ungranted): %v", err)
	}
	if env2.registry.Count() != 0 {
		t.Fatalf("no grant ⇒ no hooks, got %d entries", env2.registry.Count())
	}
	survivedA := strings.Join(env.outputs, "\n")
	survivedB := strings.Join(env2.outputs, "\n")
	if !strings.Contains(survivedA, "survived") || !strings.Contains(survivedB, "survived") {
		t.Fatalf("rejections must not abort the plugin script: %q / %q", survivedA, survivedB)
	}
}
