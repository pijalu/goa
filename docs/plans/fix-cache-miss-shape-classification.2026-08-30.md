<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<!-- Copyright (C) 2026 Pierre Poissinger -->

# Fix plan — Unclear cache miss: real miss or switch of context (bugs.md 2026-08-30)

## Report

"Cache miss ?" — the user cannot tell from the `/stats:cache` report whether a
reported unexpected miss is a REAL provider miss or an intentional context /
request-shape change.

## Evidence & root cause (export `goa-export-20260830-132638.zip`, workspace creaves)

`logs/cache_miss_requests.json` holds one bust: prev read 80,768 → 0 at
13:07:08 (seq 47 → 48, model glm-5.3-flash, provider zai). Decomposition of
both captured request bodies:

- Same `session_id` / `prompt_cache_key`.
- All 121 leading messages byte-identical (canonical prefix INTACT — the
  conversation was NOT switched and nothing was rewritten).
- The bust request DROPPED the `tools` array (9 tools → absent) and ADDED
  `tool_choice: "none"`.

Conclusion: NOT a real provider miss and NOT a conversation switch — it is
Goa's intentional P7 text-only collapse (`internal/agentic/agent.go`
`toolCollapseNextRound`): the turn's final summary round drops tools, the
request shape changes, and the provider prefix cache misses BY DESIGN. The
provider forensics already classify this precisely
(`internal/agentic/provider/cache_fingerprint.go`
`PrefixToolPolicyTransition`), but that classification never reaches the
session-scoped completion log, so `/stats:cache` `scanMisses` files the bust
under "unexpected" — exactly the user's confusion.

## Fix

Plumb the collapse signal to the completion record and classify it in the
report:

- F1 `internal/agentic`: when a stream round runs with the P7 collapse
  (request carries no tools + `tool_choice "none"`), make that round's token
  stats observable (extend the existing token-stats event path with a flag —
  follow the `EventContextReset` precedent: emit → observe).
- F2 `core/turnrecorder.go`: `RecordCompletion` (or the event handler in
  `core/agentmanager_events.go`) latches the flag into a new
  `CompletionRecord` field (e.g. `TextOnlyCollapse bool`), mirroring
  `ContextReset`.
- F3 `core/commands/stats_cache.go`: `cacheTurn` carries the flag;
  `scanMisses` treats a bust on a text-only-collapse call as a third kind —
  a known, intentional request-shape change — NOT "unexpected": no miss
  classified for that call's loss is charged to the unexpected counters; the
  misses table Kind column shows it (e.g. `no-tools step`) so the report
  answers "real miss or switch of context" at a glance.
- F4 Global headline counters keep consistency: unexpected/partial counts
  exclude no-tools events (same rationale as intentional resets).

## Test approach (test-first)

- T1 `core/turnrecorder_test.go`: completion recorded with the collapse flag
  carries it through `CompletionHistory()`.
- T2 `core/commands/stats_cache_test.go`:
  - a bust (read → 0 after establishment) on a collapse call is classified
    `no-tools step`, not `unexpected`; global headline excludes it from the
    unexpected count;
  - a bust on a NORMAL call still classifies `unexpected` (no regression);
  - intentional resets still restart the baseline (no regression).
- T3 `internal/agentic` event test: the collapse round's stats event carries
  the flag; normal rounds do not.

## Validation steps

1. Package tests: `go test ./core/... ./internal/agentic/... -count=1 -race
   -timeout 120s`.
2. Cross-check with the archived export scenario: replay the seq47→48 shape
   (prefix intact + tools dropped) in a unit fixture reproduces the new
   classification.
3. Quality gates, each run separately: `go vet ./...`, `staticcheck ./...`,
   `gocognit -over 15 .`, `gocyclo -over 12 .`,
   `go test -count=1 -race -cover ./...`. Pre-existing complexity warnings
   unrelated to the change are acceptable if noted.
4. Verify the rendered `/stats:cache` misses table through the MD pipeline
   shows the distinct kind label.

## Closure

On green gates: move this plan to `docs/archive/`, empty the bugs.md entry,
commit with a descriptive message.
