// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/provider"
)

// TestSubsystems_ConfigWatcher_AppliesEdit is the end-to-end acceptance test
// for P22/DS6: editing ~/.goa/config.yaml's model applies on the next request
// without restart. It wires the real loader + provider manager through
// startConfigWatcher and verifies the provider profile swaps after the edit.
func TestSubsystems_ConfigWatcher_AppliesEdit(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	internal.SetGoaHome(homeDir)
	t.Cleanup(func() { internal.SetGoaHome("") })

	homeCfgPath := filepath.Join(homeDir, ".goa", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(homeCfgPath), 0755); err != nil {
		t.Fatalf("mkdir home .goa: %v", err)
	}
	if err := os.WriteFile(homeCfgPath, []byte(`
active_provider: home-provider
active_model: model-a
`), 0644); err != nil {
		t.Fatalf("write home config: %v", err)
	}

	loader := config.NewCascadeLoader(projectDir, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	pm := provider.NewProviderManager(cfg)
	s := &subsystems{cfg: cfg, loader: loader, providerMgr: pm}
	s.startConfigWatcher()
	defer s.stopConfigWatcher()

	if got := pm.Config().ActiveModel; got != "model-a" {
		t.Fatalf("boot ActiveModel = %q, want model-a", got)
	}

	// External edit: change the model.
	if err := os.WriteFile(homeCfgPath, []byte(`
active_provider: home-provider
active_model: model-b
`), 0644); err != nil {
		t.Fatalf("edit home config: %v", err)
	}

	// Wait for the provider profile to swap on the next request resolution.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if pm.Config().ActiveModel == "model-b" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("provider profile did not pick up the edited model within 5s")
}

// TestSubsystems_ConfigWatcher_BrokenEditKeepsLastGood verifies a broken YAML
// edit keeps serving last-good: the provider profile stays on the boot model
// while the file is broken, and recovers once fixed.
func TestSubsystems_ConfigWatcher_BrokenEditKeepsLastGood(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	internal.SetGoaHome(homeDir)
	t.Cleanup(func() { internal.SetGoaHome("") })

	homeCfgPath := filepath.Join(homeDir, ".goa", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(homeCfgPath), 0755); err != nil {
		t.Fatalf("mkdir home .goa: %v", err)
	}
	if err := os.WriteFile(homeCfgPath, []byte(`
active_provider: home-provider
active_model: model-a
`), 0644); err != nil {
		t.Fatalf("write home config: %v", err)
	}

	loader := config.NewCascadeLoader(projectDir, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	pm := provider.NewProviderManager(cfg)
	s := &subsystems{cfg: cfg, loader: loader, providerMgr: pm}
	s.startConfigWatcher()
	defer s.stopConfigWatcher()

	// Broken edit.
	if err := os.WriteFile(homeCfgPath, []byte("active_model: [unclosed\n  bad: :::"), 0644); err != nil {
		t.Fatalf("write broken config: %v", err)
	}
	// Give the watcher time to (fail to) reload; the profile must stay on the
	// last good model.
	time.Sleep(600 * time.Millisecond)
	if got := pm.Config().ActiveModel; got != "model-a" {
		t.Fatalf("after broken edit ActiveModel = %q, want model-a (last-good)", got)
	}

	// Fix the file: the next request resolves the new model.
	if err := os.WriteFile(homeCfgPath, []byte(`
active_provider: home-provider
active_model: model-fixed
`), 0644); err != nil {
		t.Fatalf("fix config: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if pm.Config().ActiveModel == "model-fixed" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("provider profile did not recover after fixing the config within 5s")
}

// TestSubsystems_LiveConfig verifies liveConfig returns the hot-reloaded
// config once the provider manager has one, else the boot config.
func TestSubsystems_LiveConfig(t *testing.T) {
	boot := &config.Config{ActiveModel: "boot"}
	newCfg := &config.Config{ActiveModel: "hot"}
	pm := provider.NewProviderManager(boot)

	s := &subsystems{cfg: boot, providerMgr: pm}
	if got := s.liveConfig(); got != boot {
		t.Errorf("liveConfig before reload = %v, want boot", got)
	}

	pm.SetConfig(newCfg)
	if got := s.liveConfig(); got != newCfg {
		t.Errorf("liveConfig after reload = %v, want hot config", got)
	}

	var nilS *subsystems
	if got := nilS.liveConfig(); got != nil {
		t.Errorf("nil liveConfig = %v, want nil", got)
	}
}
