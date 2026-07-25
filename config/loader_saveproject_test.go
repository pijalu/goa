// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
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
