// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pijalu/goa/internal"
)

// TestGoaHome_ResolutionOrder verifies the home override cascade:
// flag (SetGoaHome) → GOA_HOME env → os.UserHomeDir().
func TestGoaHome_ResolutionOrder(t *testing.T) {
	realHome, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no user home dir")
	}

	// 3. Default: OS user home.
	internal.SetGoaHome("")
	t.Setenv("GOA_HOME", "")
	if got, ok := internal.GoaHome(); !ok || got != realHome {
		t.Errorf("internal.GoaHome() = %q,%v, want %q (OS home)", got, ok, realHome)
	}

	// 2. Env override beats the OS home.
	envHome := t.TempDir()
	t.Setenv("GOA_HOME", envHome)
	if got, ok := internal.GoaHome(); !ok || got != envHome {
		t.Errorf("internal.GoaHome() = %q,%v, want %q (GOA_HOME)", got, ok, envHome)
	}

	// 1. Flag override beats the env.
	flagHome := t.TempDir()
	internal.SetGoaHome(flagHome)
	defer internal.SetGoaHome("")
	if got, ok := internal.GoaHome(); !ok || got != flagHome {
		t.Errorf("internal.GoaHome() = %q,%v, want %q (--home flag)", got, ok, flagHome)
	}
}

// TestGoaHome_RelocatesConfigLoad verifies a home override relocates the
// cascade home layer and first-run detection: a config.yaml under the
// override root is picked up, and the real home is untouched.
func TestGoaHome_RelocatesConfigLoad(t *testing.T) {
	fakeHome := t.TempDir()
	internal.SetGoaHome(fakeHome)
	defer internal.SetGoaHome("")
	t.Setenv("GOA_HOME", "")

	goaDir := filepath.Join(fakeHome, ".goa")
	if err := os.MkdirAll(goaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goaDir, "config.yaml"),
		[]byte("active_provider: relocated-provider\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := NewCascadeLoader(t.TempDir(), "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ActiveProvider != "relocated-provider" {
		t.Errorf("ActiveProvider = %q, want relocated-provider (home override not applied)", cfg.ActiveProvider)
	}
	if loader.homeDir != fakeHome {
		t.Errorf("loader.homeDir = %q, want %q", loader.homeDir, fakeHome)
	}
}

// TestGoaHome_RelocatesFirstRunDetection verifies first-run detection looks
// under the overridden home: an override root without .goa/config.yaml is a
// first run, and one with the file is not.
func TestGoaHome_RelocatesFirstRunDetection(t *testing.T) {
	fakeHome := t.TempDir()
	internal.SetGoaHome(fakeHome)
	defer internal.SetGoaHome("")
	t.Setenv("GOA_HOME", "")

	loader := NewCascadeLoader(t.TempDir(), "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.FirstRun {
		t.Error("FirstRun = false under a fresh --home root, want true")
	}

	if err := os.MkdirAll(filepath.Join(fakeHome, ".goa"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fakeHome, ".goa", "config.yaml"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	loader = NewCascadeLoader(t.TempDir(), "", nil)
	cfg, err = loader.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.FirstRun {
		t.Error("FirstRun = true with config.yaml present under --home root, want false")
	}
}

// TestGoaHomeDir verifies the .goa suffix helper.
func TestGoaHomeDir(t *testing.T) {
	fakeHome := t.TempDir()
	internal.SetGoaHome(fakeHome)
	defer internal.SetGoaHome("")
	if got := internal.GoaHomeDir(); got != filepath.Join(fakeHome, ".goa") {
		t.Errorf("internal.GoaHomeDir() = %q, want %q", got, filepath.Join(fakeHome, ".goa"))
	}
}

// TestGoaHome_SkillDirsFollowOverride verifies DefaultSkillDirs resolves
// ~/.agents/skills under the overridden home.
func TestGoaHome_SkillDirsFollowOverride(t *testing.T) {
	fakeHome := t.TempDir()
	internal.SetGoaHome(fakeHome)
	defer internal.SetGoaHome("")
	t.Setenv("GOA_HOME", "")

	projectDir := t.TempDir()
	dirs := DefaultSkillDirs(projectDir)
	want := filepath.Join(fakeHome, ".agents", "skills")
	if len(dirs) != 2 || dirs[0] != want {
		t.Errorf("DefaultSkillDirs = %v, want first dir %q", dirs, want)
	}
}
