// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/memory"
)

// dreamIntegrationProvider is a mock API provider that returns a simple
// consolidated memory response for dream integration tests.
type dreamIntegrationProvider struct {
	api provider.Api
}

func (p *dreamIntegrationProvider) API() provider.Api { return p.api }

func (p *dreamIntegrationProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	result := provider.NewAssistantMessageEventStream(16)
	go func() {
		result.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: "# Consolidated Memory\n\n## Architecture\n\n- fact\n"})
		result.Push(provider.AssistantMessageEvent{Type: provider.EventTextEnd})
		result.End(&provider.AssistantMessage{})
	}()
	return result, nil
}

func (p *dreamIntegrationProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(model, ctx, opts.StreamOptions)
}

func TestRunDream_Headless(t *testing.T) {
	api := provider.Api(fmt.Sprintf("test-dream-%d", time.Now().UnixNano()))
	provider.RegisterApiProvider(&dreamIntegrationProvider{api: api})

	dir := t.TempDir()
	store := memory.NewMemoryStore(dir, "")
	if err := store.Write("facts", "summary: facts\n\n## Facts\n\n- old"); err != nil {
		t.Fatalf("write memory: %v", err)
	}

	cfg := &config.Config{
		Providers: []config.ProviderConfig{
			{ID: "test", API: string(api), Endpoint: "http://localhost:9999", APIKey: "test"},
		},
		Models: []config.ModelConfig{
			{ID: "test-model", ProviderID: "test", Model: "test-model", API: string(api)},
		},
		ActiveProvider: "test",
		ActiveModel:    "test-model",
		Memory:         config.MemoryConfig{Enabled: true},
	}

	// The embedded dream skill is OFF by default like every other built-in, so
	// this test opts it in — exactly what a user does before running --dream.
	cfg.Skills.EmbeddedEnabled = []string{"dream"}

	loader := config.NewCascadeLoader(dir, "", nil)
	subs := InitSubsystems(cfg, loader, dir, RuntimeOptions{})

	var out bytes.Buffer
	if err := executeDream(subs, RuntimeOptions{Dream: true}); err != nil {
		t.Fatalf("executeDream: %v", err)
	}

	got := out.String()
	if got != "" {
		t.Logf("runDream output: %s", got)
	}

	dreamDir := filepath.Join(dir, ".goa", "memory.dream")
	entries, err := os.ReadDir(dreamDir)
	if err != nil {
		t.Fatalf("read memory.dream dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("expected dream output file")
	}

	found := false
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), "-dream.md") {
			found = true
			data, err := os.ReadFile(filepath.Join(dreamDir, e.Name()))
			if err != nil {
				t.Fatalf("read dream output: %v", err)
			}
			if !strings.Contains(string(data), "Consolidated Memory") {
				t.Fatalf("unexpected dream output: %s", data)
			}
		}
	}
	if !found {
		t.Fatalf("expected *-dream.md file in %s", dreamDir)
	}
}

func TestRunDream_WithApply(t *testing.T) {
	api := provider.Api(fmt.Sprintf("test-dream-apply-%d", time.Now().UnixNano()))
	provider.RegisterApiProvider(&dreamIntegrationProvider{api: api})

	dir := t.TempDir()
	store := memory.NewMemoryStore(dir, "")
	if err := store.Write("facts", "summary: facts\n\n## Facts\n\n- old"); err != nil {
		t.Fatalf("write memory: %v", err)
	}

	cfg := &config.Config{
		Providers: []config.ProviderConfig{
			{ID: "test", API: string(api), Endpoint: "http://localhost:9999", APIKey: "test"},
		},
		Models: []config.ModelConfig{
			{ID: "test-model", ProviderID: "test", Model: "test-model", API: string(api)},
		},
		ActiveProvider: "test",
		ActiveModel:    "test-model",
		Memory:         config.MemoryConfig{Enabled: true},
	}

	// The embedded dream skill is OFF by default like every other built-in, so
	// this test opts it in — exactly what a user does before running the CLI mode.
	cfg.Skills.EmbeddedEnabled = []string{"dream"}

	loader := config.NewCascadeLoader(dir, "", nil)
	subs := InitSubsystems(cfg, loader, dir, RuntimeOptions{})

	// Override context to avoid timeout during provider test registration.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = ctx

	if err := executeDream(subs, RuntimeOptions{DreamApply: true}); err != nil {
		t.Fatalf("executeDream: %v", err)
	}

	consolidated := filepath.Join(dir, ".goa", "memory.consolidated", "consolidated.md")
	if _, err := os.Stat(consolidated); err != nil {
		t.Fatalf("consolidated file missing: %v", err)
	}
	backupDir := filepath.Join(dir, ".goa", "memory.backup")
	entries, err := os.ReadDir(backupDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("expected backup dir with entries, got %v", err)
	}
}

// TestDreamCLI_SkillDisabledReportsHowToEnable: with the shipped defaults the
// embedded dream skill is not registered, so the CLI path must return an
// actionable message — naming skills.embedded_enabled / /config → Skills and the
// restart caveat — instead of the old bare "dream skill not found".
func TestDreamCLI_SkillDisabledReportsHowToEnable(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{Memory: config.MemoryConfig{Enabled: true}}
	loader := config.NewCascadeLoader(dir, "", nil)
	subs := InitSubsystems(cfg, loader, dir, RuntimeOptions{})
	if subs.skillRegistry == nil {
		t.Fatal("skill registry not wired")
	}
	if _, ok := subs.skillRegistry.Get("dream"); ok {
		t.Fatal("precondition: dream must be OFF by default")
	}

	err := executeDream(subs, RuntimeOptions{Dream: true})
	if err == nil {
		t.Fatal("executeDream must fail while the dream skill is disabled")
	}
	msg := err.Error()
	for _, want := range []string{"dream is disabled", "skills.embedded_enabled", "restart"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message must contain %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "dream skill not found") {
		t.Errorf("the bare not-found message must be gone:\n%s", msg)
	}
}

// TestReloadSkills_PicksUpEmbeddedEnabled: ReloadSkills must refresh the
// embedded opt-in list from disk like the other skill lists, so an in-session
// skill toggle produces exactly the registry a fresh start would build.
func TestReloadSkills_PicksUpEmbeddedEnabled(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	loader := config.NewCascadeLoader(project, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.Skills.EmbeddedEnabled) != 0 {
		t.Fatalf("precondition: no embedded opt-ins, got %v", cfg.Skills.EmbeddedEnabled)
	}
	subs := InitSubsystems(cfg, loader, project, RuntimeOptions{})
	if _, ok := subs.skillRegistry.Get("telegram"); ok {
		t.Fatal("precondition: telegram must be OFF by default")
	}

	// Simulate the persisted opt-in a toggle writes, WITHOUT touching the live
	// config: the reload must read the on-disk state. SaveHomeFieldValue is the
	// same call persistSkillToggle makes for the embedded opt-in list.
	if err := loader.SaveHomeFieldValue([]string{"skills", "embedded_enabled"}, []string{"telegram"}); err != nil {
		t.Fatalf("persist opt-in: %v", err)
	}

	h := &ReloadHandler{subs: subs}
	if _, err := h.ReloadSkills(); err != nil {
		t.Fatalf("ReloadSkills: %v", err)
	}
	// The reload count is 1 (only the opted-in skill) — every embedded skill
	// being OFF by default is exactly why a bare count assertion is meaningless.
	if !stringInSliceForTest(subs.cfg.Skills.EmbeddedEnabled, "telegram") {
		t.Errorf("ReloadSkills must refresh Skills.EmbeddedEnabled, got %v", subs.cfg.Skills.EmbeddedEnabled)
	}
	skill, ok := subs.skillRegistry.Get("telegram")
	if !ok {
		t.Fatal("telegram must be loaded in-session after the reload")
	}
	if !skill.IsSticky() {
		t.Error("the opted-in telegram skill must keep its sticky (always-on) body")
	}
}

func stringInSliceForTest(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
