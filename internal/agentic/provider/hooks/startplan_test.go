// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package hooks

import (
	"errors"
	"net/http"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/pijalu/goa/internal/agentic/provider/startplan"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAuthHook_StartPlanInjectsDeviceMid verifies every Start Plan request
// carries X-Device-Mid: the backend answers a headerless request with
// 400 {"code":3001} (parameter error) — a local defect, never an outage.
func TestAuthHook_StartPlanInjectsDeviceMid(t *testing.T) {
	dir := t.TempDir()
	startplan.SetDeviceMidDir(dir)
	t.Cleanup(func() {
		startplan.SetDeviceMidDir("")
		startplan.ResetDeviceMid()
	})

	h := &AuthHook{}
	require.NoError(t, h.Init(schema.VariantProfile{
		Auth: schema.AuthConfig{Header: "Authorization", Prefix: "Bearer "},
	}))
	ctx := &RequestContext{
		Model:   schema.Model{Provider: schema.ProviderZaiStartPlan},
		Options: schema.StreamOptions{APIKey: "zjwt-test"},
		Headers: make(map[string]string),
	}
	require.NoError(t, h.ApplyRequest(ctx))

	mid := startplan.DeviceMid()
	require.NotEmpty(t, mid)
	assert.Equal(t, mid, ctx.Headers["X-Device-Mid"])
}

// A caller-supplied device header (staging mirror, manual test) must win.
func TestAuthHook_StartPlanDeviceMidNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	startplan.SetDeviceMidDir(dir)
	t.Cleanup(func() {
		startplan.SetDeviceMidDir("")
		startplan.ResetDeviceMid()
	})

	h := &AuthHook{}
	require.NoError(t, h.Init(schema.VariantProfile{
		Auth: schema.AuthConfig{Header: "Authorization", Prefix: "Bearer "},
	}))
	ctx := &RequestContext{
		Model:   schema.Model{Provider: schema.ProviderZaiStartPlan},
		Headers: map[string]string{"Authorization": "Bearer preset", "X-Device-Mid": "custom-mid"},
	}
	require.NoError(t, h.ApplyRequest(ctx))
	assert.Equal(t, "custom-mid", ctx.Headers["X-Device-Mid"])
}

// Non-Start-Plan providers must NOT gain the header.
func TestAuthHook_NoDeviceMidForOtherProviders(t *testing.T) {
	dir := t.TempDir()
	startplan.SetDeviceMidDir(dir)
	t.Cleanup(func() {
		startplan.SetDeviceMidDir("")
		startplan.ResetDeviceMid()
	})

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
	_, has := ctx.Headers["X-Device-Mid"]
	assert.False(t, has, "X-Device-Mid must stay Start-Plan-only")
}

// A missing Start Plan credential must point at the family login — the
// zcode JWT is minted by /login:zai:oauth, not by any start-plan key source.
func TestAuthHook_StartPlanMissingCredentialHintsFamilyLogin(t *testing.T) {
	t.Setenv("ZAI_START_PLAN_API_KEY", "")
	h := &AuthHook{}
	require.NoError(t, h.Init(schema.VariantProfile{
		Auth: schema.AuthConfig{
			Header:   "Authorization",
			Prefix:   "Bearer ",
			Required: true,
		},
	}))
	ctx := &RequestContext{
		Model:   schema.Model{Provider: schema.ProviderZaiStartPlan},
		Headers: make(map[string]string),
	}
	err := h.ApplyRequest(ctx)
	var miss *schema.MissingCredentialError
	require.ErrorAs(t, err, &miss)
	assert.Contains(t, miss.Sources, "/login:zai:oauth",
		"sources must name the family login that mints the zcode JWT")
}

func TestAuthHook_StartPlanBigModelMissingCredentialHintsBigModelLogin(t *testing.T) {
	t.Setenv("BIGMODEL_START_PLAN_API_KEY", "")
	h := &AuthHook{}
	require.NoError(t, h.Init(schema.VariantProfile{
		Auth: schema.AuthConfig{
			Header:   "Authorization",
			Prefix:   "Bearer ",
			Required: true,
		},
	}))
	ctx := &RequestContext{
		Model:   schema.Model{Provider: schema.ProviderBigModelStartPlan},
		Headers: make(map[string]string),
	}
	err := h.ApplyRequest(ctx)
	var miss *schema.MissingCredentialError
	require.ErrorAs(t, err, &miss)
	assert.Contains(t, miss.Sources, "/login:bigmodel:oauth")
}

func TestErrorHook_StartPlanBusinessCodes(t *testing.T) {
	profile := schema.DefaultProfile(schema.Model{
		Api:      schema.ApiOpenAICompletions,
		Provider: schema.ProviderZaiStartPlan,
	})
	tests := []struct {
		name        string
		status      int
		body        string
		retryable   bool
		rateLimit   bool
		overflow    bool
		description string
	}{
		{
			name:      "3008 busy retries as rate limit",
			body:      `{"code":3008,"msg":"concurrent limit"}`,
			retryable: true, rateLimit: true,
		},
		{
			name:      "3009 model busy retries",
			body:      `{"code":3009,"msg":"model busy"}`,
			retryable: true, rateLimit: true,
		},
		{
			name:      "3010 model busy alt retries",
			body:      `{"code":3010,"msg":"model busy"}`,
			retryable: true, rateLimit: true,
		},
		{
			name:      "1005 quota is terminal",
			body:      `{"code":1005,"msg":"quota exhausted"}`,
			retryable: false,
		},
		{
			name:      "3007 security verify is terminal",
			body:      `{"code":3007,"msg":"verify"}`,
			retryable: false,
		},
		{
			name:      "3001 parameter error is terminal",
			body:      `{"code":3001,"msg":"parameter error"}`,
			retryable: false,
		},
		{
			name:      "quota text with overflow words must not read as overflow",
			body:      `{"code":1005,"msg":"too many tokens used"}`,
			retryable: false, overflow: false,
		},
		{
			name:      "unknown code stays non-retryable",
			body:      `{"code":4242,"msg":"mystery"}`,
			retryable: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &ErrorHook{}
			require.NoError(t, h.Init(profile))
			ctx := &ErrorContext{
				Model:      schema.Model{Provider: schema.ProviderZaiStartPlan},
				Profile:    profile,
				StatusCode: tt.status,
				Body:       tt.body,
			}
			require.NoError(t, h.ApplyError(ctx))
			assert.Equal(t, tt.retryable, ctx.IsRetryable, "IsRetryable")
			assert.Equal(t, tt.rateLimit, ctx.IsRateLimit, "IsRateLimit")
			assert.Equal(t, tt.overflow, ctx.IsContextOverflow, "IsContextOverflow")
		})
	}
}

// An unparseable body must fall through to the generic classification —
// an outage must keep behaving like an outage (500 stays retryable), not
// become a local terminal error.
func TestErrorHook_StartPlanGenericFallbackOnUnparseableBody(t *testing.T) {
	profile := schema.DefaultProfile(schema.Model{
		Api:      schema.ApiOpenAICompletions,
		Provider: schema.ProviderZaiStartPlan,
	})
	h := &ErrorHook{}
	require.NoError(t, h.Init(profile))
	ctx := &ErrorContext{
		Model:      schema.Model{Provider: schema.ProviderZaiStartPlan},
		Profile:    profile,
		StatusCode: http.StatusInternalServerError,
		Body:       `upstream exploded`,
	}
	require.NoError(t, h.ApplyError(ctx))
	assert.True(t, ctx.IsRetryable, "a 500 must stay retryable via the generic rules")
}

// The classification must be scoped to Start Plan providers: the same body
// for another provider keeps the generic behavior.
func TestErrorHook_BusinessCodeIgnoredForOtherProviders(t *testing.T) {
	profile := schema.DefaultProfile(schema.Model{
		Api:      schema.ApiOpenAICompletions,
		Provider: schema.ProviderOpenAI,
	})
	h := &ErrorHook{}
	require.NoError(t, h.Init(profile))
	ctx := &ErrorContext{
		Model:      schema.Model{Provider: schema.ProviderOpenAI},
		Profile:    profile,
		StatusCode: http.StatusInternalServerError,
		Body:       `{"code":3008,"msg":"concurrent limit"}`,
	}
	require.NoError(t, h.ApplyError(ctx))
	// Generic path: 500 is in the default retryable set; the plan-code
	// override must not have downgraded/rewritten it.
	assert.True(t, ctx.IsRetryable)
	assert.False(t, ctx.IsRateLimit)
}

// The wrapped business error must keep the transport cause reachable
// (errors.Is/As through ProviderError) and ride the canonical retry flags.
func TestErrorHook_StartPlanBusinessErrorKeepsCauseAndFlags(t *testing.T) {
	profile := schema.DefaultProfile(schema.Model{
		Api:      schema.ApiOpenAICompletions,
		Provider: schema.ProviderZaiStartPlan,
	})
	h := &ErrorHook{}
	require.NoError(t, h.Init(profile))

	cause := errors.New("transport boom")
	ctx := &ErrorContext{
		Model:      schema.Model{Provider: schema.ProviderZaiStartPlan},
		Profile:    profile,
		StatusCode: http.StatusOK,
		Body:       `{"code":3008,"msg":"concurrent limit"}`,
		Err:        cause,
	}
	require.NoError(t, h.ApplyError(ctx))
	require.True(t, ctx.IsRateLimit)
	require.True(t, ctx.IsRetryable)

	out := ctx.ToError()
	assert.True(t, errors.Is(out, cause), "cause must stay reachable through the wrap")

	var be *startplan.BusinessError
	require.ErrorAs(t, out, &be)
	assert.Equal(t, startplan.CodeBusy, be.Code)
}
