// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"testing"
)

// testProvider is a minimal ApiProvider implementation for testing the registry.
type testProvider struct {
	api Api
}

func (p *testProvider) API() Api { return p.api }

func (p *testProvider) Stream(model Model, ctx Context, opts StreamOptions) (*AssistantMessageEventStream, error) {
	s := NewAssistantMessageEventStream(16)
	s.End(&AssistantMessage{})
	return s, nil
}

func (p *testProvider) StreamSimple(model Model, ctx Context, opts SimpleStreamOptions) (*AssistantMessageEventStream, error) {
	return p.Stream(model, ctx, opts.StreamOptions)
}

func TestRegisterAndGet(t *testing.T) {
	ClearApiProviders()
	defer ClearApiProviders()

	p := &testProvider{api: ApiOpenAICompletions}
	RegisterApiProvider(p)

	got, ok := GetApiProvider(ApiOpenAICompletions)
	if !ok {
		t.Fatal("expected provider to be found")
	}
	if got.API() != ApiOpenAICompletions {
		t.Errorf("expected API=%q, got %q", ApiOpenAICompletions, got.API())
	}
}

func TestGetUnregistered(t *testing.T) {
	ClearApiProviders()
	defer ClearApiProviders()

	_, ok := GetApiProvider(ApiAnthropicMessages)
	if ok {
		t.Error("expected false for unregistered API")
	}
}

func TestRegisterPanicsOnDuplicate(t *testing.T) {
	ClearApiProviders()
	defer ClearApiProviders()

	RegisterApiProvider(&testProvider{api: ApiOpenAICompletions})

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate registration")
		}
	}()

	RegisterApiProvider(&testProvider{api: ApiOpenAICompletions})
}

func TestClearProviders(t *testing.T) {
	ClearApiProviders()
	defer ClearApiProviders()

	RegisterApiProvider(&testProvider{api: ApiOpenAICompletions})
	RegisterApiProvider(&testProvider{api: ApiAnthropicMessages})

	ClearApiProviders()

	apis := RegisteredAPIs()
	if len(apis) != 0 {
		t.Errorf("expected 0 registered APIs after clear, got %d", len(apis))
	}
}

func TestRegisteredAPIs(t *testing.T) {
	ClearApiProviders()
	defer ClearApiProviders()

	RegisterApiProvider(&testProvider{api: ApiOpenAICompletions})
	RegisterApiProvider(&testProvider{api: ApiAnthropicMessages})

	apis := RegisteredAPIs()
	if len(apis) != 2 {
		t.Fatalf("expected 2 registered APIs, got %d", len(apis))
	}

	seen := make(map[Api]bool)
	for _, api := range apis {
		seen[api] = true
	}
	if !seen[ApiOpenAICompletions] {
		t.Errorf("expected %q in registered APIs", ApiOpenAICompletions)
	}
	if !seen[ApiAnthropicMessages] {
		t.Errorf("expected %q in registered APIs", ApiAnthropicMessages)
	}
}

func TestStream_TestProvider(t *testing.T) {
	ClearApiProviders()
	defer ClearApiProviders()

	p := &testProvider{api: ApiOpenAICompletions}
	RegisterApiProvider(p)

	got, ok := GetApiProvider(ApiOpenAICompletions)
	if !ok {
		t.Fatal("expected provider to be found")
	}

	s, err := got.Stream(Model{}, Context{}, StreamOptions{})
	if err != nil {
		t.Fatalf("Stream() error: %v", err)
	}

	result := s.Result()
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestStreamSimple_TestProvider(t *testing.T) {
	ClearApiProviders()
	defer ClearApiProviders()

	p := &testProvider{api: ApiOpenAICompletions}
	RegisterApiProvider(p)

	got, _ := GetApiProvider(ApiOpenAICompletions)

	s, err := got.StreamSimple(Model{}, Context{}, SimpleStreamOptions{})
	if err != nil {
		t.Fatalf("StreamSimple() error: %v", err)
	}

	result := s.Result()
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}
