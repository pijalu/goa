// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package hooks

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPipelineOrdering(t *testing.T) {
	p := BuildPipeline(schema.Model{})
	names := p.HookNames()
	assert.Equal(t, []string{"auth", "sdkkey", "thinking", "cache", "tools", "messages", "errors"}, names)
}

func TestAuthHookInjectsAPIKey(t *testing.T) {
	h := &AuthHook{}
	require.NoError(t, h.Init(schema.VariantProfile{
		Auth: schema.AuthConfig{Header: "Authorization", Prefix: "Bearer "},
	}))

	ctx := &RequestContext{
		Model:   schema.Model{Provider: schema.ProviderOpenAI},
		Options: schema.StreamOptions{APIKey: "sk-test"},
		Headers: make(map[string]string),
	}
	require.NoError(t, h.ApplyRequest(ctx))
	assert.Equal(t, "Bearer sk-test", ctx.Headers["Authorization"])
	assert.Contains(t, ctx.Headers["User-Agent"], "goa/")
}

func TestAuthHookGitHubCopilotHeaders(t *testing.T) {
	h := &AuthHook{}
	require.NoError(t, h.Init(schema.VariantProfile{}))

	ctx := &RequestContext{
		Model:   schema.Model{Provider: schema.ProviderGitHub},
		Options: schema.StreamOptions{APIKey: "gh-token"},
		Headers: make(map[string]string),
		Context: schema.Context{
			Messages: []schema.Message{schema.NewUserMessageWithImage("look", "data:image/png;base64,abc")},
		},
	}
	require.NoError(t, h.ApplyRequest(ctx))
	assert.Contains(t, ctx.Headers["User-Agent"], "goa (")
	assert.Equal(t, "true", ctx.Headers["X-Vision-Preview"])
}

func TestErrorHookClassifiesContextOverflow(t *testing.T) {
	h := &ErrorHook{}
	require.NoError(t, h.Init(schema.VariantProfile{}))

	ctx := &ErrorContext{StatusCode: 400, Body: "context length exceeded"}
	require.NoError(t, h.ApplyError(ctx))
	assert.True(t, ctx.IsContextOverflow)
	assert.True(t, ctx.IsRetryable)
}

func TestErrorHookClassifiesRateLimit(t *testing.T) {
	h := &ErrorHook{}
	require.NoError(t, h.Init(schema.VariantProfile{}))

	ctx := &ErrorContext{StatusCode: http.StatusTooManyRequests}
	require.NoError(t, h.ApplyError(ctx))
	assert.True(t, ctx.IsRateLimit)
	assert.True(t, ctx.IsRetryable)
}

func TestToolHookNormalizesToolCallID(t *testing.T) {
	h := &ToolHook{}
	require.NoError(t, h.Init(schema.VariantProfile{
		ToolCompat: schema.ToolCompat{
			ToolCallIDRules: schema.ToolCallIDRules{MaxLength: 9, Alphabet: "[a-zA-Z0-9]"},
		},
	}))

	ctx := &RequestContext{
		Context: schema.Context{
			Messages: []schema.Message{
				schema.NewToolResultMessage("call-123!abc", "tool", "result", false),
			},
		},
	}
	require.NoError(t, h.ApplyRequest(ctx))
	id := ctx.Context.Messages[0].Content[0].ToolCallID
	assert.Len(t, id, 9)
	assert.Regexp(t, "^[a-zA-Z0-9]+$", id)
}

func TestToolHookSanitizesSchema(t *testing.T) {
	h := &ToolHook{}
	require.NoError(t, h.Init(schema.VariantProfile{
		ToolCompat: schema.ToolCompat{SchemaSanitizer: schema.SchemaSanitizerOpenAI},
	}))

	ctx := &RequestContext{
		Context: schema.Context{
			Tools: []schema.ToolSchema{
				{
					Name: "test",
					InputSchema: map[string]any{
						"$ref": "#/defs/foo",
						"type": "object",
					},
				},
			},
		},
	}
	require.NoError(t, h.ApplyRequest(ctx))
	_, hasRef := ctx.Context.Tools[0].InputSchema["$ref"]
	assert.False(t, hasRef)
	assert.Equal(t, "object", ctx.Context.Tools[0].InputSchema["type"])
}

func TestToolHookGeminiEnumSanitizer(t *testing.T) {
	h := &ToolHook{}
	require.NoError(t, h.Init(schema.VariantProfile{
		ToolCompat: schema.ToolCompat{SchemaSanitizer: schema.SchemaSanitizerGemini},
	}))

	ctx := &RequestContext{
		Context: schema.Context{
			Tools: []schema.ToolSchema{
				{
					Name: "test",
					InputSchema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"n": map[string]any{"type": "integer", "enum": []any{1, 2, 3}},
						},
					},
				},
			},
		},
	}
	require.NoError(t, h.ApplyRequest(ctx))
	props := ctx.Context.Tools[0].InputSchema["properties"].(map[string]any)
	enum := props["n"].(map[string]any)["enum"].([]any)
	assert.Equal(t, "1", enum[0])
}

func TestToolHookMistralID(t *testing.T) {
	h := &ToolHook{}
	require.NoError(t, h.Init(schema.VariantProfile{
		ToolCompat: schema.ToolCompat{
			ToolCallIDRules: schema.ToolCallIDRules{MaxLength: 9, Alphabet: "[a-zA-Z0-9]", HashBased: true},
		},
	}))

	ctx := &RequestContext{
		Context: schema.Context{
			Messages: []schema.Message{
				schema.NewToolResultMessage("very-long-call-id-12345", "tool", "result", false),
			},
		},
	}
	require.NoError(t, h.ApplyRequest(ctx))
	id := ctx.Context.Messages[0].Content[0].ToolCallID
	assert.Len(t, id, 9)
}

func TestMessageHookFiltersContentFiltered(t *testing.T) {
	h := &MessageHook{}
	require.NoError(t, h.Init(schema.VariantProfile{}))

	ctx := &RequestContext{
		Context: schema.Context{
			Messages: []schema.Message{
				{Role: schema.RoleAssistant, Content: []schema.ContentBlock{{Type: schema.ContentBlockText, Text: "bad"}}, StopReason: schema.StopReasonContentFiltered},
			},
		},
	}
	require.NoError(t, h.ApplyRequest(ctx))
	assert.Empty(t, ctx.Context.Messages[0].StopReason)
}

func TestMessageHookWrapsSystemUpdate(t *testing.T) {
	h := &MessageHook{}
	require.NoError(t, h.Init(schema.VariantProfile{}))

	ctx := &RequestContext{
		Model: schema.Model{ID: "gpt-4o"},
		Context: schema.Context{
			Messages: []schema.Message{schema.NewSystemMessage("update")},
		},
	}
	require.NoError(t, h.ApplyRequest(ctx))
	assert.Contains(t, ctx.Context.Messages[0].Content[0].Text, "<system-update>")
}

func TestToolHookToolResultAsUser(t *testing.T) {
	h := &ToolHook{}
	require.NoError(t, h.Init(schema.VariantProfile{
		ToolCompat: schema.ToolCompat{ToolResultAsUser: true},
	}))

	ctx := &RequestContext{
		Context: schema.Context{
			Messages: []schema.Message{
				schema.NewToolResultMessage("call-1", "tool", "result", false),
			},
		},
	}
	require.NoError(t, h.ApplyRequest(ctx))
	assert.Equal(t, schema.RoleUser, ctx.Context.Messages[0].Role)
	assert.Contains(t, ctx.Context.Messages[0].Content[0].Text, "<tool_result")
}

func TestMessageHookDowngradesImages(t *testing.T) {
	h := &MessageHook{}
	require.NoError(t, h.Init(schema.VariantProfile{}))

	ctx := &RequestContext{
		Context: schema.Context{
			Messages: []schema.Message{
				schema.NewUserMessageWithImage("look", "data:image/png;base64,abc"),
			},
		},
	}
	require.NoError(t, h.ApplyRequest(ctx))
	require.Len(t, ctx.Context.Messages[0].Content, 2)
	assert.Equal(t, schema.ContentBlockText, ctx.Context.Messages[0].Content[0].Type)
	assert.Equal(t, schema.ContentBlockText, ctx.Context.Messages[0].Content[1].Type)
	assert.Equal(t, "(image omitted)", ctx.Context.Messages[0].Content[1].Text)
}

func TestThinkingHookConvertsThinkingToText(t *testing.T) {
	h := &ThinkingHook{}
	require.NoError(t, h.Init(schema.VariantProfile{
		Compat: schema.CompatFlags{ThinkingFormat: "none"},
	}))

	ctx := &RequestContext{
		Context: schema.Context{
			Messages: []schema.Message{
				{
					Role: schema.RoleAssistant,
					Content: []schema.ContentBlock{
						{Type: schema.ContentBlockThinking, Thinking: " I think... "},
					},
				},
			},
		},
	}
	require.NoError(t, h.ApplyRequest(ctx))
	require.Len(t, ctx.Context.Messages[0].Content, 1)
	assert.Equal(t, schema.ContentBlockText, ctx.Context.Messages[0].Content[0].Type)
	assert.Equal(t, "I think...", ctx.Context.Messages[0].Content[0].Text)
}

func TestCacheHookPlacesBreakpoints(t *testing.T) {
	h := &CacheHook{}
	require.NoError(t, h.Init(schema.VariantProfile{
		CachePolicy: schema.CachePolicy{
			Mode:          schema.CacheModeAuto,
			BreakpointCap: 4,
			Messages:      schema.CacheMessagePolicy{Tools: true, System: true, Tail: 2},
		},
	}))

	ctx := &RequestContext{
		Context: schema.Context{
			Messages: []schema.Message{
				schema.NewSystemMessage("sys"),
				schema.NewUserMessage("hello"),
				schema.NewUserMessage("world"),
			},
		},
	}
	require.NoError(t, h.ApplyRequest(ctx))
	cached := 0
	for _, m := range ctx.Context.Messages {
		if m.Extra != nil && m.Extra["cache_control"] != nil {
			cached++
		}
	}
	assert.GreaterOrEqual(t, cached, 1)
	assert.LessOrEqual(t, cached, 4)
}

func TestSanitizeCacheKey(t *testing.T) {
	assert.Equal(t, "hello_world_", SanitizeCacheKey("Hello World!"))
	long := strings.Repeat("a", 200)
	assert.Len(t, SanitizeCacheKey(long), 128)
}

func TestErrorHookRetryAfter(t *testing.T) {
	h := &ErrorHook{}
	require.NoError(t, h.Init(schema.VariantProfile{}))

	ctx := &ErrorContext{
		StatusCode: 429,
		Headers:    map[string]string{"Retry-After": "5"},
	}
	require.NoError(t, h.ApplyError(ctx))
	assert.True(t, ctx.IsRetryable)
	assert.Equal(t, 5, ctx.RetryAfter)
}

func TestIsContextOverflow(t *testing.T) {
	assert.True(t, IsContextOverflow(&ProviderError{Err: errors.New("context length exceeded"), IsContextOverflow: true}))
	assert.True(t, IsContextOverflow(&ProviderError{Err: errors.New("context_length_exceeded"), IsContextOverflow: true}))
	assert.False(t, IsContextOverflow(errors.New("random error")))
}
