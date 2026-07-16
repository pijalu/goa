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

// A 404 ("model not found") is permanent for nearly every provider — retrying
// a wrong model ID cannot succeed. Only Codex/OpenAI treat a 404 as worth a
// single bounded retry (a documented quirk of their deployment). The error
// hook must therefore scope the 404-retryable rule to OpenAI profiles, not
// apply it unconditionally. Regression test for the bug where every provider's
// 404 burned the retry budget with "Reconnecting…" churn before failing.
func TestErrorHook_404RetryableScope(t *testing.T) {
	cases := []struct {
		name           string
		provider       string
		wantRetryable  bool
	}{
		{"openai 404 retried once", "openai", true},
		{"anthropic 404 surfaces immediately", "anthropic", false},
		{"google 404 surfaces immediately", "google", false},
		{"deepseek 404 surfaces immediately", "deepseek", false},
		{"lm-studio 404 surfaces immediately", "lm-studio", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &ErrorHook{}
			profile := schema.VariantProfile{
				ID:    tc.provider + "-base",
				Match: schema.ProfileMatch{Provider: tc.provider},
			}
			require.NoError(t, h.Init(profile))

			ctx := &ErrorContext{
				StatusCode: 404,
				Body:       `{"error":{"message":"model 'typo-model' not found","type":"not_found_error"}}`,
				Profile:    profile,
			}
			require.NoError(t, h.ApplyError(ctx))
			assert.Equal(t, tc.wantRetryable, ctx.IsRetryable,
				"404 retryable scope for provider %q", tc.provider)
		})
	}
}
