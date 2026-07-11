// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package hooks

// buildEnv creates an evaluation environment for template/expression use.
func buildEnv(ctx *RequestContext) map[string]any {
	return map[string]any{
		"model":   ctx.Model,
		"options": ctx.Options,
		"profile": ctx.Profile,
	}
}
