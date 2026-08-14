# Compression config rework — bugs & plan (IN PROGRESS)

Meta: mid-implementation. Engine/config/CLI edits landed (uncommitted). Menu rewrite + test fallout remain.
This file is the single source of truth; work from here, not from chat history.

---

## 1. Original user report

- All compression should be disabled by default (later refined, see decisions).
- Compression config menu unclear (too many rows, derived values mixed with settings).
- Bug: selecting a percentage-looking row "returns to main menu" instead of opening a picker.
- Expected menu:
  ```
  Soft ceiling %: XX% (0% => disabled)
  Soft ceiling method: micro
  Hard ceiling %: XX%
  Hard ceiling method: summarize
  On error: hybrid   (picker incl. "off")
  ```
  - Method rows: picker from ALL methods (micro/tool_elision/selective/hybrid/summarize).
  - Ceiling rows: picker of percentages 0,5,...,100 (step 5); 0 = disabled.

## 2. User decisions (from clarification round)

| Question | Decision |
|---|---|
| Defaults | Keep ON: hard ceiling 95% + method summarize, and on-context-error recovery (hybrid). Off: soft (0), trigger (0, hidden in Advanced). No implicit engine default-on — default.yaml sets `hard_percent: 95` explicitly. |
| Menu rows | 5 main rows: Soft ceiling % / Soft method / Hard ceiling % / Hard method / On error (single method picker where "off" = disabled). Plus "Advanced…" entry. |
| Percent list | 0,5,10,...,100 (step 5), 0 = disabled. |
| Soft method list | ALL methods on every layer (engine restriction removed). |
| Advanced settings | Keep functional, tucked under "Advanced…" submenu — "make sure functional and does not overcomplexify general params". |

## 3. Root cause of "returns to main menu" bug

- `core/commands/config_compression.go` old menu had read-only derived rows
  (`_derived_deferral`, `_derived_effective_hard`, `_derived_elision_target`,
  `_derived_escalation`, `_derived_reactive_savings`) with Values starting `_derived_`.
- Selecting such a row: `openers` map misses, `switch` misses → callback does nothing.
  TUI Selector on selection: emit(value) → done → overlay HIDE. No new overlay opened →
  user falls back to previous screen → looks like "returned to main menu".
  User screenshot cursor was on "↳ Deferral ceiling (cache-hot cutoff) 85%" — exactly such a dead row.
- Secondary trap: `ctx.SelectOption` wrapper treats empty-string Value as CANCEL
  (`ok := selected != ""`); any picker item with empty Value would act as cancel → `m.back()`.
  Rule: never use empty Value for selectable rows.
- Fix: new menu has zero dead rows; regression test must assert every row either opens
  a picker or applies a set.

## 4. Semantics change (old → new)

| Item | Old | New |
|---|---|---|
| hard_percent 0 (global) | default 95, hard tier ON | DISABLED (0 = off everywhere) |
| hard_percent negative | explicit opt-out | still accepted = disabled (legacy spelling) |
| hard default 95 | engine implicit (DefaultHardPercent) | explicit in `config/configs/default.yaml` (`hard_percent: 95`) |
| soft strategy | forced zero-LLM (micro/tool_elision), others silently degraded to micro | any strategy honored; empty → micro |
| on-error strategy | hardcoded hybrid (elision → selective → maybe Compact) | configurable `on_error_strategy`; empty = hybrid (same behavior) |
| config validation levels | 10–95 step 5, 0=inherit, -1 soft-only | 0 or -1 = disabled; else 5–100 step 5 |
| default.yaml `strategy` | "micro" | "" (legacy trigger-layer strategy unset; nothing proactive beyond hard 95) |
| enabled:false | zeroed thresholds but hard still defaulted ON via engine | zeroes ALL proactive layers incl. hard (0=disabled semantics) |
| SDK zero-value ContextCompressionConfig | hard tier on at 95 | fully disabled (matches its doc comment) |

Reactive safety net unchanged in spirit: `on_context_error: true` + hybrid default;
escalation-to-Compact tail preserved for hybrid/micro/tool_elision/selective;
`summarize` on-error goes straight to Compact.

## 5. Already-done edits (uncommitted, in working tree)

1. `internal/agentic/compression_thresholds.go`
   - Docs: every layer opt-in, 0/negative = disabled; DefaultHardPercent = reactive-math fallback only.
   - `hardEnabled()` → `hard > 0`.
   - `resolveThresholds`: `softStrategy = c.Strategies.Soft; if "" → micro` (no degradation).
   - Removed `zeroLLMStrategy` helper.
2. `internal/agentic/agent.go`
   - `ContextCompressionConfig` += `OnErrorStrategy CompressionStrategy` (empty = hybrid).
   - Doc: zero value disables automatic compression entirely.
3. `internal/agentic/agent_compression.go`
   - `handleContextError` logs resolved strategy.
   - New `onErrorStrategy()` resolver.
   - `compressOverflowRecovery`: dispatch — summarize → Compact; else
     `overflowRecoveryInMemory(strategy)` (hybrid: elision+selective under one lock, unchanged;
     micro: `microCompactForced(true)` lock-free; selective/tool_elision: single op under lock)
     then escalate-to-Compact tail when `stats.UsagePercent >= escalationPercent()`, else emit
     "overflow" compaction result; emitContextStats at end.
4. `config/config.go`
   - `ContextCompressionConfig` += `OnErrorStrategy string` yaml `on_error_strategy,omitempty`.
   - Thresholds doc comments updated (0 = disabled global; negative legacy).
5. `config/configs/default.yaml`
   - `hard_percent: 95`, `on_error_strategy: "hybrid"`, `strategy: ""`, comments rewritten.
6. `config/config_validate.go`
   - validates `on_error_strategy` via `validCompressionStrategy`.
   - `validateLayerStrategies`: soft restriction removed.
   - `validateCompressionLevel(ve, path, v)`: 0/-1 pass; else 5..100 %5==0; msg updated.
     (signature changed — dropped `allowDisable bool`; all 3 call sites updated.)
7. `config/config_merge.go`
   - merges `OnErrorStrategy` (non-empty wins); added to `contextCompressionLayerEmpty`.
8. `core/commands/config_cli.go`
   - new `setOnErrorStrategy`; key `context_compression.on_error_strategy`.
   - `setLayerStrategy` dropped `soft` param (any strategy any layer); 3 call sites updated.
9. `core/agentmanager_lifecycle.go`
   - maps `OnErrorStrategy` into agentic config; enabled:false comment updated.

NOT yet compiled/tested since these edits — expect build fallout first (task 01).

## 6. Remaining work — micro-tasks (ordered)

**STATUS 2026-08-14: ALL DONE (01–09).** Evidence: `go vet ./...` clean;
`go test -count=1 -race -cover ./...` green (81 pkgs ok, 0 FAIL); verify cmd
(`go vet + go test ./config/... ./core/commands/... ./internal/agentic/...`) rc=0.
Extra beyond plan: on-error dispatch table tests
(`internal/agentic/agent_onerror_dispatch_test.go`), dead-row regression
(`core/commands/config_compression_deadrow_test.go`), filmstrip UI validation
(`internal/app/config_compression_filmstrip_test.go`, race-clean ×3 full-pkg
runs; includes engine fix `tui.RenderNow` now snapshots via ApplySync).
gocognit/gocyclo: no new violations — all >15 findings pre-exist on HEAD
(verified via stash: validateOrchestrator 19, mergeExecution 16/17,
renderLoop 16). Docs updated (CONFIGURATION.md, PROVIDER-CONNECTIVITY.md,
stale zero-LLM comments in agent_compression.go).

### 01 — Restore build
- `go build ./...` then `go vet ./...`; fix only compile errors from §5 edits
  (e.g. leftover references to removed helpers, signature changes).
- Acceptance: build + vet clean.

### 02 — internal/agentic threshold/strategy tests
Files: `internal/agentic/compression_thresholds_test.go`,
`internal/agentic/compression_default_summarize_test.go`.
- hard 0 → tierNone at ANY usage (was: hard@95). Tests previously relying on default-95
  hard tier with zero config: set `HardPercent: 95` explicitly.
- hard -1 → disabled (same as 0).
- soft strategy: explicit `selective`/`summarize`/`hybrid` now honored (no degradation to micro).
- `zeroLLMStrategy` references removed.
- Acceptance: `go test ./internal/agentic/ -run 'Threshold|Tier|Summarize'` green.

### 03 — internal/agentic cache-gate + overflow recovery tests
Files: `internal/agentic/agent_compression_cache_gate_test.go`,
`internal/agentic/compaction_cache_test.go`,
`internal/agentic/agent_overflow_recovery_test.go`.
- Any zero-config case expecting hard-tier/reactive fire at 95: set explicit 95.
- NEW: on-error dispatch table tests — for OnErrorStrategy summarize → Compact called, no
  elision; tool_elision → elision only; selective → selective only; micro → micro forced;
  hybrid → elision+selective then Compact only when still ≥ escalation.
- Acceptance: package tests green incl. new cases.

### 04 — config package tests
Files: `config/compression_test.go`, `config/config_validate_test.go` (or wherever level/
strategy validation tests live), merge tests.
- Defaults from embedded default.yaml: HardPercent 95, OnErrorStrategy "hybrid",
  Strategy "" (was "micro").
- Validation: level cases → 0 ok, -1 ok, 5 ok (new), 100 ok (new), 4/7/101 rejected,
  97 rejected (not %5); soft layer now accepts all strategies; unknown on_error_strategy rejected.
- Merge: on_error_strategy overlay + contextCompressionLayerEmpty.
- Acceptance: `go test ./config/` green.

### 05 — Menu rewrite (core/commands/config_compression.go) — THE UX PIECE
Replace `settingCompression` + old pickers with:

Main menu (title "Compression:"):
```
Soft ceiling %        → desc: "N%" or "0% (disabled)"
Soft ceiling method   → desc: strategy or "micro (default)"
Hard ceiling %        → desc: "N%" or "0% (disabled)"
Hard ceiling method   → desc: strategy or "summarize (default)"
On error              → desc: strategy or "off"
Advanced…             → desc: "trigger layer, cache gate, max tokens, micro, per-model"
```

- `settingCompressionCeiling(layer)`: picker items = "0" (label "0% (disabled)") + 5..100
  step 5; current = configured value as string; on select → `m.applySet("context_compression.thresholds.<layer>_percent", v)` → `m.back()` (returns to Compression menu, redrawn).
- `settingCompressionMethod(layer)`: items = all strategies (micro, tool_elision, selective,
  hybrid, summarize); current = configured; applySet `context_compression.strategies.<layer>` → back.
- `settingCompressionOnError`: items = "off" (desc: no compression on context error) + all
  strategies; current = "off" if !OnContextError else OnErrorStrategy or "hybrid";
  select off → applySet on_context_error=false; else → applySet on_context_error=true AND
  applySet on_error_strategy=<v>; then back. (Two applySet calls OK.)
- `settingCompressionAdvanced`: rows = trigger strategy (reuse `settingCompressionStrategy`),
  trigger threshold (`settingCompressionThreshold`), cache gate toggle (inline applySet
  cache_gate on/off), max tokens (`settingCompressionMaxTokens`), preserve recent turns,
  micro:* rows (reuse existing micro sub-screens), per-model (reuse existing), "Enabled" toggle
  row + "Compress on context error" is NOT here (it's the On error row in main menu).
  Every row must have opener or inline handler — ZERO dead rows.
- Delete: derived `_derived_*` rows and their label helpers, old `settingCompressionSoft`/
  `settingCompressionHard`, `percentStepItems` (10..95 step5) if unused → replace with new
  ceiling items builder shared with per-model picker (`percentItemsWithInherit` → inherit +
  new items). Remove unused helpers (percentLabel, derivedPercentLabel, compressionHardValue)
  — check usages first.
- `compressionLabel(cfg)` (root menu desc) rewrite:
  !EnabledValue → "off"; else parts: soft N% · trigger N% · hard N% <method> · on-error <method>;
  empty parts → "off". Keep short.
- Empty-Value rule: never selectable item with Value "".

### 06 — Menu tests (core/commands)
Files: `core/commands/config_menu_test.go` (compression section),
`core/commands/config_compression_test.go`.
- Update expected rows to new 6-row menu; old derived-row tests removed.
- NEW regression (the reported bug): drive `settingCompression`, select EACH item,
  assert a new SelectOption is shown (picker opened) or config applied — no silent close.
- On-error picker: select "off" → OnContextError false; select "summarize" → OnContextError
  true + OnErrorStrategy "summarize".
- Ceiling picker: select "60" → Thresholds.SoftPercent/HardPercent 60 (per layer).
- Advanced menu rows all actionable.
- Acceptance: `go test ./core/commands/ -run 'Compression'` green.

### 07 — core/agentmanager tests
Files: agentmanager tests touching buildCompressionConfig / compression (search
`buildCompressionConfig`, `HardPercent`, `resolveAgenticThresholds` in core/*_test.go).
- enabled:false → ALL thresholds 0 (hard no longer implicit 95).
- embedded default path → hard 95 summarize; OnErrorStrategy mapped ("hybrid").
- Acceptance: `go test ./core/...` green.

### 08 — Docs
- Search embedded docs for compression semantics (`goa://CONFIGURATION`, docs/*.md):
  update "hard 0 = default 95" phrasing → "0 = disabled; default config sets 95";
  mention on_error_strategy; menu rows changed.
- Acceptance: no stale "default 95"/"zero-LLM soft" claims in docs.

### 09 — Full gate
- `go vet ./...`
- `go test -count=1 -race -cover ./...`
- `gocognit -over 15` / `gocyclo -over 12` on changed files (config_compression.go TUI budget 18/12).
- Fix fallout. Then done.

## 7. Key file map

- Engine: `internal/agentic/compression_thresholds.go`, `agent_compression.go`, `agent.go`
- Config: `config/config.go`, `config/configs/default.yaml`, `config/config_validate.go`, `config/config_merge.go`
- Wiring: `core/agentmanager_lifecycle.go` (buildCompressionConfig), `core/agentmanager.go` (RefreshContextCompression)
- Menu/CLI: `core/commands/config_compression.go`, `core/commands/config_cli.go`, `core/commands/config.go` (open/back/applySet)
- TUI selector: `tui/tui.go` ShowSelector/ShowOverlay; `internal/app/commandcontext.go` SelectOptionFunc (empty Value ⇒ treated as cancel!)

## 9. Tooling note (agent workflow)

- Todo list: ONLY micro-tasks that need shared context within this goal.
- Goals: separate units of work that can run on a fresh context (handover via notes like this file).

---

## 8. Risks / notes

- Behavior change: configs with hard_percent unset (0) lose implicit 95 hard tier — must set 95.
  Accepted per user decisions (default.yaml now explicit).
- `microCompactForced` must NOT run under a.mu (self-manages lock) — honored in
  overflowRecoveryInMemory.
- escalate-to-Compact threshold = resolveThresholds().escalationPercent() (effectiveHard-5,
  effectiveHard falls back to 95 when hard unset) — unchanged.
- Two applySet in on-error picker → two RefreshContextCompression calls; harmless.
- Per-model overlay: 0 = inherit (merge semantics) — unchanged; -1 = disable — unchanged.
