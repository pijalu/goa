// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package openai

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// TestBuildParams_ToolStream verifies the legacy OpenAI-completions builder
// emits the z.ai "tool_stream": true field next to the tools array, and omits it
// everywhere else (no tools, NoTools collapse, non-z.ai provider).
func TestBuildParams_ToolStream(t *testing.T) {
	zaiModel := provider.Model{
		ID: "glm-5.2", Api: provider.ApiOpenAICompletions,
		Provider: provider.ProviderZai, BaseURL: "https://api.z.ai/api/coding/paas/v4",
	}
	openaiModel := provider.Model{
		ID: "gpt-4o", Api: provider.ApiOpenAICompletions,
		Provider: provider.ProviderOpenAI, BaseURL: "https://api.openai.com/v1",
	}
	tools := []provider.ToolSchema{{Name: "read", Description: "read a file"}}
	msgs := []provider.Message{provider.NewUserMessage("hi")}

	zai := provider.ResolveOpenAICompat(zaiModel)
	if !provider.ToBool(zai.ZaiToolStream, false) {
		t.Fatal("z.ai compat must advertise ZaiToolStream")
	}
	openai := provider.ResolveOpenAICompat(openaiModel)
	if provider.ToBool(openai.ZaiToolStream, false) {
		t.Fatal("non-z.ai compat must not advertise ZaiToolStream")
	}

	cases := []struct {
		name string
		mod  provider.Model
		cm   provider.OpenAICompletionsCompat
		ctx  provider.Context
		want bool
	}{
		{"zai with tools", zaiModel, zai, provider.Context{Messages: msgs, Tools: tools}, true},
		{"zai without tools", zaiModel, zai, provider.Context{Messages: msgs}, false},
		{"zai NoTools collapse", zaiModel, zai, provider.Context{Messages: msgs, Tools: tools, NoTools: true}, false},
		{"openai with tools", openaiModel, openai, provider.Context{Messages: msgs, Tools: tools}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := buildParams(tc.mod, tc.ctx, provider.StreamOptions{}, tc.cm)
			got, ok := body["tool_stream"]
			if !tc.want {
				if ok {
					t.Fatalf("tool_stream must be absent, got %v", got)
				}
				return
			}
			if got != true {
				t.Fatalf("tool_stream = %v, want true", got)
			}
		})
	}
}
