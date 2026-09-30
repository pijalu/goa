<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bug: no `max_tokens` cap on reasoning-capable providers — reasoning can starve the answer (MEDIUM, z.ai-specific)

Date: 2026-09-30 · Status: FIXED — implemented, tests RED-before-fix, gates clean.

## Reported

> **Observed.** No `max_tokens` / `max_completion_tokens` in the captured z.ai body;
> the user model profile sets `thinking_level: xhigh`, `max_tokens: 0`. Export A
> request 17 spent 4 268 reasoning tokens before its first tool call and ran 80.7s.
>
> `pi` documents the hazard (`packages/ai/src/types.ts:834-840`): reasoning and the
> answer share `max_tokens`, so without a budget a reasoning-heavy turn can consume
> the whole response and emit no answer.
>
> **Expected.** Reasoning-capable providers receive an explicit output cap so the
> answer cannot be starved by reasoning.

Evidence: diagnostic bundle `docs/research/zai-connection-review-20260930.md`
(two sessions: `zai`/`glm-5.3-flash` and `opencode-go`/`space-bunny-free`).

## Root cause

The OpenAI-completions builder already materialized a cap, but only from two
sources: the explicit request value and the per-provider catalog
`default_max_tokens` (`internal/agentic/provider/protocol/openai_completions_messages.go`,
`resolveRequestMaxTokens`). `default_max_tokens` is set for DeepSeek only, so every
other completions route — z.ai (`zai`, `zai-api`) included — omitted the cap
entirely when the user left `model.max_tokens` at 0 (the shipped default). The
comment on that code path explicitly forbade the model's known output ceiling as a
source ("model.MaxTokens is deliberately not used here: it is a model hard limit,
not an adapter default cap"), which left reasoning-capable providers uncapped.

## Fix

`resolveRequestMaxTokens` gains a reasoning branch
(`internal/agentic/provider/protocol/openai_completions_messages.go`):

1. explicit request value wins (`model.max_tokens` in config — "skip when the user
   set an explicit value");
2. else the provider catalog `default_max_tokens` (unchanged, P21/DS2);
3. else, **only for a reasoning-capable model** (`model.Reasoning`, the models.dev
   `reasoning` flag — `internal/agentic/provider/models/modelsdev.go`), cap at the
   model's known output ceiling (`model.MaxTokens`, models.dev `limit.output`);
4. else, when the ceiling is unknown, `FallbackReasoningMaxTokens` (16384) so the
   request is never unbounded.

The cap rides `compat.MaxTokensField` like every other value on that body:
`max_tokens` for the z.ai variant profile, `max_completion_tokens` for routes that
declare no override. Materialized defaults stay observable: the existing
`protocolLog` line now names the source (`reasoning-ceiling` /
`reasoning-fallback`).

Non-reasoning providers are untouched by construction — the branch is gated on
`model.Reasoning`, so a completions route with neither an explicit cap nor a
catalog default still sends no cap and the server applies its own default.

## Tests

`internal/agentic/provider/protocol/openai_max_tokens_reasoning_test.go` (payload
assertions on the built request body):

- `TestMaxTokens_ReasoningModelCappedAtOutputCeiling` — z.ai `glm-5.2`
  (reasoning, ceiling 131072) with no configured cap → `"max_tokens": 131072`,
  and no stray `max_completion_tokens` (the provider's MaxTokensField wins).
- `TestMaxTokens_ExplicitUserCapWins` — explicit 4096 beats the ceiling.
- `TestMaxTokens_CatalogDefaultBeatsReasoningCeiling` — DeepSeek keeps its
  catalog `default_max_tokens` 256000 even on a reasoning model with a ceiling.
- `TestMaxTokens_ReasoningCeilingUnknownFallsBack` — ceiling 0 →
  `FallbackReasoningMaxTokens`.
- `TestMaxTokens_NonReasoningProviderUnchanged` — non-reasoning route stays
  uncapped.
- `TestMaxTokens_RespectsMaxTokensFieldCompat` — the cap lands in whatever
  `MaxTokensField` the route declares.

## RED-before-fix evidence

With the source change stashed (tests kept, plus a temporary constant so the
package compiled):

- `TestMaxTokens_ReasoningModelCappedAtOutputCeiling` FAIL — `req["max_tokens"]`
  is nil on the z.ai reasoning model.
- `TestMaxTokens_ReasoningCeilingUnknownFallsBack` FAIL — no cap at all.
- `TestMaxTokens_RespectsMaxTokensFieldCompat` FAIL — the reasoning cap is
  missing from the configured field.
- The three guard tests (explicit wins, catalog default wins, non-reasoning
  unchanged) pass before and after: they pin behavior the fix must not alter.

## Validation

- `go build ./...` — clean.
- `go vet ./internal/agentic/provider/protocol/` — clean.
- `staticcheck ./internal/agentic/provider/protocol/` — clean.
- `gocognit -over 15`, `gocyclo -over 12` on the touched package — clean.
- `go test -count=1 ./internal/agentic/...` — pass.

## Residual risk

- The derived cap is the model's *ceiling*, not a tuned answer budget: a
  reasoning-heavy turn can still consume most of it before answering. z.ai's
  thinking body carries no reasoning budget (`thinking:{type:enabled}` has no
  numeric field), so bounding reasoning below the shared cap is not expressible on
  this wire format; the answer share is protected by the ceiling plus the
  `FallbackReasoningMaxTokens` floor for ceilings the catalog does not know.
- Only the OpenAI-completions family is covered — that is where `MaxTokensField`
  lives. The Responses builder (`applyResponsesSamplingFields`,
  `max_output_tokens`) and the Anthropic/Google/Bedrock builders were left alone:
  Anthropic already sends a bounded `max_tokens`, and the others are outside this
  bug's fix plan.
- Any provider that rejects large `max_tokens` values would now see the model's
  ceiling on reasoning models; an explicit `model.max_tokens` overrides it.
