// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package multiagent

import (
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider"
)

func TestAgentPool_GetOrCreate_CreatesAndCaches(t *testing.T) {
	p := NewAgentPool(testModel("default"), provider.StreamOptions{}, nil)

	agent, err := p.GetOrCreate("reviewer")
	if err != nil {
		t.Fatalf("GetOrCreate failed: %v", err)
	}
	if agent == nil {
		t.Fatal("GetOrCreate returned nil agent")
	}

	// Second call should return cached agent
	cached, err := p.GetOrCreate("reviewer")
	if err != nil {
		t.Fatalf("second GetOrCreate failed: %v", err)
	}
	if cached != agent {
		t.Error("second GetOrCreate returned different instance")
	}
}

func TestAgentPool_Get_ReturnsNilForMissing(t *testing.T) {
	p := NewAgentPool(testModel("default"), provider.StreamOptions{}, nil)
	agent := p.Get("nonexistent")
	if agent != nil {
		t.Error("expected nil for non-existent role")
	}
}

func TestAgentPool_Get_AfterGetOrCreate(t *testing.T) {
	p := NewAgentPool(testModel("default"), provider.StreamOptions{}, nil)
	created, err := p.GetOrCreate("coder")
	if err != nil {
		t.Fatalf("GetOrCreate failed: %v", err)
	}

	fetched := p.Get("coder")
	if fetched != created {
		t.Error("Get returned different agent than GetOrCreate")
	}
}

func TestAgentPool_DifferentRoles_GetDifferentAgents(t *testing.T) {
	p := NewAgentPool(testModel("default"), provider.StreamOptions{}, nil)

	reviewer, err := p.GetOrCreate("reviewer")
	if err != nil {
		t.Fatalf("GetOrCreate reviewer: %v", err)
	}
	coder, err := p.GetOrCreate("coder")
	if err != nil {
		t.Fatalf("GetOrCreate coder: %v", err)
	}

	if reviewer == coder {
		t.Error("reviewer and coder should be different agent instances")
	}
}

func TestAgentPool_DifferentModelPerRole_UsesModelFactory(t *testing.T) {
	p := NewAgentPool(testModel("default"), provider.StreamOptions{}, nil)

	var resolvedModels []string
	p.ModelFactory = func(modelName string) (provider.Model, error) {
		resolvedModels = append(resolvedModels, modelName)
		return testModel(modelName), nil
	}

	p.SetConfig("reviewer", AgentConfig{ModelName: "gpt-4"})
	p.SetConfig("coder", AgentConfig{ModelName: "gpt-3.5"})

	_, err := p.GetOrCreate("reviewer")
	if err != nil {
		t.Fatalf("GetOrCreate reviewer: %v", err)
	}
	if len(resolvedModels) != 1 || resolvedModels[0] != "gpt-4" {
		t.Errorf("expected ModelFactory called with 'gpt-4', got %v", resolvedModels)
	}

	_, err = p.GetOrCreate("coder")
	if err != nil {
		t.Fatalf("GetOrCreate coder: %v", err)
	}
	if len(resolvedModels) != 2 || resolvedModels[1] != "gpt-3.5" {
		t.Errorf("expected ModelFactory called with 'gpt-3.5', got %v", resolvedModels)
	}
}

func TestAgentPool_DefaultModel_WhenNoConfig(t *testing.T) {
	p := NewAgentPool(testModel("default-model"), provider.StreamOptions{}, nil)

	agent, err := p.GetOrCreate("planner")
	if err != nil {
		t.Fatalf("GetOrCreate planner: %v", err)
	}
	if agent == nil {
		t.Fatal("expected non-nil agent")
	}
}

func TestAgentPool_Roles(t *testing.T) {
	p := NewAgentPool(testModel("default"), provider.StreamOptions{}, nil)
	p.GetOrCreate("reviewer")
	p.GetOrCreate("coder")

	roles := p.Roles()
	if len(roles) != 2 {
		t.Errorf("expected 2 roles, got %d: %v", len(roles), roles)
	}
}

func TestAgentPool_SetGoaConfig_Stored(t *testing.T) {
	p := NewAgentPool(testModel("default"), provider.StreamOptions{}, nil)
	cfg := &config.Config{}
	p.SetGoaConfig(cfg)
	if p.Config != cfg {
		t.Error("SetGoaConfig did not store the config")
	}
}

func TestAgentPool_GetOrCreate_InheritsGoaConfig(t *testing.T) {
	trueVal := true
	cfg := &config.Config{
		Execution: config.ExecutionConfig{MaxToolRepeatTotal: 5},
		Skills:    config.SkillsConfig{ExecutionMode: config.AgenticSkillModeInline},
		ContextCompression: config.ContextCompressionConfig{
			Enabled:             true,
			MaxTokens:           4096,
			ThresholdPercent:    75,
			OnContextError:      true,
			Strategy:            config.AgenticCompressionToolElision,
			PreserveRecentTurns: 3,
		},
	}
	cfg.Providers = []config.ProviderConfig{{ID: "p", ToolResultAsUser: &trueVal}}
	cfg.ActiveProvider = "p"

	var created *agentic.Agent
	p := NewAgentPool(testModel("default"), provider.StreamOptions{}, nil)
	p.SetGoaConfig(cfg)
	p.OnAgentCreated = func(role string, agent *agentic.Agent) {
		created = agent
	}

	agent, err := p.GetOrCreate("reviewer")
	if err != nil {
		t.Fatalf("GetOrCreate failed: %v", err)
	}
	if agent != created {
		t.Fatal("OnAgentCreated was not called with the created agent")
	}
	if agent == nil {
		t.Fatal("expected non-nil agent")
	}
}

func TestAgentPool_GetOrCreate_SystemPromptFallback(t *testing.T) {
	p := NewAgentPool(testModel("default"), provider.StreamOptions{}, nil)
	agent, err := p.GetOrCreate("reviewer")
	if err != nil {
		t.Fatalf("GetOrCreate failed: %v", err)
	}
	if agent == nil {
		t.Fatal("expected non-nil agent")
	}
}

func TestAgentPool_ProviderModelFactory_UsedWhenProviderIDSet(t *testing.T) {
	p := NewAgentPool(testModel("default"), provider.StreamOptions{}, nil)

	var calledWith []string
	p.ProviderModelFactory = func(providerID, modelName string) (provider.Model, error) {
		calledWith = append(calledWith, providerID, modelName)
		return testModel(modelName), nil
	}

	p.SetConfig("companion", AgentConfig{
		ModelName:  "companion-model",
		ProviderID: "remote",
	})

	_, err := p.GetOrCreate("companion")
	if err != nil {
		t.Fatalf("GetOrCreate companion: %v", err)
	}
	if len(calledWith) != 2 || calledWith[0] != "remote" || calledWith[1] != "companion-model" {
		t.Errorf("ProviderModelFactory called with %v, want [remote companion-model]", calledWith)
	}
}

func TestAgentPool_SetConfig_UsedOnGetOrCreate(t *testing.T) {
	var resolvedModel string
	p := NewAgentPool(testModel("default"), provider.StreamOptions{}, nil)
	p.ModelFactory = func(modelName string) (provider.Model, error) {
		resolvedModel = modelName
		return testModel(modelName), nil
	}
	p.SetConfig("reviewer", AgentConfig{
		ModelName:    "claude-3",
		SystemPrompt: "You are a test reviewer",
	})

	agent, err := p.GetOrCreate("reviewer")
	if err != nil {
		t.Fatalf("GetOrCreate reviewer: %v", err)
	}
	if agent == nil {
		t.Fatal("agent is nil")
	}
	if resolvedModel != "claude-3" {
		t.Errorf("expected model 'claude-3', got %q", resolvedModel)
	}
}
