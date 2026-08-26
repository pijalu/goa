// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/pijalu/goa/internal"
)

// TestConfigDeserializeFromYAML verifies Config struct deserializes from YAML.

// TestConfigValidateActiveProvider verifies provider existence check.
func TestConfigValidateActiveProvider(t *testing.T) {
	cfg := &Config{
		ActiveProvider: "nonexistent",
		Execution:      ExecutionConfig{Mode: internal.ExecutionYolo, WorktreeMode: internal.WorktreeAlways},
		Providers:      []ProviderConfig{{ID: "openai"}},
	}
	err := cfg.Validate()
	if err == nil {
		t.Error("Expected error for nonexistent active_provider")
	}
}

// TestConfigValidateTimeout verifies duration parsing.

// TestGetProviderByID verifies provider lookup.
func TestGetProviderByID(t *testing.T) {
	cfg := &Config{
		Providers: []ProviderConfig{
			{ID: "openai"},
			{ID: "anthropic"},
		},
	}
	if p := cfg.GetProviderByID("openai"); p == nil {
		t.Error("GetProviderByID('openai') should find provider")
	}
	if p := cfg.GetProviderByID("nonexistent"); p != nil {
		t.Error("GetProviderByID('nonexistent') should return nil")
	}
}

// TestPreferredProvider verifies preferred provider selection.

// TestPreferredProvider verifies preferred provider selection.
func TestPreferredProvider(t *testing.T) {
	cfg := &Config{
		Providers: []ProviderConfig{
			{ID: "openai"},
			{ID: "anthropic", Preferred: true},
		},
	}
	p := cfg.PreferredProvider()
	if p == nil || p.ID != "anthropic" {
		t.Errorf("PreferredProvider = %v, want anthropic", p)
	}
}

// TestPreferredProviderEmpty verifies nil return when no providers.

// TestPreferredProviderEmpty verifies nil return when no providers.
func TestPreferredProviderEmpty(t *testing.T) {
	cfg := &Config{}
	if p := cfg.PreferredProvider(); p != nil {
		t.Error("PreferredProvider should return nil with no providers")
	}
}

// TestDeepMergeScalars verifies scalar fields are overwritten.

func TestConfigDeserialize_AgenticFields(t *testing.T) {
	cfg := unmarshalAgenticConfig(t)
	assertProvider(t, cfg)
	assertModel(t, cfg)
	assertExecution(t, cfg)
	assertContextCompression(t, cfg)
}

func unmarshalAgenticConfig(t *testing.T) Config {
	t.Helper()
	y := agenticConfigYAML()
	var cfg Config
	if err := yaml.Unmarshal([]byte(y), &cfg); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	return cfg
}

func agenticConfigYAML() string {
	return `
providers:
  - id: openai
    provider: openai
    api: openai-completions
    base_url: https://api.openai.com/v1
    transport: sse
    cache_retention: short
    session_id: sess-1
    metadata:
      project: goa
    max_retry_delay: 2s
    reasoning_effort: low
    retry_policy:
      mode: always
      max_retries: 7
      backoff:
        initial_ms: 500
        max_ms: 5000
        jitter: 0.2
      codes:
        - RATE_LIMIT
        - SERVER
models:
  - id: gpt-4o
    provider: openai
    model: gpt-4o
    api: openai-completions
    provider_name: openai
    reasoning: true
    thinking_level: medium
    thinking_budget: 512
    input_types:
      - text
    headers:
      X-Model: "1"
    compat: '{"toolResultAsUser":true}'
execution:
  mode: yolo
  max_tool_repeat_total: 5
skills:
  execution_mode: inline
context_compression:
  enabled: true
  max_tokens: 8192
  threshold_percent: 80
  on_context_error: true
  strategy: tool_elision
  preserve_recent_turns: 3
`
}

// fieldCheck pairs an expected fixture value (want) with what the parsed
// config actually produced (got).

// fieldCheck pairs an expected fixture value (want) with what the parsed
// config actually produced (got).
type fieldCheck struct {
	name string
	got  any
	want any
}

// assertFields compares each check with reflect.DeepEqual.

// assertFields compares each check with reflect.DeepEqual.
func assertFields(t *testing.T, checks []fieldCheck) {
	t.Helper()
	for _, c := range checks {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

// assertProvider verifies every deserialized provider field of the canonical
// YAML fixture: flat attributes first, then the retry policy.

// assertProvider verifies every deserialized provider field of the canonical
// YAML fixture: flat attributes first, then the retry policy.
func assertProvider(t *testing.T, cfg Config) {
	t.Helper()
	if len(cfg.Providers) != 1 {
		t.Fatalf("Providers = %d, want 1", len(cfg.Providers))
	}
	p := cfg.Providers[0]
	assertFields(t, []fieldCheck{
		{"Provider.Provider", p.Provider, AgenticProviderOpenAI},
		{"Provider.API", p.API, AgenticAPIOpenAICompletions},
		{"Provider.Transport", p.Transport, AgenticTransportSSE},
		{"Provider.CacheRetention", p.CacheRetention, AgenticCacheRetentionShort},
		{"Provider.SessionID", p.SessionID, "sess-1"},
		{"Provider.Metadata project", p.Metadata["project"], "goa"},
		{"Provider.MaxRetryDelay", p.MaxRetryDelay, "2s"},
		{"Provider.ReasoningEffort", p.ReasoningEffort, "low"},
	})
	if p.RetryPolicy == nil {
		t.Fatal("Provider.RetryPolicy should be set")
	}
	assertRetryPolicy(t, *p.RetryPolicy)
}

// assertRetryPolicy verifies the retry policy of the canonical YAML fixture.

// assertRetryPolicy verifies the retry policy of the canonical YAML fixture.
func assertRetryPolicy(t *testing.T, rp RetryPolicyConfig) {
	t.Helper()
	assertFields(t, []fieldCheck{
		{"Provider.RetryPolicy.Mode", rp.Mode, "always"},
		{"Provider.RetryPolicy.MaxRetries", rp.MaxRetries, 7},
		{"Provider.RetryPolicy.Backoff.InitialMS", rp.Backoff.InitialMS, 500},
		{"Provider.RetryPolicy.Backoff.MaxMS", rp.Backoff.MaxMS, 5000},
		{"Provider.RetryPolicy.Backoff.Jitter", rp.Backoff.Jitter, 0.2},
		{"Provider.RetryPolicy.Codes", rp.Codes, []string{"RATE_LIMIT", "SERVER"}},
	})
}

func assertModel(t *testing.T, cfg Config) {
	t.Helper()
	if len(cfg.Models) != 1 {
		t.Fatalf("Models = %d, want 1", len(cfg.Models))
	}
	m := cfg.Models[0]
	if m.Reasoning == nil || !*m.Reasoning {
		t.Error("Model.Reasoning should be true")
	}
	if m.ThinkingLevel != AgenticThinkingMedium {
		t.Errorf("Model.ThinkingLevel = %q, want %q", m.ThinkingLevel, AgenticThinkingMedium)
	}
	if m.ThinkingBudget != 512 {
		t.Errorf("Model.ThinkingBudget = %d, want 512", m.ThinkingBudget)
	}
	if len(m.InputTypes) != 1 || m.InputTypes[0] != "text" {
		t.Errorf("Model.InputTypes = %v, want [text]", m.InputTypes)
	}
	if m.Headers["X-Model"] != "1" {
		t.Errorf("Model.Headers X-Model = %q, want 1", m.Headers["X-Model"])
	}
}

func assertExecution(t *testing.T, cfg Config) {
	t.Helper()
	if cfg.Execution.MaxToolRepeatTotal != 5 {
		t.Errorf("Execution.MaxToolRepeatTotal = %d, want 5", cfg.Execution.MaxToolRepeatTotal)
	}
	if cfg.Skills.ExecutionMode != AgenticSkillModeInline {
		t.Errorf("Skills.ExecutionMode = %q, want %q", cfg.Skills.ExecutionMode, AgenticSkillModeInline)
	}
}

// TestMergeExecution_ToolCallLimitResetWindow verifies the new execution
