<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bug: `tool_stream: true` is never sent to z.ai — `ZaiToolStream` was a dead flag (MEDIUM)

Date: 2026-09-30 · Status: FIXED — implemented, tests RED-before-fix, gates clean.

## Reported

> `ZaiToolStream` is declared in `internal/agentic/provider/compat.go:25` and
> `internal/agentic/provider/protocol/openai_completions.go:77`, hardcoded
> `boolPtr(false)` in `compat_detect.go:118`, and then never read — nothing writes
> `tool_stream` into the request body. The live body captured in export A
> (`logs/cache_miss_requests.json` → `requests[0].body`) has keys `messages,
> model, prompt_cache_key, prompt_cache_retention, stream, stream_options,
> thinking, tools` — no `tool_stream`.

Evidence: diagnostic bundle `docs/research/zai-connection-review-20260930.md`
(two sessions: `zai`/`glm-5.3-flash` and `opencode-go`/`space-bunny-free`).
`pi` sends it (`packages/ai/src/api/openai-completions.ts:851-853`) and its
generated z.ai catalog sets `compat.zaiToolStream: true` for GLM-4.6 → GLM-5.3.

## Root cause

The capability existed in two places and neither carried it:

1. **Catalog/profile data** had no field for it — `schema.ProviderCompat` and
   `schema.CompatFlags` could not express "this endpoint wants tool_stream", so
   there was nothing for the protocol layer to read.
2. **Detection** hardcoded `ZaiToolStream: boolPtr(false)`, so even the declared
   flag was inert.

Consequently both request builders (`provider/protocol.buildOpenAIParams` — the
live wire path — and the legacy `provider/openai.buildParams`) never emitted the
field, and z.ai served tool calls on the default channel.

## Fix

**Catalog as the single source of truth** (`internal/agentic/provider/schema/catalog.go`)

- `ProviderCompat` gains `ToolStream bool`: send the top-level
  `"tool_stream": true` next to the tools array.
- Set on both z.ai entries (`zai` coding and `zai-api` general) — the capability
  is an endpoint property of the platform, not of one catalog row.

**Wire path** (`internal/agentic/provider/protocol/`)

- `CompatFlags` gains `ToolStream bool` (`json:"tool_stream"`), merged with
  set-if-true semantics in `mergeCompat`, so a user variant profile can raise the
  capability for an endpoint not in the catalog.
- `resolveOpenAICompat` sets `c.ZaiToolStream = profile.Compat.ToolStream ||
  catalogToolStream(model)`; `catalogToolStream` resolves through
  `schema.MatchProviderByNameOrURL`, so a `custom` provider pointed at
  `api.z.ai` (or `open.bigmodel.cn`) also gets the field — the same rule
  `maxTokensField`/`ThinkingFormat` already follow.
- New `applyTools` helper owns the whole tool-call surface of the request
  (tools array + `tool_choice` + `tool_stream`), keeping `buildOpenAIParams`
  inside its complexity budget (gocyclo was 14 with the field inlined). The
  field only rides along when a tools array is actually sent — never on the P7
  final-step collapse (`NoTools`).

**Provider layer** (`internal/agentic/provider/`)

- `fingerprintProvider.supportsToolStream()` replaces the hardcoded `false`:
  catalog entry authoritative, `isZai` fingerprint as fallback.
- Legacy `openai.buildParams` emits the field the same way, so the declared
  `OpenAICompletionsCompat.ZaiToolStream` is no longer dead on either stack.

## Tests

`internal/agentic/provider/protocol/zai_tool_stream_test.go` (payload assertions
on the built request body):

- `TestZaiSendsToolStreamWithTools` — z.ai + tools → `"tool_stream": true`.
- `TestZaiOmitsToolStreamWithoutTools` — absent when no tools array.
- `TestZaiOmitsToolStreamOnNoToolsCollapse` — absent on the final-step collapse.
- `TestZaiApiSendsToolStream` — general z.ai endpoint (no dedicated variant
  profile; resolved from the catalog).
- `TestZaiToolStreamByURLForCustomProvider` — `custom` provider id on a z.ai host.
- `TestNonZaiProvidersOmitToolStream` — openai, deepseek, opencode-go unchanged.

`internal/agentic/provider/compat_detect_test.go`:

- `TestDetectOpenAICompat_ZaiToolStream` — every z.ai identity (both provider
  names, URL fingerprint, CN bigmodel host) detects the capability.
- `TestDetectOpenAICompat_ToolStreamOffWithoutZai` — openai, deepseek,
  opencode-go and a non-z.ai `custom` endpoint keep it off.

`internal/agentic/provider/openai/stream_tool_stream_test.go`:

- `TestBuildParams_ToolStream` — legacy builder: emitted for z.ai with tools,
  absent without tools, absent on the collapse, absent for openai.

## RED-before-fix evidence

With the source changes stashed (tests kept):

- `TestZaiSendsToolStreamWithTools`, `TestZaiApiSendsToolStream`,
  `TestZaiToolStreamByURLForCustomProvider` FAIL — `req["tool_stream"]` is nil.
- `TestDetectOpenAICompat_ZaiToolStream` FAILs on all four sub-cases —
  `ZaiToolStream = false, want true for z.ai`.
- `TestBuildParams_ToolStream` FAILs in the legacy package.

## Validation

- `go build ./...` — clean.
- `go vet ./...` — clean.
- `staticcheck ./internal/agentic/...` — clean.
- `gocognit -over 15`, `gocyclo -over 12` on the touched packages — clean.
- `go test -count=1 ./internal/agentic/...` — pass.

## Residual risk

`tool_stream: true` was accepted by z.ai in pi's traffic but not live-probed from
goa; if a future GLM generation rejects the field, the capability is a single
catalog boolean per provider (`ProviderCompat.ToolStream`) that can be turned off
without touching the wire code. Non-z.ai providers are unaffected by
construction — the field can only be produced from a catalog/profile flag.