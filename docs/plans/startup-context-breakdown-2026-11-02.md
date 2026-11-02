<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Startup Context Breakdown — bugs.md B52 (2026-11-02)

End-to-end measurement of the fixed first-request payload: the system prompt
(what `buildSystemPrompt` sends) plus every eager tool schema (what
`migrateSchemas(reg.Schemas())` sends with request #1, before any deferred
tool is loaded). Repeatable via:

    go test -run TestStartupContextBreakdown -v ./internal/app

`StartupContextBreakdown` (internal/app/startup_context.go) reports bytes and
estimated tokens (chars/4, same estimator as `systemPromptBudget`) per
component; `TestStartupContextBudget` pins the total. The breakdown is
deterministic: embedded prompts, embedded skills, a fixed synthetic
AGENTS.md, and schema JSON over the real default config cascade.

## Baseline (pre-B52)

Default headless start (default config cascade, shipped skills = telegram
only, one ~1.2KB AGENTS.md, no MCP servers, no plugins):

| component              | bytes | ~tokens |
|------------------------|------:|--------:|
| system:mode-prompt     |   603 |     151 |
| system:tool-usage      |    60 |      15 |
| system:project-context |  1270 |     318 |
| system:skills-catalog  |   282 |      71 |
| system:self-doc        |   434 |     109 |
| tool:goal              |  4671 |    1168 |
| tool:edit              |  2247 |     562 |
| tool:mcp               |  2008 |     502 |
| tool:search            |  1072 |     268 |
| tool:ask_user_question |   730 |     183 |
| tool:bash              |   641 |     161 |
| tool:tool_search       |   620 |     155 |
| tool:webfetch          |   591 |     148 |
| tool:read              |   577 |     145 |
| tool:run_skill         |   476 |     119 |
| tool:todo_list         |   430 |     108 |
| tool:write             |   288 |      72 |
| **TOTAL**              |**31351**|**7838**|

Eager schemas alone: 14,351 bytes (~3,588 tokens). **Deferral was dormant**:
`agentic.PartitionDeferred` requires ≥ `DeferralThreshold` (then 8)
deferred-eligible tools plus the tool_search loader, but the B46 minimal
default tool set leaves only mcp, webfetch, run_skill, and todo_list
deferred-marked (4 < 8). The P1 deferred-loading design (P1,
docs/plans/context-usage-tool-skills-review.md; tools/deferred.go) never
engaged on any default session — four schemas the deferred set explicitly
meant to withhold shipped eagerly on every request.

## Waste review

| finding | evidence | action |
|---|---|---|
| Deferral permanently inactive on default sets | 4 deferred-eligible < threshold 8; the embedded default config even documents `mcp` as "deferred — the model discovers it via tool search" | **applied (R1):** `DeferralThreshold` 8 → 2 |
| `<deferred_tools>` prompt section duplicates the tool_search catalog (names + short descriptions twice) | deferred-tools section 524 bytes vs tool_search description 620 bytes, same tool list | kept: the prompt section is the first thing the system-prompt budget drops under pressure, while the loader description always ships — different failure modes, and 524 bytes is cheap |
| `tool:goal` 4,671 bytes | spec-grade by design, guarded at 5,000 (tools/schema_size_test.go) | kept |
| `tool:edit` 2,247 bytes | under its 2,500 per-tool ceiling | kept: trimming descriptions risks behavior; not a cheap win |
| system:tool-usage (60 B), self-doc (434 B), skills catalog (282 B) | already minimal post-B50 | kept |

## Applied reducer R1 — activate deferral on default sets

`internal/agentic/tool_registry.go`: `DeferralThreshold` 8 → 2. The catalog
indirection (~0.5KB, bounded by `catalogBudget` in tools/tool_search.go) is
now paid only when at least two schemas are actually withheld; with the
default set, four are.

Before/after (same breakdown harness):

| component              | before | after |
|------------------------|-------:|------:|
| tool:mcp               |  2008  |  — (deferred) |
| tool:webfetch          |   591  |  — (deferred) |
| tool:run_skill         |   476  |  — (deferred) |
| tool:todo_list         |   430  |  — (deferred) |
| system:deferred-tools  |   —    |  524  |
| tool:eager-total       | 14351  | 10846 |
| **TOTAL first request**| **31351** | **24865** |

**−6,486 bytes (~1,620 tokens) on every request, before any conversation
exists.** Withheld tools stay discoverable: named + one-line-described in the
`<deferred_tools>` prompt section, loadable via `tool_search
"select:<name>"`, redirect error on direct calls. Discoverability after the
change is pinned by `TestHeadless_ConfigEnabledAdvertisesAgentDrivenTools` /
`TestHeadless_CompanionOverridesStaleConfigFalse` (assert advertised **or**
listed in `<deferred_tools>`), and the loaded-tail survives runtime tool-set
pushes (C2 suite in internal/agentic/deferral_test.go).

## Budget pin

`TestStartupContextBudget` (internal/app/startup_context_test.go): total
first-request payload ≤ **26,000 bytes** (post-B52 baseline 24,865 + ~4.5%
headroom). Failing it means a component grew; shrink first, raise the ceiling
only with a fresh measured breakdown.

## Validation

- `go test -count=1 -race -timeout 600s ./...` — green
- `go vet ./...` — clean
- gocognit/gocyclo/staticcheck — clean for touched files
- Real-session check: headless companion flow (the two headless tests drive
  full `InitSubsystems` + headless turn against a capture provider) shows
  request_review/delegate_to listed in `<deferred_tools>`; skills catalog
  still rendered; run_skill loadable on demand.
