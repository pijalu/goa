// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/internal"
)

// logCapture is a concurrency-safe log sink for watcher tests: the watcher
// logs from its own goroutine while the test asserts from the main one.
type logCapture struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *logCapture) Writef(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.b.WriteString(format + "\n")
}

func (l *logCapture) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// newWatcherTestEnv creates a temp home + project, points the goa home at the
// temp home, writes an initial home config, and returns the loader, the watcher
// (started), and a log sink. Callers must call cleanup.
func newWatcherTestEnv(t *testing.T) (*CascadeLoader, *ConfigWatcher, *logCapture, func()) {
	t.Helper()
	homeDir, projectDir, cleanup := setupTestConfig(t)
	internal.SetGoaHome(homeDir)
	t.Cleanup(func() { internal.SetGoaHome("") })

	homeCfg := filepath.Join(homeDir, ".goa", "config.yaml")
	writeConfig(t, homeCfg, `
active_provider: home-provider
active_model: model-a
`)
	// Create the project .goa dir so the watcher can watch the project/local
	// layers too (matching a project that already has .goa prompts/skills or a
	// prior saved config).
	os.MkdirAll(filepath.Join(projectDir, ".goa"), 0755)
	loader := NewCascadeLoader(projectDir, "", nil)
	if _, err := loader.Load(); err != nil {
		t.Fatalf("initial Load failed: %v", err)
	}

	logs := &logCapture{}
	w, err := NewConfigWatcher(loader, logs.Writef)
	if err != nil {
		cleanup()
		t.Fatalf("NewConfigWatcher: %v", err)
	}
	w.Start()

	return loader, w, logs, func() {
		w.Close()
		cleanup()
	}
}

// waitForConfig waits up to 5s for a config to arrive on the changes channel.
func waitForConfig(t *testing.T, w *ConfigWatcher) *Config {
	t.Helper()
	select {
	case cfg := <-w.Changes():
		return cfg
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for config change")
		return nil
	}
}

// expectNoChange asserts no config is published within a quiet window. Used to
// verify self-writes and unchanged content do not trigger reloads.
func expectNoChange(t *testing.T, w *ConfigWatcher, d time.Duration) {
	t.Helper()
	select {
	case cfg := <-w.Changes():
		t.Fatalf("unexpected config change published: %+v", cfg)
	case <-time.After(d):
	}
}

// TestConfigWatcher_AppliesHomeModelEdit is the acceptance test: editing
// ~/.goa/config.yaml's model applies without restart.
func TestConfigWatcher_AppliesHomeModelEdit(t *testing.T) {
	_, w, _, cleanup := newWatcherTestEnv(t)
	defer cleanup()

	homeCfg := filepath.Join(w.cl.HomeConfigPath())
	writeConfig(t, homeCfg, `
active_provider: home-provider
active_model: model-b
`)
	cfg := waitForConfig(t, w)
	if cfg.ActiveModel != "model-b" {
		t.Errorf("ActiveModel = %q, want %q (edit should apply without restart)", cfg.ActiveModel, "model-b")
	}
	if cfg.ActiveProvider != "home-provider" {
		t.Errorf("ActiveProvider = %q, want %q", cfg.ActiveProvider, "home-provider")
	}
}

// TestConfigWatcher_AppliesHomeEffortEdit verifies a thinking/effort edit on the
// active model applies without restart (P22 acceptance: model/effort).
func TestConfigWatcher_AppliesHomeEffortEdit(t *testing.T) {
	_, w, _, cleanup := newWatcherTestEnv(t)
	defer cleanup()

	writeConfig(t, w.cl.HomeConfigPath(), `
active_provider: home-provider
active_model: model-a
thinking_levels:
  main_agent: high
`)
	cfg := waitForConfig(t, w)
	if got := cfg.GetReasoningEffort(); got != "high" {
		t.Errorf("GetReasoningEffort() = %q, want %q", got, "high")
	}
}

// TestConfigWatcher_BrokenYAMLKeepsLastGood is the acceptance test: a broken
// edit keeps serving last-good and logs a warning.
func TestConfigWatcher_BrokenYAMLKeepsLastGood(t *testing.T) {
	_, w, logs, cleanup := newWatcherTestEnv(t)
	defer cleanup()

	writeConfig(t, w.cl.HomeConfigPath(), "active_model: [unclosed\n  bad: :::")
	expectNoChange(t, w, 600*time.Millisecond)

	if !strings.Contains(logs.String(), "config hot-reload failed") {
		t.Errorf("expected a warning log about the broken edit, got: %q", logs.String())
	}
}

// TestConfigWatcher_RecoversAfterFix verifies a broken edit is followed by a
// successful reload once the file is fixed (last-good until then).
func TestConfigWatcher_RecoversAfterFix(t *testing.T) {
	_, w, logs, cleanup := newWatcherTestEnv(t)
	defer cleanup()

	homeCfg := w.cl.HomeConfigPath()
	writeConfig(t, homeCfg, "active_model: [unclosed\n  bad: :::")
	expectNoChange(t, w, 600*time.Millisecond)

	writeConfig(t, homeCfg, `
active_provider: home-provider
active_model: model-fixed
`)
	cfg := waitForConfig(t, w)
	if cfg.ActiveModel != "model-fixed" {
		t.Errorf("ActiveModel = %q, want %q", cfg.ActiveModel, "model-fixed")
	}
	if !strings.Contains(logs.String(), "config hot-reload failed") {
		t.Errorf("expected a warning log about the broken edit, got: %q", logs.String())
	}
}

// TestConfigWatcher_ProjectLayerEdit verifies a project .goa/config.yaml edit
// is hot-published.
func TestConfigWatcher_ProjectLayerEdit(t *testing.T) {
	_, w, _, cleanup := newWatcherTestEnv(t)
	defer cleanup()

	writeConfig(t, filepath.Join(w.cl.projectDir, ".goa", "config.yaml"), `
active_provider: project-provider
active_model: project-model
`)
	cfg := waitForConfig(t, w)
	if cfg.ActiveModel != "project-model" {
		t.Errorf("ActiveModel = %q, want %q", cfg.ActiveModel, "project-model")
	}
	if cfg.ActiveProvider != "project-provider" {
		t.Errorf("ActiveProvider = %q, want %q", cfg.ActiveProvider, "project-provider")
	}
}

// TestConfigWatcher_LocalLayerEdit verifies a project .goa/config.local.yaml
// edit is hot-published.
func TestConfigWatcher_LocalLayerEdit(t *testing.T) {
	_, w, _, cleanup := newWatcherTestEnv(t)
	defer cleanup()

	writeConfig(t, filepath.Join(w.cl.projectDir, ".goa", "config.local.yaml"), `
active_provider: local-provider
active_model: local-model
`)
	cfg := waitForConfig(t, w)
	if cfg.ActiveModel != "local-model" {
		t.Errorf("ActiveModel = %q, want %q", cfg.ActiveModel, "local-model")
	}
}

// TestConfigWatcher_SelfWriteSuppressed verifies that a Save producing
// byte-identical content is not published as a change (duplicate events with
// identical content are no-ops).
func TestConfigWatcher_SelfWriteSuppressed(t *testing.T) {
	loader, w, _, cleanup := newWatcherTestEnv(t)
	defer cleanup()

	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := loader.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// The first Save rewrites the file from the minimal boot YAML to the full
	// marshaled config — that content change legitimately publishes.
	select {
	case <-w.Changes():
	case <-time.After(5 * time.Second):
		t.Fatal("first Save should publish (content changed)")
	}

	// A second identical Save writes the same bytes: must NOT publish.
	if err := loader.Save(cfg); err != nil {
		t.Fatalf("Save #2: %v", err)
	}
	expectNoChange(t, w, 600*time.Millisecond)
}

// TestConfigWatcher_CloseIdempotent verifies Close stops the event loop and
// can be called twice without panicking.
func TestConfigWatcher_CloseIdempotent(t *testing.T) {
	_, w, _, cleanup := newWatcherTestEnv(t)
	defer cleanup()

	w.Close()
	w.Close()
}

// TestConfigWatcher_NoChangeOnUnrelatedFile verifies events for files outside
// the writable layers are ignored.
func TestConfigWatcher_NoChangeOnUnrelatedFile(t *testing.T) {
	_, w, _, cleanup := newWatcherTestEnv(t)
	defer cleanup()

	other := filepath.Join(w.cl.homeDir, ".goa", "trust.json")
	if err := os.WriteFile(other, []byte("{}"), 0644); err != nil {
		t.Fatalf("write unrelated file: %v", err)
	}
	expectNoChange(t, w, 600*time.Millisecond)
}

// TestConfigWatcher_ExplicitConfigPath verifies the watcher follows the
// explicit --config file rather than the project cascade file.
func TestConfigWatcher_ExplicitConfigPath(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()
	internal.SetGoaHome(homeDir)
	t.Cleanup(func() { internal.SetGoaHome("") })

	explicitPath := filepath.Join(homeDir, "custom.yaml")
	writeConfig(t, explicitPath, `
active_provider: explicit-provider
active_model: explicit-model
`)
	loader := NewCascadeLoader(projectDir, explicitPath, nil)
	if _, err := loader.Load(); err != nil {
		t.Fatalf("initial Load failed: %v", err)
	}

	w, err := NewConfigWatcher(loader, (&logCapture{}).Writef)
	if err != nil {
		t.Fatalf("NewConfigWatcher: %v", err)
	}
	w.Start()
	defer w.Close()

	writeConfig(t, explicitPath, `
active_provider: explicit-provider
active_model: explicit-model-2
`)
	cfg := waitForConfig(t, w)
	if cfg.ActiveModel != "explicit-model-2" {
		t.Errorf("ActiveModel = %q, want %q", cfg.ActiveModel, "explicit-model-2")
	}
}
