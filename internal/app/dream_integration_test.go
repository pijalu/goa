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
	"github.com/pijalu/goa/core"
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
	core.ResetGlobalRegistry()

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

	loader := config.NewCascadeLoader(dir, "", nil)
	subs := InitSubsystems(cfg, loader, dir, RuntimeOptions{})

	var out bytes.Buffer
	runDream(subs, RuntimeOptions{Dream: true})

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
	core.ResetGlobalRegistry()

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

	loader := config.NewCascadeLoader(dir, "", nil)
	subs := InitSubsystems(cfg, loader, dir, RuntimeOptions{})

	// Override context to avoid timeout during provider test registration.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = ctx

	runDream(subs, RuntimeOptions{DreamApply: true})

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
