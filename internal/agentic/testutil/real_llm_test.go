// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package testutil

import (
	"os"
	"testing"
)

// realLLMEndpoint returns the endpoint to use for a real-LLM integration test
// and whether the test should run. Real-LLM tests are opt-in via the
// K8_LLM_URL environment variable; without it they are skipped so that the
// standard test suite remains deterministic and does not fail because a
// local LLM is slow or unavailable.
func realLLMEndpoint() (endpoint string, ok bool) {
	endpoint = os.Getenv("K8_LLM_URL")
	if endpoint == "" {
		return "", false
	}
	return endpoint, true
}

// TestRealLLMEndpoint_SkipsWithoutEnv verifies that real-LLM tests are
// disabled unless the operator explicitly opts in. This keeps the default
// `go test ./...` run deterministic and prevents a slow or missing local LLM
// from causing flaky failures.
func TestRealLLMEndpoint_SkipsWithoutEnv(t *testing.T) {
	t.Setenv("K8_LLM_URL", "")
	if _, ok := realLLMEndpoint(); ok {
		t.Fatal("expected realLLMEndpoint to return ok=false when K8_LLM_URL is unset")
	}
}

func TestRealLLMEndpoint_RunsWithEnv(t *testing.T) {
	t.Setenv("K8_LLM_URL", "http://example.local/v1/chat/completions")
	endpoint, ok := realLLMEndpoint()
	if !ok {
		t.Fatal("expected realLLMEndpoint to return ok=true when K8_LLM_URL is set")
	}
	if endpoint != "http://example.local/v1/chat/completions" {
		t.Fatalf("endpoint = %q, want the env value", endpoint)
	}
}
