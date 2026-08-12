// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSaveProjectConfig_PreservesOnDiskToolFlags is the regression test for
// the goal enable/disable flag reverting across sessions. /mode and /autonomy
// persist via SaveProjectConfig, which (before the fix) marshaled the whole
// in-memory config. A stale in-memory Tools.Enabled (e.g. goal flipped to true
// earlier in the session) was written back over the on-disk goal:false, so the
// next session reloaded goal:true. The save must be merge-preserving: only the
// caller's mode fields update; unrelated on-disk fields keep their values.
func TestSaveProjectConfig_PreservesOnDiskToolFlags(t *testing.T) {
	proj := t.TempDir()
	home := t.TempDir()
	mustMkdir(t, filepath.Join(proj, ".goa"))
	mustMkdir(t, filepath.Join(home, ".goa"))

	// On-disk project config: goal explicitly disabled.
	writeFile(t, filepath.Join(proj, ".goa", "config.yaml"),
		"tools:\n  enabled:\n    goal: false\n    agent: false\n")

	cl := NewCascadeLoader(proj, "", map[string]string{})
	cl.homeDir = home

	cfg, err := cl.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Tools.Enabled.Goal {
		t.Fatalf("precondition: goal should load false, got true")
	}

	// Simulate staleness: goal was toggled ON earlier in the session (in-memory
	// only), then the user ran /mode or /autonomy which calls SaveProjectConfig.
	cfg.Tools.Enabled.SetEnabled("goal", true)
	cfg.Tools.Enabled.SetEnabled("agent", true)
	// The caller's legitimate change: a new major mode (what /mode persists).
	cfg.Mode.Default.Major = "oracle"

	if err := cl.SaveProjectConfig(cfg); err != nil {
		t.Fatalf("SaveProjectConfig: %v", err)
	}

	// Reload and verify the on-disk goal:false was NOT clobbered by the stale
	// in-memory goal:true.
	reloaded, err := cl.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Tools.Enabled.Goal {
		t.Errorf("goal flag clobbered: on-disk goal:false became %v after SaveProjectConfig with stale in-memory true", reloaded.Tools.Enabled.Goal)
	}
	if reloaded.Tools.Enabled.Agent {
		t.Errorf("agent flag clobbered: on-disk agent:false became %v after SaveProjectConfig", reloaded.Tools.Enabled.Agent)
	}
	// ...while the caller's mode change IS persisted (the save is scoped, not a
	// blanket no-op).
	if string(reloaded.Mode.Default.Major) != "oracle" {
		t.Errorf("mode change not persisted: Mode.Default.Major = %q, want oracle", reloaded.Mode.Default.Major)
	}
}

// TestSaveProjectConfig_NoExistingFile_WritesModeOnly is the regression test
// for the stale-project-config shadowing bug: when the project has
// no .goa/config.yaml yet, SaveProjectConfig must persist ONLY the mode
// section — the documented field scope of this entry point (/mode, /autonomy,
// autonomy-cycle hotkey). Previously it marshaled the ENTIRE merged in-memory
// config (embedded defaults + home-layer values) into the project layer,
// where those baked values silently shadowed every later home-config edit
// (e.g. the embedded default context_compression.strategy "micro" kept
// winning over the user's home-config "tool_elision" on every startup).
func TestSaveProjectConfig_NoExistingFile_WritesModeOnly(t *testing.T) {
	proj := t.TempDir()
	home := t.TempDir()
	mustMkdir(t, filepath.Join(home, ".goa"))

	cl := NewCascadeLoader(proj, "", map[string]string{})
	cl.homeDir = home

	cfg, err := cl.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// Precondition: the merged in-memory config carries embedded defaults
	// (context_compression) — the values that must NOT be baked into the
	// project layer.
	if cfg.ContextCompression.Strategy == "" {
		t.Fatalf("precondition: embedded compression strategy should be set")
	}
	// The caller's legitimate change (what /mode and /autonomy persist).
	cfg.Mode.Default.Major = "oracle"

	if err := cl.SaveProjectConfig(cfg); err != nil {
		t.Fatalf("SaveProjectConfig: %v", err)
	}

	// The written file must contain ONLY the mode section: no merged
	// top-level sections that would shadow the home config on next load.
	raw, err := os.ReadFile(filepath.Join(proj, ".goa", "config.yaml"))
	if err != nil {
		t.Fatalf("read written project config: %v", err)
	}
	content := string(raw)
	for _, banned := range []string{
		"context_compression", "providers:", "models:", "tools:",
		"telegram:", "execution:", "active_model", "orchestrator:",
	} {
		if strings.Contains(content, banned) {
			t.Errorf("project config must not bake %q into the project layer, got:\n%s", banned, content)
		}
	}
	if !strings.Contains(content, "mode:") {
		t.Errorf("project config missing the mode section, got:\n%s", content)
	}

	// The caller's mode change round-trips, and compression still resolves
	// from the lower layers (nothing shadowed by the project file).
	reloaded, err := cl.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if string(reloaded.Mode.Default.Major) != "oracle" {
		t.Errorf("mode change not persisted: Mode.Default.Major = %q, want oracle", reloaded.Mode.Default.Major)
	}
	if reloaded.ContextCompression.Strategy != cfg.ContextCompression.Strategy {
		t.Errorf("compression strategy changed across save: %q -> %q (project layer must not restate it)",
			cfg.ContextCompression.Strategy, reloaded.ContextCompression.Strategy)
	}
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
