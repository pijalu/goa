// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal/agentic/provider"
)

// toolCaptureProvider records the tool schema names of the first streaming
// request so tests can assert exactly what was advertised to the model.
type toolCaptureProvider struct {
	api   provider.Api
	event []provider.AssistantMessageEvent

	mu    sync.Mutex
	tools []string
}

func (p *toolCaptureProvider) API() provider.Api { return p.api }

func (p *toolCaptureProvider) Stream(_ provider.Model, ctx provider.Context, _ provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	p.mu.Lock()
	if p.tools == nil {
		for _, t := range ctx.Tools {
			p.tools = append(p.tools, t.Name)
		}
	}
	p.mu.Unlock()
	result := provider.NewAssistantMessageEventStream(64)
	go func() {
		for _, event := range p.event {
			result.Push(event)
		}
		result.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "done"}},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return result, nil
}

func (p *toolCaptureProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(model, ctx, provider.BuildSimpleOptions(model, opts))
}

func (p *toolCaptureProvider) advertised() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.tools...)
}

const companionSeedState = `{"mode_state":{"major":"coder","autonomy":"yolo"},` +
	`"minor_mode":"companion","agent_driven_enabled":true,"thinking_level":"off"}`

// setupCaptureProject writes a fake project: seeded state (or empty) plus a
// config.yaml wiring the test provider. It returns the project dir and the
// capture provider after registering it.
func setupCaptureProject(t *testing.T, stateJSON string) (string, *toolCaptureProvider) {
	t.Helper()
	api := provider.Api(fmt.Sprintf("test-companion-%d", time.Now().UnixNano()))
	p := &toolCaptureProvider{
		api: api,
		event: []provider.AssistantMessageEvent{
			{Type: provider.EventTextStart, ContentIndex: 0},
			{Type: provider.EventTextDelta, ContentIndex: 0, Delta: "ok"},
			{Type: provider.EventTextEnd, ContentIndex: 0, Content: "ok"},
		},
	}
	provider.RegisterApiProvider(p)

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".goa"), 0o755); err != nil {
		t.Fatal(err)
	}
	if stateJSON != "" {
		if err := os.WriteFile(filepath.Join(dir, ".goa", "state.json"), []byte(stateJSON), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	projectCfg := "active_provider: test\nactive_model: test-model\n" +
		"providers:\n  - id: test\n    endpoint: http://localhost:9999\n    api_key: test\n    api: " + string(api) + "\n" +
		"models:\n  - id: test-model\n    provider: test\n    model: test-model\n    api: " + string(api) + "\n"
	if err := os.WriteFile(filepath.Join(dir, ".goa", "config.yaml"), []byte(projectCfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, p
}

// runHeadlessCapture drives a full headless turn against dir and returns the
// tool names advertised to the model. loadCfg supplies the effective config
// (cascade in production; direct construction for targeted cases).
func runHeadlessCapture(t *testing.T, dir string, p *toolCaptureProvider, cfg *config.Config) []string {
	t.Helper()
	loader := config.NewCascadeLoader(dir, "", nil)
	subs := InitSubsystems(cfg, loader, dir, RuntimeOptions{PromptArg: "hi", Plain: true})
	var out bytes.Buffer
	app := NewHeadlessApp(subs, RuntimeOptions{PromptArg: "hi", Plain: true}, newPlainRenderer(&out), autoConfirmStrategy{})
	if code := app.Run(); code != 0 {
		t.Fatalf("Run() returned %d, want 0; output:\n%s", code, out.String())
	}
	return p.advertised()
}

func directCfg(p *toolCaptureProvider) *config.Config {
	return &config.Config{
		Providers: []config.ProviderConfig{
			{ID: "test", API: string(p.api), Endpoint: "http://localhost:9999", APIKey: "test"},
		},
		Models: []config.ModelConfig{
			{ID: "test-model", ProviderID: "test", Model: "test-model", API: string(p.api)},
		},
		ActiveProvider: "test",
		ActiveModel:    "test-model",
	}
}

func assertAdvertised(t *testing.T, tools []string, want ...string) {
	t.Helper()
	set := map[string]bool{}
	for _, name := range tools {
		set[name] = true
	}
	for _, w := range want {
		if !set[w] {
			t.Errorf("tool %q not advertised to the model (advertised=%v)", w, tools)
		}
	}
}

func assertNotAdvertised(t *testing.T, tools []string, unwanted ...string) {
	t.Helper()
	for _, name := range tools {
		for _, u := range unwanted {
			if name == u {
				t.Errorf("tool %q advertised but should be gated off (advertised=%v)", u, tools)
			}
		}
	}
}

// TestHeadless_ConfigEnabledAdvertisesAgentDrivenTools covers the
// embedded-default path at app level (the cascade half is pinned by
// config.TestDefaultConfig_AgentDrivenToolsEnabled): with the tools enabled
// in config — the new default — they are registered and advertised even with
// no companion state at all, ready to be armed by /companion:on.
func TestHeadless_ConfigEnabledAdvertisesAgentDrivenTools(t *testing.T) {
	dir, p := setupCaptureProject(t, "")
	cfg := directCfg(p)
	cfg.Tools.Enabled.RequestReview = true
	cfg.Tools.Enabled.DelegateTo = true
	assertAdvertised(t, runHeadlessCapture(t, dir, p, cfg), "request_review", "delegate_to")
}

// TestHeadless_CompanionOverridesStaleConfigFalse pins the precedence: a
// config layer's explicit tools.enabled false (persisted by an older version
// as a stale default) must NOT strip the tools when companion mode is active
// — the user explicitly turned companion on.
func TestHeadless_CompanionOverridesStaleConfigFalse(t *testing.T) {
	dir, p := setupCaptureProject(t, companionSeedState)
	cfg := directCfg(p)
	assertAdvertised(t, runHeadlessCapture(t, dir, p, cfg), "request_review", "delegate_to")
}

// TestHeadless_NoCompanionRespectsConfigFalse pins the inverse: without
// companion intent, the config gate still keeps the tools unregistered, so
// their schemas never reach the model.
func TestHeadless_NoCompanionRespectsConfigFalse(t *testing.T) {
	dir, p := setupCaptureProject(t, "")
	cfg := directCfg(p)
	assertNotAdvertised(t, runHeadlessCapture(t, dir, p, cfg), "request_review", "delegate_to")
}
