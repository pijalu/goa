package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoaderSaveHomeFieldAgain(t *testing.T) {
	home, project, cleanup := setupTestConfig(t)
	defer cleanup()
	t.Setenv("HOME", home)
	l := NewCascadeLoader(project, "", nil)
	p := filepath.Join(home, ".goa", "config.yaml")
	writeConfig(t, p, "active_provider: existing-provider\n")
	if err := l.SaveHomeField([]string{"tui", "transparency", "thinking_collapsed"}, true); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !containsStr(string(b), "thinking_collapsed") || !containsStr(string(b), "existing-provider") {
		t.Errorf("saved=%s", b)
	}
}
