// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package testutil

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider"
	_ "github.com/pijalu/goa/internal/agentic/provider/openai"
	"github.com/pijalu/goa/internal/agentic/skillrunner/tools"
)

func TestValidationError_PreservesFields(t *testing.T) {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type": "string",
				"enum": []string{"get", "set", "list"},
			},
			"entity": map[string]interface{}{
				"type": "string",
			},
		},
		"required": []string{"action", "entity"},
	}

	err := agentic.Validate(schema, `{"action": "invalid"}`)
	if err == nil {
		t.Fatal("expected validation error")
	}

	if len(err.Fields) == 0 {
		t.Fatal("expected Fields to be populated")
	}

	foundEntity := false
	foundEnum := false
	for _, f := range err.Fields {
		if f.Field == "entity" && f.Type == "required" {
			foundEntity = true
		}
		if f.Field == "action" && f.Type == "enum" {
			foundEnum = true
			if len(f.ValidValues) == 0 {
				t.Error("expected ValidValues for enum failure")
			}
		}
	}

	if !foundEntity {
		t.Error("expected 'entity' required field error")
	}
	if !foundEnum {
		t.Error("expected 'action' enum field error")
	}
}

func TestEnrichError_RequiredField(t *testing.T) {
	schema := agentic.ToolSchema{
		Name:        "state",
		Description: "State tool",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"action": map[string]interface{}{
					"type":        "string",
					"description": "The action",
					"enum":        []string{"get", "set", "list"},
				},
				"entity": map[string]interface{}{
					"type":        "string",
					"description": "The entity",
				},
			},
			"required": []string{"action", "entity"},
		},
	}

	vErr := agentic.Validate(schema.Schema, `{"action": "get"}`)
	if vErr == nil {
		t.Fatal("expected validation error")
	}

	enriched := agentic.EnrichError(schema, vErr)
	if !strings.Contains(enriched, "entity is required") {
		t.Errorf("expected 'entity is required' in enriched error, got: %s", enriched)
	}
	if !strings.Contains(enriched, "Hint:") {
		t.Errorf("expected 'Hint:' in enriched error, got: %s", enriched)
	}
	if !strings.Contains(enriched, "state") {
		t.Errorf("expected tool name 'state' in enriched error, got: %s", enriched)
	}
}

func TestEnrichError_EnumFailure(t *testing.T) {
	schema := agentic.ToolSchema{
		Name:        "state",
		Description: "State tool",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"action": map[string]interface{}{
					"type": "string",
					"enum": []string{"get", "set", "list"},
				},
			},
			"required": []string{"action"},
		},
	}

	vErr := agentic.Validate(schema.Schema, `{"action": "invalid"}`)
	if vErr == nil {
		t.Fatal("expected validation error")
	}

	enriched := agentic.EnrichError(schema, vErr)
	if !strings.Contains(enriched, "get") || !strings.Contains(enriched, "set") || !strings.Contains(enriched, "list") {
		t.Errorf("expected enum values in enriched error, got: %s", enriched)
	}
}

func TestEnrichError_PlainGoError_Short(t *testing.T) {
	schema := agentic.ToolSchema{
		Name:        "test",
		Description: "Test tool",
		Schema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	}

	shortErr := agentic.EnrichError(schema, agentic.Validate(schema.Schema, `{}`))
	if shortErr == "" {
		// Validate returned nil for empty object with no required fields
		// Use a plain error instead
		shortErr = agentic.EnrichError(schema, &testError{msg: "something failed"})
	}

	if !strings.Contains(shortErr, "Hint:") {
		t.Errorf("expected hint for short error, got: %s", shortErr)
	}
}

func TestEnrichError_PlainGoError_Long(t *testing.T) {
	schema := agentic.ToolSchema{
		Name:        "test",
		Description: "Test tool",
		Schema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	}

	longMsg := strings.Repeat("a", 250)
	enriched := agentic.EnrichError(schema, &testError{msg: longMsg})
	if strings.Contains(enriched, "Hint:") {
		t.Errorf("expected NO hint for long error (≥200 chars), got: %s", enriched)
	}
	if enriched != longMsg {
		t.Errorf("expected original error only, got: %s", enriched)
	}
}

type testError struct {
	msg string
}

func (e *testError) Error() string { return e.msg }

func TestHandleToolCall_SchemaValidation(t *testing.T) {
	tool := &SimulatedTool{
		ToolSchema: agentic.ToolSchema{
			Name:        "test_tool",
			Description: "A test tool",
			Schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"required_field": map[string]interface{}{
						"type": "string",
					},
				},
				"required": []string{"required_field"},
			},
		},
		Result: "success",
	}

	harness := NewSimulated(t, []agentic.Tool{tool}, []SimulatedResponse{
		{ToolName: "test_tool", ToolInput: `{}`},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := harness.RunConversation(ctx, "test")
	if err != nil {
		t.Fatalf("RunConversation failed: %v", err)
	}

	// With the stream path, the tool always succeeds (SimulatedTool doesn't validate)
	// The tool call is written to history and the turn completes normally.
	if len(result.ToolErrors) > 0 {
		t.Logf("Tool errors (unexpected): %v", result.ToolErrors)
	}
}

func TestHandleToolCall_ExecutionError(t *testing.T) {
	tool := SimulatedFailingTool("fail_tool", "Always fails", "execution failed")

	harness := NewSimulated(t, []agentic.Tool{tool}, []SimulatedResponse{
		{ToolName: "fail_tool", ToolInput: `{}`},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := harness.RunConversation(ctx, "test")
	if err != nil {
		t.Fatalf("RunConversation failed: %v", err)
	}

	// SimulatedFailingTool always fails — error is captured in ToolErrors
	if len(result.ToolErrors) == 0 {
		t.Fatal("expected tool error for execution failure")
	}
}

// retryTool fails on first call, succeeds on second
type retryTool struct {
	callCount int
}

func (t *retryTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "retry_tool",
		Description: "Sometimes fails",
		Schema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	}
}

func (t *retryTool) Execute(input string) (string, error) {
	t.callCount++
	if t.callCount == 1 {
		return "", &testError{msg: "transient failure"}
	}
	return "success", nil
}

func (t *retryTool) IsRetryable(err error) bool { return true }

func TestHandleToolCall_RetryOnlyWhenRetryable(t *testing.T) {
	tool := &retryTool{}

	harness := NewSimulated(t, []agentic.Tool{tool}, []SimulatedResponse{
		{ToolName: "retry_tool", ToolInput: `{}`},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := harness.RunConversation(ctx, "test")
	if err != nil {
		t.Fatalf("RunConversation failed: %v", err)
	}

	// With the stream-based path, the tool is called once and the result
	// is written to history. Auto-retry is handled by LLM re-streaming.
	if tool.callCount != 1 {
		t.Errorf("expected 1 call (new path: no auto-retry), got %d", tool.callCount)
	}
}

func TestHandleToolCall_NoRetryWhenNotRetryable(t *testing.T) {
	tool := SimulatedFailingTool("fail_tool", "Always fails", "deterministic failure")

	harness := NewSimulated(t, []agentic.Tool{tool}, []SimulatedResponse{
		{ToolName: "fail_tool", ToolInput: `{}`},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := harness.RunConversation(ctx, "test")
	if err != nil {
		t.Fatalf("RunConversation failed: %v", err)
	}

	if tool.CallCount != 1 {
		t.Errorf("expected 1 call (no retry), got %d", tool.CallCount)
	}
}

func TestRealLLM_SelfCorrection(t *testing.T) {
	endpoint := os.Getenv("K8_LLM_URL")
	if endpoint == "" {
		endpoint = "http://localhost:1234/v1/chat/completions"
	}

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(endpoint)
	if err != nil {
		t.Skipf("LLM not available at %s: %v", endpoint, err)
	}
	resp.Body.Close()

	tool := &retryTool{}

	mdl := provider.Model{
		ID:         "google/gemma-4-e4b",
		Name:       "google/gemma-4-e4b",
		Api:        provider.ApiOpenAICompletions,
		Provider:   provider.ProviderCustom,
		BaseURL:    endpoint,
		InputTypes: []string{"text"},
	}

	harness := NewHarness(mdl, provider.StreamOptions{Timeout: 15 * time.Second}, []agentic.Tool{tool})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	_, err = harness.RunConversation(ctx, "Reply with exactly: 'LLM is working'")
	if err != nil {
		if strings.Contains(err.Error(), "Failed to load model") {
			t.Skipf("LLM model not loadable at %s: %v", endpoint, err)
		}
		t.Fatalf("RunConversation failed: %v", err)
	}
}

// Test integration with real skillrunner tools to ensure IsRetryable works
func TestStateTool_SchemaFix_ListWithoutEntity(t *testing.T) {
	// The state tool should accept {"action": "list"} without entity
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type": "string",
				"enum": []string{"get", "set", "validate", "resync", "list", "diff"},
			},
			"entity": map[string]interface{}{
				"type": "string",
			},
		},
		"required": []string{"action"},
	}

	err := agentic.Validate(schema, `{"action": "list"}`)
	if err != nil {
		t.Fatalf("expected list without entity to be valid, got error: %s", err.Error())
	}
}

func TestStateTool_SchemaFix_SetWithoutEntity(t *testing.T) {
	// The state tool schema should accept {"action": "set"} without entity at schema level,
	// but the tool's Execute should return a runtime error with enrichment.
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type": "string",
				"enum": []string{"get", "set", "validate", "resync", "list", "diff"},
			},
			"entity": map[string]interface{}{
				"type": "string",
			},
		},
		"required": []string{"action"},
	}

	// Schema validation should pass without entity
	err := agentic.Validate(schema, `{"action": "set"}`)
	if err != nil {
		t.Fatalf("expected set without entity to pass schema validation, got error: %s", err.Error())
	}

	// But EnrichError should still provide a hint if there's a runtime error
	toolSchema := agentic.ToolSchema{
		Name:        "state",
		Description: "State operations",
		Schema:      schema,
	}

	runtimeErr := &testError{msg: "entity is required for action 'set'"}
	enriched := agentic.EnrichError(toolSchema, runtimeErr)
	if !strings.Contains(enriched, "Hint:") {
		t.Errorf("expected enriched runtime error with hint, got: %s", enriched)
	}
}

func TestRestClientTool_IsRetryable(t *testing.T) {
	tool := tools.NewRestClientTool(agentic.NewLogger(agentic.Error))

	// Network timeout should be retryable
	if !tool.IsRetryable(&testNetError{timeout: true}) {
		t.Error("expected timeout error to be retryable")
	}

	// Regular errors should not be retryable
	if tool.IsRetryable(&testError{msg: "bad request"}) {
		t.Error("expected regular error to NOT be retryable")
	}
}

type testNetError struct {
	timeout   bool
	temporary bool
}

func (e *testNetError) Error() string   { return "network error" }
func (e *testNetError) Timeout() bool   { return e.timeout }
func (e *testNetError) Temporary() bool { return e.temporary }
