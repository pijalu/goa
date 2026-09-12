# Provider 400: DeepSeek V4.1 on InferX rejects reasoning_effort (bugs-20260912-inferx-reasoning-effort)

## Observed
Every turn failed with `LLM request failed (not retryable): {"error":{"message":
"DeepSeek V4.1 reasoning_effort must be low, high, xhigh, max, or an integer
within [1, 100] in chat_template_kwargs","type":"invalid_request_error"}}`.
Evidence: `/Users/muaddib/dev/brol/.goa/exports/goa-export-20260912-192852.zip`
(both requests HTTP 400; model `deepseek-v4.1-flash` on user-defined provider
`inferx`, config `thinking_level: xhigh`).

## Root cause
`ClampThinkingLevelWithMap` (internal/agentic/provider/options.go) passed only
low/medium/high through when a model has no `ThinkingLevelMap` and rewrote
every other level — including configured `xhigh` and `max` — to `medium`.
InferX's DeepSeek V4.1 rejects `medium` (its value domain is
low/high/xhigh/max/int 1..100), so the "safe" downgrade manufactured the 400.
Chain: config `thinking_level: xhigh` → `applyModelStreamOptions` (raw
passthrough) → `BuildSimpleOptions` → clamp rewrote to `medium` →
`applyThinking` (DefaultProfile ThinkingFormat "openai") → wire
`reasoning_effort: "medium"` → 400. Reproduced exactly via a throwaway repro
before fixing; wire body showed `"reasoning_effort":"medium"`.

Same class: `thinkingLevelOrder` did not rank `ThinkingMax`, so
`nearestThinkingLevel` returned medium for a requested `max` even when the
model's map supports it. The downgrade behavior was codified by
`TestClampThinkingLevel_NoMap` (asserted xhigh → medium).

## Change
- `ClampThinkingLevelWithMap`: the no-map passthrough now covers all canonical
  levels (minimal, low, medium, high, xhigh, max); non-canonical values still
  fall back to medium.
- `thinkingLevelOrder`: added `ThinkingMax` so map-based clamping can rank it.
- `TestClampThinkingLevel_NoMap`: corrected to the passthrough contract (the
  old expectation codified the defect).

## Test approach
- `TestClampThinkingLevel_NoMap` — xhigh/max/minimal passthrough; unknown → medium.
- `TestNearestThinkingLevel_RanksMax` — map {high, max}: requested max → max;
  requested xhigh clamps to high.
- `thinking_clamp_regression_test.go` — wire-level regression walking the exact
  production chain for an unregistered provider (config level →
  `BuildSimpleOptions` → `protocol.BuildRequest`): asserts
  `reasoning_effort` == `xhigh` and `max` in the request body. This test would
  have caught the incident.

## Validation
- `go test -count=1 -timeout 120s ./internal/agentic/provider/` — PASS.
- Targeted: TestClampThinkingLevel*, TestNearestThinkingLevel_RanksMax,
  TestBuildSimpleOptions_Configured* — all PASS.
- `go vet ./...` — clean.
- `staticcheck ./...` — clean.
- `gocognit -over 15 .` / on internal/agentic/provider — clean.
- `gocyclo -over 12 .` — one pre-existing unrelated warning
  (tools/editfile.go `(*EditFileTool).editByOperation` = 13, untouched here).
- `go test -count=1 -race -cover ./...` — exit 0.

## Residual risk
The InferX error text mentions `chat_template_kwargs`. Evidence indicates the
400 was value-driven (the server validated the `medium` value we sent
top-level), so top-level `xhigh` should be accepted. If InferX additionally
rejects top-level placement, the followup is a user-side variant profile
override in `~/.goa/providers/*.json` (thinking format), not a code change.
