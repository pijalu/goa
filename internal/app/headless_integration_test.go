// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/agentic/provider"
)

func TestMain(m *testing.M) {
	core.ResetGlobalRegistry()
	os.Exit(m.Run())
}

// headlessTestProvider is a mock API provider that emits predetermined events.
type headlessTestProvider struct {
	api    provider.Api
	events []provider.AssistantMessageEvent
}

func (p *headlessTestProvider) API() provider.Api { return p.api }

func (p *headlessTestProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	result := provider.NewAssistantMessageEventStream(64)
	go func() {
		for _, event := range p.events {
			result.Push(event)
		}
		result.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "mock done"}},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return result, nil
}

func (p *headlessTestProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	base := provider.BuildSimpleOptions(model, opts)
	return p.Stream(model, ctx, base)
}

func TestHeadlessApp_Run_EndToEnd(t *testing.T) {
	api := provider.Api(fmt.Sprintf("test-headless-%d", time.Now().UnixNano()))
	p := &headlessTestProvider{
		api: api,
		events: []provider.AssistantMessageEvent{
			{Type: provider.EventTextStart, ContentIndex: 0},
			{Type: provider.EventTextDelta, ContentIndex: 0, Delta: "Hello "},
			{Type: provider.EventTextDelta, ContentIndex: 0, Delta: "world"},
			{Type: provider.EventTextEnd, ContentIndex: 0, Content: "Hello world"},
		},
	}
	provider.RegisterApiProvider(p)
	core.ResetGlobalRegistry()

	dir := t.TempDir()
	cfg := &config.Config{
		Providers: []config.ProviderConfig{
			{ID: "test", API: string(api), Endpoint: "http://localhost:9999", APIKey: "test"},
		},
		Models: []config.ModelConfig{
			{ID: "test-model", ProviderID: "test", Model: "test-model", API: string(api)},
		},
		ActiveProvider: "test",
		ActiveModel:    "test-model",
	}

	loader := config.NewCascadeLoader(dir, "", nil)
	subs := InitSubsystems(cfg, loader, dir, RuntimeOptions{PromptArg: "hi", Plain: true})

	var out bytes.Buffer
	app := NewHeadlessApp(subs, RuntimeOptions{PromptArg: "hi", Plain: true}, newPlainRenderer(&out), autoConfirmStrategy{})
	code := app.Run()
	if code != 0 {
		t.Fatalf("Run() returned %d, want 0; output:\n%s", code, out.String())
	}

	got := out.String()
	if !strings.Contains(got, "-- user") {
		t.Errorf("missing user marker: %q", got)
	}
	if !strings.Contains(got, "hi") {
		t.Errorf("missing prompt: %q", got)
	}
	if !strings.Contains(got, "-- assistant") {
		t.Errorf("missing assistant marker: %q", got)
	}
	if !strings.Contains(got, "Hello world") {
		t.Errorf("missing assistant content: %q", got)
	}
	if !strings.Contains(got, "-- stats turn=1") {
		t.Errorf("missing stats line: %q", got)
	}
	if !strings.Contains(got, "-- summary turns=1") {
		t.Errorf("missing summary line: %q", got)
	}
}

func TestHeadlessApp_Run_NoProvider(t *testing.T) {
	subs := &subsystems{cfg: &config.Config{}}
	var out bytes.Buffer
	app := NewHeadlessApp(subs, RuntimeOptions{PromptArg: "hi", Plain: true}, newPlainRenderer(&out), autoConfirmStrategy{})
	code := app.Run()
	if code != headlessExitProviderError {
		t.Fatalf("Run() returned %d, want %d; output:\n%s", code, headlessExitProviderError, out.String())
	}
	if !strings.Contains(out.String(), "no provider configured") {
		t.Errorf("expected no provider error, got: %q", out.String())
	}
}
