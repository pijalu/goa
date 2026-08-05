// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// setupSkillHome seeds a home config with an active provider/model so the
// snapshot writers (Save, SaveHomeProvidersAndModels) have content to write.
func setupSkillHome(t *testing.T) (homeDir, projectDir string, loader *CascadeLoader) {
	t.Helper()
	homeDir = t.TempDir()
	t.Setenv("HOME", homeDir)
	projectDir = t.TempDir()

	cfgDir := filepath.Join(homeDir, ".goa")
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatal(err)
	}
	seed := "active_provider: p\nactive_model: m\nproviders:\n- id: p\nmodels:\n- id: m\n  provider_id: p\n  model: m\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(seed), 0644); err != nil {
		t.Fatal(err)
	}
	loader = NewCascadeLoader(projectDir, "", nil)
	return homeDir, projectDir, loader
}

func disabledContains(t *testing.T, cfg *Config, name string) bool {
	t.Helper()
	for _, s := range cfg.Skills.Disabled {
		if s == name {
			return true
		}
	}
	return false
}

// TestSavePreservesOnDiskSkillDisable reproduces the "skills re-enable
// spontaneously" bug: the user disables a skill (a field-scoped write to
// disk), then a later snapshot Save() fires with an in-memory config that was
// loaded BEFORE the toggle (its Skills.Disabled is still empty). The stale
// snapshot must not wipe the on-disk skills.disabled entry — otherwise the
// skill is resurrected on the next cascade load.
func TestSavePreservesOnDiskSkillDisable(t *testing.T) {
	_, _, loader := setupSkillHome(t)

	stale, err := loader.Load() // snapshot taken before the skill toggle
	if err != nil {
		t.Fatal(err)
	}

	// User disables a skill via /config or /skill:disable (field-scoped write).
	if err := loader.SaveHomeFieldValue([]string{"skills", "disabled"}, []string{"review"}); err != nil {
		t.Fatal(err)
	}

	// Later, an unrelated snapshot Save fires with the stale config
	// (model switch, /goal:settings, /config:add, thinking-level).
	if err := loader.Save(stale); err != nil {
		t.Fatal(err)
	}

	final, err := loader.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if !disabledContains(t, final, "review") {
		t.Errorf("skills.disabled lost after stale snapshot Save; Disabled=%v", final.Skills.Disabled)
	}
}

// TestSaveHomeProvidersAndModelsPreservesSkillDisable covers the model-switch
// / thinking-level path, which writes the provider/model section but must not
// disturb a concurrently persisted skills.disabled entry.
func TestSaveHomeProvidersAndModelsPreservesSkillDisable(t *testing.T) {
	_, _, loader := setupSkillHome(t)

	cfg, err := loader.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := loader.SaveHomeFieldValue([]string{"skills", "disabled"}, []string{"review"}); err != nil {
		t.Fatal(err)
	}

	// Model switch writes provider/model fields from the (pre-toggle) cfg.
	cfg.ActiveModel = "m2"
	if err := loader.SaveHomeProvidersAndModels(cfg); err != nil {
		t.Fatal(err)
	}

	final, err := loader.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if !disabledContains(t, final, "review") {
		t.Errorf("skills.disabled lost after SaveHomeProvidersAndModels; Disabled=%v", final.Skills.Disabled)
	}
}

// TestConcurrentSkillToggleVsSnapshotSave is the race variant: a field-scoped
// skill toggle and a snapshot save hammer the same home config file from many
// goroutines. With write serialization, the skills.disabled entry written by
// the toggle must survive every interleaving.
func TestConcurrentSkillToggleVsSnapshotSave(t *testing.T) {
	_, _, loader := setupSkillHome(t)

	base, err := loader.Load()
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = loader.SaveHomeFieldValue([]string{"skills", "disabled"}, []string{"review"})
		}()
		go func() {
			defer wg.Done()
			_ = loader.Save(base)
		}()
	}
	wg.Wait()

	final, err := loader.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if !disabledContains(t, final, "review") {
		t.Errorf("skills.disabled lost under concurrent writes; Disabled=%v", final.Skills.Disabled)
	}
}

// TestConcurrentFieldWritesRaceFree asserts the whole write surface is
// serialized: concurrent scalar field writes and skill-list writes from many
// goroutines must not corrupt the file (YAML stays parseable) nor lose the
// skills entry. Run under -race to catch unsynchronized file access.
func TestConcurrentFieldWritesRaceFree(t *testing.T) {
	_, _, loader := setupSkillHome(t)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			_ = loader.SaveHomeFieldValue([]string{"skills", "disabled"}, []string{"review"})
		}()
		go func() {
			defer wg.Done()
			_ = loader.SaveHomeField([]string{"tui", "theme"}, "dark")
		}()
		go func() {
			defer wg.Done()
			_ = loader.SaveProjectField([]string{"mode", "active"}, "coder")
		}()
	}
	wg.Wait()

	final, err := loader.Reload()
	if err != nil {
		t.Fatalf("config file corrupted under concurrent writes: %v", err)
	}
	if !disabledContains(t, final, "review") {
		t.Errorf("skills.disabled lost under concurrent field writes; Disabled=%v", final.Skills.Disabled)
	}
}
