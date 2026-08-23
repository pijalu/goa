package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
)

// TestSaveProjectActiveModel_CreatesAndUpdates pins the config-layer pin:
// the project .goa/config.yaml is created when missing, updated afterwards,
// and other keys in the file are preserved.
func TestSaveProjectActiveModel_CreatesAndUpdates(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	project := t.TempDir()
	cl := config.NewCascadeLoader(project, "", nil)

	cfg := &config.Config{ActiveProvider: "stealth", ActiveModel: "ox-alpha"}
	if err := cl.SaveProjectActiveModel(cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(project, ".goa", "config.yaml"))
	if err != nil {
		t.Fatalf("project config not created: %v", err)
	}
	out := string(data)
	if !strings.Contains(out, "active_provider: stealth") || !strings.Contains(out, "active_model: ox-alpha") {
		t.Fatalf("pin missing from file:\n%s", out)
	}

	// Update the pin; unrelated keys survive.
	if err := cl.SaveProjectField([]string{"mode", "default", "major"}, "coder"); err != nil {
		t.Fatalf("seed mode key: %v", err)
	}
	cfg2 := &config.Config{ActiveProvider: "openai-codex", ActiveModel: "gpt-x"}
	if err := cl.SaveProjectActiveModel(cfg2); err != nil {
		t.Fatalf("update: %v", err)
	}
	data, _ = os.ReadFile(filepath.Join(project, ".goa", "config.yaml"))
	out = string(data)
	if !strings.Contains(out, "active_model: gpt-x") || !strings.Contains(out, "major: coder") {
		t.Fatalf("update lost data:\n%s", out)
	}

	// Reload through the cascade: the project pin wins over home.
	got, err := cl.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.ActiveProvider != "openai-codex" || got.ActiveModel != "gpt-x" {
		t.Fatalf("reloaded pair = (%s, %s), want project pin (openai-codex, gpt-x)",
			got.ActiveProvider, got.ActiveModel)
	}
}

// TestPersistModelSwitch_ProjectFirstOrder drives the switch persistence
// against a recording saver and asserts (a) both layers written, (b) the
// project pin lands BEFORE the home fallback.
func TestPersistModelSwitch_ProjectFirstOrder(t *testing.T) {
	saver := &fakeConfigSaver{}
	cfg := twoProviderConfig(t)
	cfg.Execution.AutoSaveModel = true
	cfg.ActiveProvider = "stealth"
	cfg.ActiveModel = "stealth/ox-alpha"

	if err := persistModelSwitch(cfg, saver); err != nil {
		t.Fatalf("persistModelSwitch: %v", err)
	}
	if saver.projectActiveSaved == nil {
		t.Fatal("project pin was not saved")
	}
	if len(saver.saveOrder) < 2 ||
		saver.saveOrder[0] != "project_active" {
		t.Fatalf("save order = %v, want project_active first, then home", saver.saveOrder)
	}

	// Opt-out (auto_save_model false): no project pin, home fallback only.
	saver2 := &fakeConfigSaver{}
	cfg.Execution.AutoSaveModel = false
	if err := persistModelSwitch(cfg, saver2); err != nil {
		t.Fatalf("persistModelSwitch opt-out: %v", err)
	}
	if saver2.projectActiveSaved != nil {
		t.Fatal("opt-out must not write the project pin")
	}
}

// TestModelSwitch_PerProjectPinEndToEnd is the Bug6 acceptance flow: switch
// models in project A, verify A's .goa pins the new pair while home holds it
// as fallback — and that reloading project A yields A's pin.
func TestModelSwitch_PerProjectPinEndToEnd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GOA_HOME", home) // CascadeLoader resolves home via internal.GoaHome
	projectA := t.TempDir()

	cfg := twoProviderConfig(t)
	cfg.Execution.AutoSaveModel = true
	ctx, pm := newPickerTestContextWithProject(t, cfg, projectA)

	applyModelSelectionForProvider(*ctx, cfg, ctx.ConfigSaver, "stealth", "stealth/ox-alpha")

	if pm.setProvider != "stealth" {
		t.Fatalf("switch failed: SetActive = %q", pm.setProvider)
	}
	// Project A pinned...
	rawA, err := os.ReadFile(filepath.Join(projectA, ".goa", "config.yaml"))
	if err != nil {
		t.Fatalf("project A pin missing: %v", err)
	}
	if !strings.Contains(string(rawA), "active_model: stealth/ox-alpha") {
		t.Fatalf("project A pin wrong:\n%s", rawA)
	}
	// ...home carries the global fallback...
	rawHome, err := os.ReadFile(filepath.Join(home, ".goa", "config.yaml"))
	if err != nil {
		t.Fatalf("home fallback missing: %v", err)
	}
	if !strings.Contains(string(rawHome), "active_model: stealth/ox-alpha") {
		t.Fatalf("home fallback wrong:\n%s", rawHome)
	}
	// ...and reloading the cascade resolves to the project pin (which feeds
	// the status bar via cfg.ActiveProvider/ActiveModel).
	reloaded, err := config.NewCascadeLoader(projectA, "", nil).Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.ActiveProvider != "stealth" || reloaded.ActiveModel != "stealth/ox-alpha" {
		t.Fatalf("reloaded pair = (%s, %s), want project pin",
			reloaded.ActiveProvider, reloaded.ActiveModel)
	}
}

// TestSaveProjectActiveModel_NoProjectDirIsNoop guards against creating a
// relative .goa directory when no project dir is configured.
func TestSaveProjectActiveModel_NoProjectDirIsNoop(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cwd, _ := os.Getwd()

	cl := config.NewCascadeLoader("", "", nil)
	if err := cl.SaveProjectActiveModel(&config.Config{ActiveProvider: "p", ActiveModel: "m"}); err != nil {
		t.Fatalf("noop save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cwd, ".goa")); !os.IsNotExist(err) {
		t.Fatal("relative .goa must not be created without a project dir")
	}
}
