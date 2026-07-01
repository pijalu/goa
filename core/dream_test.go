// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/protocol"
	"github.com/pijalu/goa/memory"
)

func TestDreamEngine_Run_NoMemories(t *testing.T) {
	dir := t.TempDir()
	store := memory.NewMemoryStore(dir, "")

	engine := NewDreamEngine(
		&config.Config{Memory: config.MemoryConfig{Enabled: true}},
		&fakeProviderResolver{},
		store,
		nil,
		dir,
		"",
	)

	result, err := engine.Run(context.Background(), false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Changed {
		t.Fatalf("expected no change when no memories exist")
	}
}

func TestDreamEngine_Run_Stream(t *testing.T) {
	dir := t.TempDir()
	store := memory.NewMemoryStore(dir, "")
	if err := store.Write("facts", "summary: test summary\n\n# Facts\n\n- fact 1"); err != nil {
		t.Fatalf("write memory: %v", err)
	}

	resolver := &fakeProviderResolver{model: provider.Model{Provider: "test", Api: provider.ApiOpenAICompletions}}
	engine := NewDreamEngine(
		&config.Config{
			Memory:         config.MemoryConfig{Enabled: true},
			ActiveProvider: "test",
			ActiveModel:    "test-model",
		},
		resolver,
		store,
		nil,
		dir,
		"",
	)

	result, err := engine.Run(context.Background(), false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Changed {
		t.Fatalf("expected change")
	}
	if result.InputMemories != 1 {
		t.Fatalf("expected 1 memory, got %d", result.InputMemories)
	}
	if _, err := os.Stat(result.OutputPath); err != nil {
		t.Fatalf("output file missing: %v", err)
	}
	data, _ := os.ReadFile(result.OutputPath)
	if !strings.Contains(string(data), "Consolidated Memory") {
		t.Fatalf("output missing expected content: %s", data)
	}
}

func TestDreamEngine_Apply(t *testing.T) {
	dir := t.TempDir()
	store := memory.NewMemoryStore(dir, "")
	if err := store.Write("facts", "summary: test\n\ncontent"); err != nil {
		t.Fatalf("write memory: %v", err)
	}

	outputDir := filepath.Join(dir, ".goa", "memory.dream")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	outputPath := filepath.Join(outputDir, "test-dream.md")
	if err := os.WriteFile(outputPath, []byte("# Consolidated\n"), 0644); err != nil {
		t.Fatalf("write output: %v", err)
	}

	engine := NewDreamEngine(
		&config.Config{Memory: config.MemoryConfig{Enabled: true}},
		&fakeProviderResolver{},
		store,
		nil,
		dir,
		"",
	)
	if err := engine.Apply(outputPath); err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	consolidated := filepath.Join(dir, ".goa", "memory.consolidated", "consolidated.md")
	data, err := os.ReadFile(consolidated)
	if err != nil {
		t.Fatalf("read consolidated: %v", err)
	}
	if string(data) != "# Consolidated\n" {
		t.Fatalf("unexpected consolidated content: %s", data)
	}

	backupDir := filepath.Join(dir, ".goa", "memory.backup")
	entries, err := os.ReadDir(backupDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("expected backup dir with entries, got %v", err)
	}
}

func TestDreamEngine_buildDreamPrompt(t *testing.T) {
	engine := NewDreamEngine(&config.Config{}, nil, nil, nil, "", "custom body")
	memories := []memoryFile{{Name: "facts.md", Content: "summary: test"}}
	prompt := engine.buildDreamPrompt(memories, nil)
	if !strings.Contains(prompt, "custom body") {
		t.Fatalf("expected custom skill body")
	}
	if !strings.Contains(prompt, "facts.md") {
		t.Fatalf("expected memory name")
	}
}

func TestDreamEngine_buildDreamPrompt_DefaultBody(t *testing.T) {
	engine := NewDreamEngine(&config.Config{}, nil, nil, nil, "", "")
	prompt := engine.buildDreamPrompt(nil, nil)
	if !strings.Contains(prompt, "memory curator") {
		t.Fatalf("expected default skill body")
	}
}

type fakeProviderResolver struct {
	model provider.Model
}

func (f *fakeProviderResolver) ResolveActiveModel() (provider.Model, error) {
	if f.model.Provider != "" {
		return f.model, nil
	}
	return provider.Model{Provider: "test", Api: provider.ApiOpenAICompletions}, nil
}

func (f *fakeProviderResolver) BuildStreamOptions() provider.StreamOptions {
	return provider.StreamOptions{MaxTokens: 1024}
}

// compile-time check that fakeProviderResolver implements the interface.
var _ ProviderResolver = (*fakeProviderResolver)(nil)

// fakeStreamProvider registers a fake ApiProvider so provider.Stream works.
type fakeStreamProvider struct{}

func (p *fakeStreamProvider) API() provider.Api { return provider.ApiOpenAICompletions }

func (p *fakeStreamProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	s := provider.NewAssistantMessageEventStream(8)
	s.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: "# Consolidated Memory\n"})
	s.Push(provider.AssistantMessageEvent{Type: provider.EventTextEnd})
	s.End(&provider.AssistantMessage{})
	return s, nil
}

func (p *fakeStreamProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(model, ctx, opts.StreamOptions)
}

func TestMain(m *testing.M) {
	provider.ClearApiProviders()
	protocol.Clear()
	provider.RegisterApiProvider(&fakeStreamProvider{})
	os.Exit(m.Run())
}
