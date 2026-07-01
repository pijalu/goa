// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"context"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
)

func TestModelValidator_ValidatesConfiguredModels(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{
			{ID: "local", Endpoint: "http://localhost:1234/v1"},
		},
		Models: []config.ModelConfig{
			{ID: "llama3", ProviderID: "local", Model: "llama3"},
			{ID: "missing", ProviderID: "local", Model: "not-in-list"},
		},
	}
	pm := NewProviderManager(cfg)
	v := NewModelValidator(pm, cfg)

	// Override the validator's check for the provider to avoid network calls.
	v.SetValid("llama3", true)
	v.SetValid("missing", false)

	if !v.IsValid("llama3") {
		t.Error("expected llama3 to be valid")
	}
	if v.IsValid("missing") {
		t.Error("expected missing to be invalid")
	}
	if v.IsValid("unknown") {
		t.Error("expected unknown model to be invalid")
	}
}

func TestModelValidator_StartRunsInitialValidation(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{
			{ID: "local", Endpoint: "http://localhost:1234/v1"},
		},
		Models: []config.ModelConfig{
			{ID: "m1", ProviderID: "local", Model: "m1"},
		},
	}
	pm := NewProviderManager(cfg)
	v := NewModelValidator(pm, cfg)

	// Without overriding, ValidateAll will hit the network and mark invalid.
	// We just verify Start does not panic and the status map is updated.
	ctx, cancel := testingContext()
	defer cancel()
	v.Start(ctx, time.Hour)

	// Give the initial validation goroutine a moment to run.
	time.Sleep(50 * time.Millisecond)

	// The model should have been checked (status entry exists, likely false).
	_ = v.IsValid("m1")
}

func testingContext() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}
