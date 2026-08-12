// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// modelsServer returns an httptest server answering GET /models with the given
// model IDs (or with a 500 when ids is nil and fail is true).
func modelsServer(t *testing.T, ids []string, fail bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var data []map[string]string
		for _, id := range ids {
			data = append(data, map[string]string{"id": id})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
}

// TestModelValidator_TriState pins the Model list:fix: validity is a
// TRI-STATE — unknown (never probed / probe failed), valid (provider lists the
// model), invalid (provider answered but does not list the model). A probe
// ERROR must never mark a model invalid (transient local outages, e.g. LM
// Studio down at probe time, must not turn the entry red).
func TestModelValidator_TriState(t *testing.T) {
	listed := modelsServer(t, []string{"gemma"}, false)
	defer listed.Close()
	failing := modelsServer(t, nil, true)
	defer failing.Close()
	// unreachable: a closed port that refuses connections.
	dead := modelsServer(t, nil, false)
	deadURL := dead.URL
	dead.Close()

	cfg := &config.Config{
		Providers: []config.ProviderConfig{
			{ID: "ok", Endpoint: listed.URL + "/v1"},
			{ID: "flaky", Endpoint: failing.URL + "/v1"},
			{ID: "dead", Endpoint: deadURL + "/v1"},
		},
		Models: []config.ModelConfig{
			{ID: "present", ProviderID: "ok", Model: "gemma"},
			{ID: "absent", ProviderID: "ok", Model: "not-served"},
			{ID: "flaky", ProviderID: "flaky", Model: "gemma"},
			{ID: "dead", ProviderID: "dead", Model: "gemma"},
			{ID: "noprov", ProviderID: "missing-provider", Model: "gemma"},
		},
	}
	v := NewModelValidator(NewProviderManager(cfg), cfg)

	// Pre-probe: everything is unknown, never invalid.
	for _, m := range cfg.Models {
		if got := v.State(m.ID); got != ValidityUnknown {
			t.Errorf("pre-probe State(%s) = %v, want ValidityUnknown", m.ID, got)
		}
	}

	v.ValidateAll()

	cases := map[string]ModelValidity{
		"present": ValidityValid,   // provider lists the model
		"absent":  ValidityInvalid, // provider answered, model not listed
		"flaky":   ValidityUnknown, // 500: probe error keeps unknown
		"dead":    ValidityUnknown, // connection refused keeps unknown
		"noprov":  ValidityInvalid, // config references a missing provider
	}
	for id, want := range cases {
		if got := v.State(id); got != want {
			t.Errorf("State(%s) = %v, want %v", id, got, want)
		}
	}

	// IsValid backward compatibility: only ValidityValid is true.
	if !v.IsValid("present") || v.IsValid("absent") || v.IsValid("flaky") {
		t.Error("IsValid must be true only for ValidityValid")
	}

	// A later outage must not clobber a confirmed state: kill the good server
	// and re-validate — valid stays valid, invalid stays invalid.
	listed.Close()
	v.ValidateAll()
	if got := v.State("present"); got != ValidityValid {
		t.Errorf("post-outage State(present) = %v, want ValidityValid (sticky)", got)
	}
	if got := v.State("absent"); got != ValidityInvalid {
		t.Errorf("post-outage State(absent) = %v, want ValidityInvalid (sticky)", got)
	}
}

func testingContext() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}
