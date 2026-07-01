// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package hooks

import "github.com/pijalu/goa/internal/agentic/provider/schema"

// buildEnv creates an evaluation environment for template/expression use.
func buildEnv(ctx *RequestContext) map[string]any {
	return map[string]any{
		"model":   ctx.Model,
		"options": ctx.Options,
		"profile": ctx.Profile,
	}
}

// applyProfileTemplates resolves templates in profile fields.
func applyProfileTemplates(profile schema.VariantProfile, env map[string]any) schema.VariantProfile {
	out := profile
	for i, h := range out.Headers {
		out.Headers[i].Value = schema.ApplyTemplate(h.Value, env)
	}
	return out
}
