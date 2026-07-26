<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bug/Feature Tracking — Archive 2026-07-26 (poolside + provider catalog + MCP wizard)

All items below were fixed, tested, and validated this session.


## Open Items

*(none — all items fixed; see Closed below, pending archive)*

---

## Closed (pending archive)

### F4. MCP wizard (add/edit/delete) shared by `/mcp` and `/config` ✅
- One reusable wizard (`core/commands/mcp_wizard.go`) drives add/edit/delete/toggle; every mutation delegates to the existing `MCPCommand` subcommands so persistence + live connect/disconnect + agent-tool refresh match the CLI exactly.
- `/mcp:wizard` (alias `wiz`, `edit`) launches it standalone; `/config → MCP servers → "＋ Add / edit / delete servers"` launches the SAME wizard via `runMCPWizardOnMenu`. UX parity proven by test.
- New `upsertMCPServer` reuses the `/mcp:add` path (upsert → persist → connect) so wizard saves are byte-identical to CLI adds.
- Filmstrip-validated (guideline #5): 7 scripted-sequence tests in `core/commands/mcp_wizard_test.go` assert the rendered prompts/options AND resulting config — add remote/local, edit URL, delete confirm yes/no, toggle, and `/mcp`-vs-`/config` parity.
- Verified: `go test ./core/commands/` green; vet/staticcheck/gocognit/gocyclo clean on touched files; full-repo `go test ./...` 79 ok / 0 FAIL; race+cover green.

### F1. Poolside provider preset ✅
- Preset + catalog identity + env key (`POOLSIDE_API_KEY`) + URL fingerprint + endpoint heuristic. Now declared once in `internal/agentic/provider/schema/catalog.go` (F3).
- Tests: `presets_test.go`, `compat_detect_test.go` (Poolside), `manager_test.go` (identity, endpoint, traits), `schema/catalog_test.go`.
- Verified: `go test ./config/... ./provider/... ./internal/agentic/provider/...` green; vet/staticcheck/gocognit/gocyclo clean (pre-existing `validateOrchestrator` + `modelsdev` unused noted, unrelated).

### F2. Tri-state `ModelConfig.Reasoning` (omitted = enabled) ✅
- `Reasoning` → `*bool` (nil = default on). Fallback defaults true; registry override only when non-nil. Explicit `reasoning:false` still disables.
- Regression tests: `TestApplyModelConfigToFallback_ReasoningDefault`, `TestApplyModelConfigCapabilities_ReasoningOverride` — green.
- Verified: race+cover green (provider 77.4%, config 55.9%).

### F3. Template-driven provider catalog ✅
- Single `ProviderDef` table in `internal/agentic/provider/schema/catalog.go` now drives: wizard presets, env-key lookup, URL fingerprinting/compat, endpoint heuristics, `ValidAgenticProviders`, `knownProviderPrefixes`, modelsdev mappings.
- Compat quirks are data (`ProviderCompat`), not fingerprint branches. New provider = one catalog entry.
- Consumers rewired: `presets.go`, `env_keys.go`, `compat_detect.go`, `manager.go`, `agentic_constants.go`, `modelsdev.go`.
- Behavior preserved: all `TestDetectOpenAICompat_*` (incl. URL-only nvidia/ant-ling/cerebras fallback), `TestPresetProviders_*`, catalog tests (`NoDuplicateIDs`, `LookupProviderDef`, `MatchProviderByNameOrURL_Poolside/ZaiPrecedence`) green. `isNonStandard` gocyclo brought under budget via `anyTrue`.
- Verified: full provider+config tree race+cover green; vet/staticcheck/gocognit/gocyclo clean.

---

### B1. Goal block truncates reason/expectation — user can't see why goal blocked ✅
- **Observed**: `◦ Goal blocked by the agent (ctrl+o: P1B parser/engine fixes complete (Steps 2-4 done, WINDOW clause fixed, CTE+VALUES` — reason/expectation cut off mid-sentence; the "ctrl+o" hint implied expansion that didn't exist.
- **Root cause** (`tui/goal/markers.go`): `Render` returned ONE line; `headline()` inlined reason+expectation, and `padToWidth` byte-sliced the raw string (`return s[:width]`), truncating text mid-word and mid-ANSI-escape. `HandleInput` was a no-op, so the ctrl+o hint was a lie.
- **Fix**: headline = status+actor only; reason & expectation now render as separate `ansi.Wrap`-wrapped, indented detail lines. No truncation; removed the false ctrl+o affordance.
- **Regression tests**: `TestMarker_BlockedShowsFullReasonAndExpectation` (full long reason+expectation survive wrapping), `TestMarker_NoReasonStaysSingleLine`, updated `TestMarker_Paused`. Filmstrip-validated render @ width 80 (guideline #5).
- **Verified**: `go test ./tui/goal/` green, race+cover 94.9%, vet/staticcheck/gocognit/gocyclo clean.
