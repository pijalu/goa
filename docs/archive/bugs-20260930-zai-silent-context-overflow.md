<!--
  SPDX-License-Identifier: GPL-3.0-or-later

  Copyright (C) 2026 Pierre Poissinger
-->

# Bug: z.ai accepts context overflow silently — no context window for models the catalog does not know (LOW)

Date: 2026-09-30 · Status: FIXED — implemented, tests RED-before-fix, gates clean.

## Reported

> `opencode` records the quirk
> (`packages/opencode/src/provider/error.ts:31`): *"z.ai: can accept overflow
> silently (needs token-count/context-window checks)"*. Goa's z.ai profile has
> `context_window: 0`, so there is no proactive guard, and `OnContextError`
> compression only triggers on an actual context-length error that z.ai may
> never raise.

Expected: overflow is detected by token count before the request, not only by
provider error.

Evidence: diagnostic bundle `docs/research/zai-connection-review-20260930.md`
(D6), sessions `zai`/`glm-5.3-flash` and `opencode-go`/`space-bunny-free`.

## Root cause

Goa's context guard is entirely token-count driven: `Agent.effectiveMaxTokens`
returns 0 when no window is known, and every downstream decision
(`checkContextLimit`, the soft/hard compression tiers, the ceiling enforcer)
short-circuits on that 0. The reactive recovery (`OnContextError`) is the only
other path, and z.ai never raises the error it keys on. So the guard's bound is
the whole safety property.

The window was resolved from the embedded models.dev snapshot, keyed on the
model id. The snapshot carries the z.ai models as of its capture date; z.ai
ships new GLM generations continuously, and the active id a user runs is
whatever z.ai last released. For an id the snapshot does not carry:

- `models.GetModelForProvider` misses;
- `LookupByPrefix` misses too when the id introduces a new major version
  (`glm-99` shares no prefix with `glm-4.5`…`glm-5.3`);
- `buildFallbackModel` mints a minimal model — `ContextWindow: 0`.

The provider itself had no say: `schema.ProviderDef` could not express "this
platform accepts over-window requests silently, so you need a bound even for
an unknown model". Every provider-scoped fallback had to be hardcoded where the
resolver happened to live, and none existed.

## Fix

**A per-provider context-window floor, declared as data**
(`internal/agentic/provider/schema/catalog.go`)

- `ProviderDef` gains `DefaultContextWindow int`: the window to assume when a
  model resolved for this provider declares none. Zero keeps the old "no bound"
  contract, so the floor is a per-provider decision, not a global guess.
- Set to `zaiContextWindowFloor = 131072` on both z.ai entries (`zai` coding and
  `zai-api` general) — the same platform, the same silent-accept behavior.
  131072 is the smallest window z.ai serves for a tool-calling CHAT model
  (GLM-4.5 / 4.5-Air / 4.5-Flash). Conservative on purpose: a floor ABOVE the
  real window would let compression fire after the silent overflow, which is the
  failure this fixes.

**One resolver primitive** (`internal/agentic/provider/models/registry.go`)

- `models.ResolveContextWindow(prov, modelID)` — provider-exact registry entry,
  else the global (first-wins) registry entry, else the declared floor, else 0.
  A known model therefore keeps its REAL window (GLM-5.2 stays 1M); only an
  undeclared model falls to the floor.

**Applied on both resolve paths** (`provider/`)

- `applyContextWindowFloor` fills `ContextWindow` only when it is still 0, called
  from `ResolveActiveModel` (the active-agent path) and from
  `resolveModelByName` (model picker / gateway path, including the minimal
  model). A user `context_window:` in model config is applied before the floor
  and therefore always wins — it is the user's statement about THIS deployment.

## Tests

`internal/agentic/provider/models/context_window_test.go`

- `TestResolveContextWindow_RegistryValueWins` — glm-5.2 → 1000000, never the
  floor (flooring it would throw away four fifths of the usable context).
- `TestResolveContextWindow_ZaiFloorForUnknownModel` — both z.ai identities, an
  unknown id → positive bound.
- `TestResolveContextWindow_FloorIsConservative` — the declared floor must not
  exceed the smallest window the provider actually serves to a text-input
  tool-calling model. This is the safety direction, asserted against the catalog
  rather than against a literal.
- `TestResolveContextWindow_NoFloorStaysUnknown` — openai / custom still
  resolve 0 for an unknown id.

`provider/manager_context_window_test.go` (active-model resolution)

- `TestResolveActiveModel_ZaiUnknownModelGetsWindow` — the regression: an
  unknown z.ai id resolves with a real window and passes the configured name
  through verbatim.
- `TestResolveActiveModel_ZaiKnownModelKeepsRegistryWindow` — glm-5.2 → 1M.
- `TestResolveActiveModel_UserContextWindowWins` — explicit model-config
  `context_window: 32768` (a smaller gateway window) is not replaced by the
  floor.
- `TestResolveActiveModel_NoFloorProviderStaysUnknown` — ollama unchanged.

`internal/agentic/agent_context_zai_overflow_test.go` (the projection +
threshold path the guard reads)

- `TestZaiBoundContext_ProjectionOverSoftCeilingCompresses` — a z.ai-shaped
  model (131072 window) at 90% projected occupancy: the soft tier fires and the
  history is compressed by token count, before any request goes out.
- `TestZaiBoundContext_OverHardCeilingRefusesTurn` — at 97% (> 95% hard) the
  turn is refused by `checkContextLimit` instead of being posted.
- `TestZaiBoundContext_UnderCeilingAllowsTurn` — 23% occupancy: the bound must
  not fire. A floor is a guard, not a permanent summarizer.
- `TestZaiUnboundedContext_GuardIsInert` — the control: the same conversation
  with `ContextWindow: 0` sails through untouched. This is the pre-fix state,
  pinned so the floor's value cannot be quietly dropped.

## RED-before-fix evidence

With the source changes stashed (tests kept):

- `TestResolveActiveModel_ZaiUnknownModelGetsWindow` FAIL —
  `ContextWindow = 0, want a positive bound for an unknown z.ai model`.
- The `models` package tests fail to build against the pre-fix API
  (`undefined: ResolveContextWindow`, `ProviderDef has no field
  DefaultContextWindow`) — the primitive did not exist.

## Validation

- `go build ./...` — clean.
- `go vet ./...` — clean.
- `gocognit -over 15`, `gocyclo -over 12` on the touched files — clean.
- `go test -count=1 ./internal/agentic/...`, `./provider/...` — pass.

## Residual risk

The floor is a conservative guess, not a probe: an undeclared z.ai model whose
real window is 1M gets compressed around 111k instead of 850k. That is the
deliberate direction — early compaction is recoverable, silent overflow is not —
and it only applies to ids the embedded snapshot does not carry. When the
snapshot is refreshed the model resolves with its real window and the floor stops
applying. A z.ai deployment whose window is SMALLER than 131072 behind a custom
endpoint is only protected by the prefix tier or an explicit
`context_window:` — same as any other provider.
