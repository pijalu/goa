package commands

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
)

// writeTestFileT creates the parent directory and writes content — used to
// seed legacy on-disk configs for the auto_save_model cascade tests.
func writeTestFileT(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

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
// against a recording saver and asserts (a) a successful switch writes ONLY
// the project pin (bugs.md: ~/.goa must not receive project-scoped model
// changes), and (b) with auto_save_model false the legacy home-only fallback
// applies without any project write.
func TestPersistModelSwitch_ProjectFirstOrder(t *testing.T) {
	saver := &fakeConfigSaver{}
	cfg := twoProviderConfig(t)
	cfg.Execution.AutoSaveModel = boolPtr(true)
	cfg.ActiveProvider = "stealth"
	cfg.ActiveModel = "stealth/ox-alpha"

	if err := persistModelSwitch(cfg, saver); err != nil {
		t.Fatalf("persistModelSwitch: %v", err)
	}
	if saver.projectActiveSaved == nil {
		t.Fatal("project pin was not saved")
	}
	// Project-only: NO home layer write once the pin landed.
	if len(saver.saveOrder) != 1 || saver.saveOrder[0] != "project_active" {
		t.Fatalf("save order = %v, want exactly [project_active] (home untouched)", saver.saveOrder)
	}

	// Opt-out (auto_save_model false): no project pin, home fallback only.
	saver2 := &fakeConfigSaver{}
	cfg.Execution.AutoSaveModel = boolPtr(false)
	if err := persistModelSwitch(cfg, saver2); err != nil {
		t.Fatalf("persistModelSwitch opt-out: %v", err)
	}
	if saver2.projectActiveSaved != nil {
		t.Fatal("opt-out must not write the project pin")
	}
	if len(saver2.saveOrder) != 1 || saver2.saveOrder[0] != "save" {
		t.Fatalf("opt-out save order = %v, want exactly [save] (home only)", saver2.saveOrder)
	}
}

// TestPersistModelSwitch_ProjectErrorFallsBackToHome pins the bugs.md
// fallback contract: when the project layer cannot be changed (write error,
// e.g. read-only tree) the switch is persisted to ~/.goa instead of being
// lost — home is the safety net for unchangeable projects, nothing else.
func TestPersistModelSwitch_ProjectErrorFallsBackToHome(t *testing.T) {
	saver := &fakeConfigSaver{projectActiveErr: config.ErrNoProjectDir}
	cfg := twoProviderConfig(t)
	cfg.Execution.AutoSaveModel = boolPtr(true)

	if err := persistModelSwitch(cfg, saver); err != nil {
		t.Fatalf("persistModelSwitch with failing project layer: %v", err)
	}
	if len(saver.saveOrder) != 2 ||
		saver.saveOrder[0] != "project_active" || saver.saveOrder[1] != "save" {
		t.Fatalf("save order = %v, want [project_active, save] (home fallback)", saver.saveOrder)
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
	cfg.Execution.AutoSaveModel = boolPtr(true)
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
	// ...home stays untouched — the pin is project-scoped now (bugs.md):
	// ~/.goa is written only when the project layer cannot be changed.
	homePath := filepath.Join(home, ".goa", "config.yaml")
	if rawHome, err := os.ReadFile(homePath); err == nil &&
		strings.Contains(string(rawHome), "active_model") {
		t.Fatalf("home must not receive the project-scoped switch:\n%s", rawHome)
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
// relative .goa directory when no project dir is configured, and pins the
// ErrNoProjectDir sentinel callers rely on for home-layer fallback.
func TestSaveProjectActiveModel_NoProjectDirIsNoop(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cwd, _ := os.Getwd()

	cl := config.NewCascadeLoader("", "", nil)
	err := cl.SaveProjectActiveModel(&config.Config{ActiveProvider: "p", ActiveModel: "m"})
	if !errors.Is(err, config.ErrNoProjectDir) {
		t.Fatalf("no project dir = %v, want config.ErrNoProjectDir", err)
	}
	if _, err := os.Stat(filepath.Join(cwd, ".goa")); !os.IsNotExist(err) {
		t.Fatal("relative .goa must not be created without a project dir")
	}
}

// TestPersistModelSwitch_UnwritableProjectFallsBackToHome is the end-to-end
// fallback proof for a project layer that EXISTS but cannot be written:
// .goa is a regular file, so MkdirAll/save fails and the switch must land in
// ~/.goa instead of being dropped.
func TestPersistModelSwitch_UnwritableProjectFallsBackToHome(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GOA_HOME", home)

	// Block the project layer AFTER loading (a blocked dir would fail Load):
	// a regular file where the config dir belongs makes every project write
	// fail with "not a directory".
	loader := config.NewCascadeLoader(project, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, ".goa"), []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("block project dir: %v", err)
	}
	cfg.Execution.AutoSaveModel = boolPtr(true)
	cfg.ActiveModel = "new-model"

	if err := persistModelSwitch(cfg, loader); err != nil {
		t.Fatalf("persistModelSwitch: %v", err)
	}
	homePath := filepath.Join(home, ".goa", "config.yaml")
	if !strings.Contains(readTestFile(t, homePath), "active_model: new-model") {
		t.Fatalf("home fallback missing after unwritable project:\n%s", readTestFile(t, homePath))
	}
}

// TestModelSwitch_LegacyConfigPinsProjectModel is the bugs.md acceptance
// flow for "switching model with /model isn't saved into the project config":
// a PRE-EXISTING install whose home yaml predates auto_save_model (execution
// section present, key absent) must still pin the switched pair into the
// project layer, because nil now resolves to the embedded default TRUE
// instead of being clobbered to false by the cascade merge.
func TestModelSwitch_LegacyConfigPinsProjectModel(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GOA_HOME", home) // CascadeLoader resolves home via internal.GoaHome

	// Legacy install: home carries an execution section WITHOUT the key,
	// project exists with a STALE active_model pin from an earlier era.
	writeTestFileT(t, filepath.Join(home, ".goa", "config.yaml"),
		"execution:\n  thinking_default: high\n")
	writeTestFileT(t, filepath.Join(project, ".goa", "config.yaml"),
		"active_provider: openai-codex\nactive_model: ox-alpha\n")

	loader := config.NewCascadeLoader(project, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Execution.AutoSaveModelEnabled() {
		t.Fatalf("precondition: legacy config must resolve auto_save_model=true before the switch")
	}
	if cfg.ActiveModel != "ox-alpha" {
		t.Fatalf("precondition: boot model = %q, want the stale project pin ox-alpha", cfg.ActiveModel)
	}

	// The REAL user path: /model new-model while booted on the stale pin.
	// GOA_HOME was set before the context helper so its loader lands on the
	// seeded home.
	ctx, _ := newPickerTestContextWithProject(t, cfg, project)
	if err := runModelCommand(*ctx, ctx.ProviderManager, cfg, ctx.ConfigSaver, []string{"new-model"}); err != nil {
		t.Fatalf("runModelCommand: %v", err)
	}

	rawProject, err := os.ReadFile(filepath.Join(project, ".goa", "config.yaml"))
	if err != nil {
		t.Fatalf("project config read: %v", err)
	}
	if !strings.Contains(string(rawProject), "active_model: new-model") {
		t.Fatalf("project config not pinned with the switched model:\n%s", rawProject)
	}
	// Home stays out of it: the project layer was changeable.
	rawHome, err := os.ReadFile(filepath.Join(home, ".goa", "config.yaml"))
	if err == nil && strings.Contains(string(rawHome), "active_model:") {
		t.Fatalf("home must not receive the pin when the project layer works:\n%s", rawHome)
	}
	// Reload feeds the NEXT session's boot model.
	reloaded, err := config.NewCascadeLoader(project, "", nil).Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.ActiveProvider != "openai-codex" || reloaded.ActiveModel != "new-model" {
		t.Fatalf("reloaded pair = (%s, %s), want project pin (openai-codex, new-model)",
			reloaded.ActiveProvider, reloaded.ActiveModel)
	}
}

// TestModelSwitch_ExplicitOptOutSkipsProjectPin pins the other side of the
// tri-state contract: an explicit execution.auto_save_model:false on disk is
// an intentional opt-out — no project pin may appear when switching; home
// remains the only persistence (legacy behavior byte-for-byte).
func TestModelSwitch_ExplicitOptOutSkipsProjectPin(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GOA_HOME", home)

	writeTestFileT(t, filepath.Join(home, ".goa", "config.yaml"),
		"execution:\n  auto_save_model: false\n")
	writeTestFileT(t, filepath.Join(project, ".goa", "config.yaml"),
		"mode:\n  default:\n    major: coder\n")

	loader := config.NewCascadeLoader(project, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Execution.AutoSaveModelEnabled() {
		t.Fatalf("explicit false must resolve disabled")
	}

	if err := persistModelSwitch(cfg, loader); err != nil {
		t.Fatalf("persistModelSwitch: %v", err)
	}
	rawProject, _ := os.ReadFile(filepath.Join(project, ".goa", "config.yaml"))
	if strings.Contains(string(rawProject), "active_model") {
		t.Fatalf("opted-out switch must not pin the project:\n%s", rawProject)
	}
	homePath := filepath.Join(home, ".goa", "config.yaml")
	if !strings.Contains(readTestFile(t, homePath), "active_model") {
		t.Fatalf("opt-out persistence must still reach home:\n%s", readTestFile(t, homePath))
	}
}

// TestRemoveActiveModel_ClearsProjectPin is the stale-pin regression:
// removing the ACTIVE model clears cfg.ActiveModel, and the per-project pin
// must be cleared in the same persist. SaveProjectActiveModel used to skip
// empty values, so the removed model stayed pinned in the highest-precedence
// cascade layer and was resurrected on the next load — pointing the session
// at a model that no longer existed in cfg.Models.
func TestRemoveActiveModel_ClearsProjectPin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GOA_HOME", home) // CascadeLoader resolves home via internal.GoaHome
	project := t.TempDir()

	cfg := twoProviderConfig(t)
	cfg.Execution.AutoSaveModel = boolPtr(true)
	cfg.ActiveProvider = "stealth"
	cfg.ActiveModel = "ox-alpha" // ID-keyed pin (YAML-configured model flow)
	ctx, _ := newPickerTestContextWithProject(t, cfg, project)

	// Switch persists the pin into the project layer.
	if err := persistModelSwitch(cfg, ctx.ConfigSaver); err != nil {
		t.Fatalf("pin: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(project, ".goa", "config.yaml"))
	if err != nil || !strings.Contains(string(raw), "active_model: ox-alpha") {
		t.Fatalf("pin not written (err=%v):\n%s", err, raw)
	}

	// Remove the active model — the exact removeModelFromConfig flow.
	removeModelFromConfig(cfg, "ox-alpha", ctx.ConfigSaver, *ctx)

	raw, err = os.ReadFile(filepath.Join(project, ".goa", "config.yaml"))
	if err != nil {
		t.Fatalf("read project config: %v", err)
	}
	if strings.Contains(string(raw), "active_model") {
		t.Fatalf("stale active_model pin survived removal:\n%s", raw)
	}

	// Reload must not resurrect the removed model from any layer.
	reloaded, err := config.NewCascadeLoader(project, "", nil).Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.ActiveModel == "ox-alpha" {
		t.Fatalf("removed model resurrected from pin: %q", reloaded.ActiveModel)
	}
}
