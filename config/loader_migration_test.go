package config

import (
	"path/filepath"
	"testing"

	"github.com/pijalu/goa/internal"
)

func TestLegacyMigrationFallback(t *testing.T) {
	home, project, cleanup := setupTestConfig(t)
	defer cleanup()
	t.Setenv("HOME", home)
	writeConfig(t, filepath.Join(home, ".goa", "config.yaml"), "execution:\n  mode: confirm\n")
	c, e := NewCascadeLoader(project, "", nil).Load()
	if e != nil {
		t.Fatal(e)
	}
	if c.Mode.Defaults[internal.MajorCoder] != internal.AutonomyConfirm || c.DefaultModeState().Autonomy != internal.AutonomyConfirm {
		t.Errorf("defaults=%v state=%v", c.Mode.Defaults, c.DefaultModeState())
	}
}
