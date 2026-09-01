// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package hooks

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// OpenCode Zen/Go serve multiple wire formats under one provider identity:
// the anthropic /messages surface expects "x-api-key" while the
// chat-completions/responses surfaces expect "Authorization: Bearer". The
// profile's per-API auth override must select the header matching the
// model's resolved wire API.
func TestAuthHook_PerAPIAuthHeader(t *testing.T) {
	profile := schema.VariantProfile{
		Auth: schema.AuthConfig{
			Header: "Authorization",
			Prefix: "Bearer ",
			PerAPI: map[string]schema.APIAuth{
				"anthropic-messages": {Header: "x-api-key", Prefix: ""},
			},
		},
	}

	t.Run("anthropic-messages uses x-api-key", func(t *testing.T) {
		h := &AuthHook{}
		require.NoError(t, h.Init(profile))
		ctx := &RequestContext{
			Model:   schema.Model{Provider: schema.ProviderOpenCode, Api: schema.ApiAnthropicMessages},
			Options: schema.StreamOptions{APIKey: "sk-zen-test"},
			Headers: make(map[string]string),
		}
		require.NoError(t, h.ApplyRequest(ctx))
		assert.Equal(t, "sk-zen-test", ctx.Headers["x-api-key"], "anthropic surface must use x-api-key with no prefix")
		assert.Empty(t, ctx.Headers["Authorization"], "no Bearer header on the anthropic surface")
	})

	t.Run("openai-completions keeps Bearer", func(t *testing.T) {
		h := &AuthHook{}
		require.NoError(t, h.Init(profile))
		ctx := &RequestContext{
			Model:   schema.Model{Provider: schema.ProviderOpenCode, Api: schema.ApiOpenAICompletions},
			Options: schema.StreamOptions{APIKey: "sk-zen-test"},
			Headers: make(map[string]string),
		}
		require.NoError(t, h.ApplyRequest(ctx))
		assert.Equal(t, "Bearer sk-zen-test", ctx.Headers["Authorization"], "oa-compat surface keeps Bearer")
		assert.Empty(t, ctx.Headers["x-api-key"], "no x-api-key on the oa-compat surface")
	})

	t.Run("openai-responses keeps Bearer", func(t *testing.T) {
		h := &AuthHook{}
		require.NoError(t, h.Init(profile))
		ctx := &RequestContext{
			Model:   schema.Model{Provider: schema.ProviderOpenCode, Api: schema.ApiOpenAIResponses},
			Options: schema.StreamOptions{APIKey: "sk-zen-test"},
			Headers: make(map[string]string),
		}
		require.NoError(t, h.ApplyRequest(ctx))
		assert.Equal(t, "Bearer sk-zen-test", ctx.Headers["Authorization"], "responses surface keeps Bearer")
		assert.Empty(t, ctx.Headers["x-api-key"], "no x-api-key on the responses surface")
	})

	t.Run("no per-api override falls back to base", func(t *testing.T) {
		h := &AuthHook{}
		require.NoError(t, h.Init(schema.VariantProfile{
			Auth: schema.AuthConfig{Header: "Authorization", Prefix: "Bearer "},
		}))
		ctx := &RequestContext{
			Model:   schema.Model{Provider: schema.ProviderOpenCode, Api: schema.ApiAnthropicMessages},
			Options: schema.StreamOptions{APIKey: "sk-zen-test"},
			Headers: make(map[string]string),
		}
		require.NoError(t, h.ApplyRequest(ctx))
		assert.Equal(t, "Bearer sk-zen-test", ctx.Headers["Authorization"], "no PerAPI -> base auth")
	})
}
