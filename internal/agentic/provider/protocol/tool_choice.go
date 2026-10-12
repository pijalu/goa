// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package protocol

import "github.com/pijalu/goa/internal/agentic/provider/schema"

// The final-step text-only collapse (P7) must never mutate the prompt-bearing
// request surface: the tools array, the system prompt and the history are what
// the provider caches, so removing the tools for one round moves the prefix
// divergence point to the front of the conversation and re-bills the entire
// prompt. The collapse therefore keeps the tool surface and expresses its
// intent on tool_choice — a prompt-neutral control field.
//
// supportsToolChoiceNone reports whether the model profile accepts that field
// on the collapse round. Defaults are per wire flavor, because upstream support
// differs:
//
//   - OpenAI completions / Anthropic / Mistral / Google: true. Their upstreams
//     accept the text-only control value next to a tools array (verified live
//     against api.kimi.com 2026-10-05: HTTP 200, prefix cache retained, no tool
//     call emitted).
//   - Responses family: false. Strict Responses upstreams (opencode Zen / muse
//     "Console", 2026-09-02) hard-400 on any tool_choice other than "auto", and
//     the Codex WebSocket reuse fingerprint compares tool_choice, so the
//     collapse round repeats the previous round's choice verbatim instead.
//
// An explicit profile value (schema.CompatFlags.SupportsToolChoiceNone) always
// wins, so a model whose upstream differs from its flavor default is one config
// line away from the right behavior.
func supportsToolChoiceNone(profile schema.VariantProfile) bool {
	return toolChoiceNoneAllowed(profile, true)
}

// supportsToolChoiceNoneResponses resolves the same capability for the
// Responses family, whose default is false (see supportsToolChoiceNone).
func supportsToolChoiceNoneResponses(profile schema.VariantProfile) bool {
	return toolChoiceNoneAllowed(profile, false)
}

// toolChoiceNoneAllowed applies an explicit profile override when present and
// the flavor default otherwise. Pure.
func toolChoiceNoneAllowed(profile schema.VariantProfile, flavorDefault bool) bool {
	if profile.Compat.SupportsToolChoiceNone != nil {
		return *profile.Compat.SupportsToolChoiceNone
	}
	return flavorDefault
}
