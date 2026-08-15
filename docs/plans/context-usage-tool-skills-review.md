<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Context-Usage Review — goa vs pi / opencode / zero / deepseek-harness

Bilateral tool-context review: how **goa** (`/Users/muaddib/dev/goa`, Go module
`github.com/pijalu/goa`) handles tool descriptions, tool returns, and their
impact on model context, compared against four sibling agents:

| Project | Language | Location | Notes |
|---|---|---|---|
| **pi** | TypeScript | `/Users/muaddib/dev/pi` | Earendil Works coding agent (TUI) |
| **opencode** | TypeScript (Effect) | `/Users/muaddib/dev/opencode` | SST/opencode agent |
| **zero** | Go | `/Users/muaddib/dev/zero` | Go agent (closest to goa) |
| **dsh** | TypeScript (Cordis) | `/Users/muaddib/dev/deepseek-harness` | DeepSeek Harness (prior parity source) |

**Scope:** tool *schemas/descriptions* shipped with every request, tool *returns*
(tool-result payloads in history), and their joint effect on context usage. The
goal is concrete, evidence-backed optimization candidates for goa.

**Baseline measurement (goa, committed state 65f7b42):** the default tool set
serializes to **17,579 JSON bytes ≈ 4,394 tokens**, sent with *every* LLM
request (`tools/schema_size_test.go`, `TestTotalToolSchemaJSONCeiling`).

**Severity scale:** Critical = cost/correctness defect · High = material
per-request cost · Medium = good hardening/tunability · Low = polish.

**Companion doc:** `docs/plans/deepseek-harness-gap-report.md` already covers
goa↔dsh context-management gaps (CX1–CX8) — several are now closed. This review
broadens the comparison to all four siblings and adds tool-schema/skill-catalog
costs that the CX series never measured.

---

## 1. What each project does

### 1.1 goa (baseline)

**Tool schemas sent per request**
- Every registered tool's full schema ships with every request:
  `buildProviderContext` → `migrateSchemas(a.reg.Schemas())`
  (`internal/agentic/agent_streaming.go:2017-2019`), provider
  `Context.Tools` (all of them).
- `ToolRegistry.Schemas()` caches and sorts schemas for prompt-cache stability
  (`internal/agentic/tool_registry.go:61-75`).
- CI guard caps the default set: per-tool description ≤500 chars (goal tool
  3600), per-tool JSON ≤2500 bytes (goal 5000), total ≤22000 bytes
  (`tools/schema_size_test.go:25-50`).
- `toolSchemaCost()` estimates schema tokens once, cached, included in
  `fixedCostTokens()` so compression thresholds count them
  (`internal/agentic/agent_context_cost.go:21-48`).
- **Skills:** `run_skill` tool carries a `skill_name` **enum of every
  model-invocable skill** (`skills/runner.go:54-83`) AND the system prompt
  renders `<available_skills>` name+description+location
  (`skills/prompter.go`, `internal/app/prompt.go:238-249`). Same catalog twice.
- **MCP / plugin tools** register into the same registry and ship unconditionally
  (schema guard explicitly excludes them: `schema_size_test.go:34-35`).

**Tool returns**
- At acquisition: `truncateToolResult` head/tail elision, default cap 50KB or
  `maxTokens/4` (`internal/agentic/agent_tools.go:272-298`).
- `SpillPolicy` (CX2, dsh parity): oversized plain-text results saved to
  session-scoped `SpillStore`, replaced by bounded head/tail preview + omission
  notice whose byte cost is reserved out of the cap
  (`tools/spill_policy.go`, `tools/common/spill.go`).
- Pre-compaction pruner (CX1, dsh parity): model-free rewrite of over-budget
  historical results to head+marker+tail, 8192/4096/1024 Unicode code points
  (`internal/agentic/agent_compression.go:355-425`, `compaction.go`).
- Read dedup (E1.3): re-reading unchanged file → short dedup hint instead of
  content (`tools/readfile_dedup.go:13-64`).
- Loop guardrails: three-tier consecutive-duplicate detection + global
  `MaxToolCalls` turn budget (`internal/agentic/TOOL_BUDGET_SPEC.md`).

**Context measurement**
- Real provider usage floor (`lastGrossInputTokens` from
  `u.TotalInputTokens()`) projected forward with `estimateTokensFromHistory` +
  `fixedCostTokens` (`internal/agentic/agent_context_stats.go:102-150`).
- Three-tier compression thresholds (soft/trigger/hard %), five strategies
  (`internal/agentic/compression_thresholds.go`).
- CX8 projected context pressure meter (`internal/agentic/agent_context.go`).

### 1.2 pi

**Tool schemas**
- **Active-tool filtering:** only `activeToolNames` ship
  (`packages/agent/src/harness/agent-harness.ts:330-331,438`); default set is
  `["read", "bash", "edit", "write"]`
  (`packages/coding-agent/src/core/agent-session.ts:2603-2605`). System prompt
  rebuilt when the set changes (`setActiveToolsByName` → `_rebuildSystemPrompt`,
  `agent-session.ts:928-940`).
- Tool schema via TypeBox (`packages/agent/src/harness/tools/read.ts:17-22`).

**Skills**
- Catalog only in system prompt as `<available_skills>`
  (`formatSkillsForSystemPrompt`, `system-prompt.ts:3-25`); description capped
  at 1024 chars (`skills.ts:22`). Model reads SKILL.md with the read tool — no
  skill tool, no enum.

**Tool returns**
- Per-tool truncation at source, 2000 lines / 50KB
  (`packages/agent/src/harness/utils/truncate.ts:6-12`): read = head truncation;
  bash = tail truncation + temp-file locator (`tools/bash.ts:57,130-140`).
- Rich `TruncationResult` (total lines/bytes, truncatedBy) feeds the UI.

**Context**
- Real provider usage (`usage.totalTokens` from the API) + trailing estimate
  (`compaction.ts:215-263`).
- Compaction: `contextTokens > contextWindow - 16384` reserve; keep
  ~20,000 recent tokens (`compaction.ts:160-161,246-247,373-393`).
- Branch summarization for oversized turns (`branch-summarization.ts`).

### 1.3 opencode

**Tool schemas**
- **Permission-filtered materialization per request:**
  `tools.materialize(agent.info?.permissions)` drops wholly-disabled tools
  (`packages/core/src/session/runner/llm.ts:203-212`,
  `packages/core/src/tool/registry.ts:106-120`).
- **Last-step tool collapse:** on the agent's final step, tools are dropped and
  `toolChoice: "none"` (`runner/llm.ts:203-212`).
- **Provider-specific schema projection** (`ToolSchemaProjection`): openai /
  gemini / moonshot trims null schemas, flattens `anyOf`, strips unsupported
  keys (`packages/llm/src/protocols/utils/tool-schema.ts:5-86`).
- Effect `Schema` with input+output schemas; definition memoized per tool
  (`packages/core/src/tool/tool.ts:60-75`).

**Skills**
- Catalog in system context `<available_skills>` (name+description, sorted)
  (`packages/core/src/skill/guidance.ts:16-32`).
- Dedicated `skill` tool loads full content on demand → `<skill_content>`
  injected with base dir + file list (`packages/core/src/tool/skill.ts:30-59`).
- **SystemContext** (baseline + snapshot + update/removed rendering): only
  changed sources re-render; unchanged stay durable; removed get notice
  (`packages/core/src/system-context/index.ts:198-280`).

**Tool returns**
- `ToolOutputStore`: 2000 lines / 50KB, head/tail preview + marker + spill to
  `tool-output/` dir with 7-day retention; configurable per config-doc
  (`packages/core/src/tool-output-store.ts:13-15,74-104,112-127`).
- `serializeToolContent` + `truncate` (2000 chars) for compaction prompt input
  (`packages/core/src/session/compaction.ts:67-90`).

**Context**
- `Token.estimate = length/4` (`packages/core/src/util/token.ts:5`).
- Compaction trigger: `estimate(system+messages+tools) > context - max(output,
  buffer=20000)`; keep 8,000 recent tokens; structured 5-section summary
  template (`compaction.ts:14-15,137-159,230-246`).

### 1.4 zero

**Tool schemas**
- **Deferred tool loading (flagship):** when ≥ `DeferThreshold` deferral-eligible
  tools exist and `tool_search` is runnable, deferred tools' schemas are
  withheld; the model loads one on demand with `tool_search`, which returns the
  full name+description+schema
  (`internal/tools/tool_search.go:21-58,66-118,399-425`).
- **Cache-stable three-region layout:** eager block (alpha-sorted, stable) +
  `tool_search` (compact catalog) + loaded-tail (append-only), so a load never
  reorders the cached prefix
  (`internal/agent/loop.go:3067-3190`).
- Permission-mode visibility (`plan`/`spec-draft` restrict to read-only tools;
  `internal/tools/tool_search.go:245-279`), operator allow/deny filters.
- Typed `Schema` with `AdditionalProperties`; `Result` carries `Truncated` +
  `Meta` (`internal/tools/types.go:69-120`).

**Skills**
- Catalog in system prompt `<available_skills>` with **4096-byte budget**; skills
  past budget summarized as a count; per-line description truncated to 200
  runes (`internal/agent/system_prompt.go:217-270`).
- `skill` tool (no enum): unknown name returns the full list
  (`internal/tools/skill.go:15-81`).

**Tool returns**
- **Semantic category-based output budgeting (flagship):** per category
  (`file`/`search`/`test`/`process`/`diff`/`worker`) priority-ordered line
  selection — files keep header + alternating ends; tests keep failure lines +
  ±3 context + tail summaries; processes keep first 10 + diagnostics + last 16;
  duplicate lines collapsed with repeat marker; diff hunks kept indivisible
  (`internal/tools/output_policies.go:13-44,72-243`).
- Spill to disk with secret redaction + 7-day retention + per-uid dir +
  symlink-hardened read path (`internal/tools/spill.go:14-145`).

**Context**
- BPE-aware estimate: non-whitespace bytes / 4 (`internal/agent/compaction.go:97-135`).
- **Stale tool-output pruning (flagship):** zero-cost model-free reclaim before
  the LLM summarizer — replace older large tool bodies (≥200 tokens) with a
  placeholder, protecting a 40,000-token recent window, gated on ≥20,000
  reclaimable tokens, idempotent
  (`internal/agent/prune.go:9-60`).
- Compaction threshold + low-water-mark hysteresis; `estimateToolDefTokens`
  counts tool definitions in the threshold.

### 1.5 deepseek-harness

**Tool schemas**
- **Presentation mode: native / code / both.** `code` sends only `run_code`
  plus a generated SDK prompt section; `both` sends both; per-scope override via
  `presentAs`
  (`packages/core/tools/src/index.ts:651-672,815-833,946-985`,
  `wireSchemas` at 850-900).
- Per-scope restrictions (allow/deny) + `toolOrder` config with
  `<unlisted-tools>` rest marker
  (`packages/core/system-prompt/src/index.ts:139-178,487-500`).
- Ordered system-prompt registry: sections / contexts / tools / variables;
  `structuredClone` of parameters per assembly.

**Skills**
- On-demand skill loading (skill packages exist; catalog rendered into system
  context; full body returned as tool output on request).

**Tool returns**
- `spill-policy` plugin (goa CX2 parity source): `maxInlineBytes`, head/tail
  preview via `TextRetainer`, byte-precise notice reservation, `read` skipped,
  best-effort fallback, plus a durable dispatch-log arm
  (`packages/spill/spill-policy/src/index.ts:79-231`).
- `compaction-tool-result-pruner` (goa CX1 parity source): 8192/4096/1024 code
  points, rich-block order preserved, marker once
  (`packages/compaction/compaction-tool-result-pruner/src/index.ts:76-131`).

**Context**
- **Token meter (flagship):** fixed density `len/4` + structural
  block/role overhead; per-node priced surface with delta tracking;
  real provider usage (input + cacheRead + cacheWrite) for pressure; durable
  shadow-price protocol for prunes/compactions
  (`packages/llm/token-meter/src/estimate.ts:16-87`,
  `packages/llm/token-meter/src/usage-projection.ts:84-150`,
  `packages/llm/token-meter/src/surface-fold.ts:41-65`).
- Compaction: `thresholdRatio × contextWindow`, retention ratio.

---

## 2. Gap matrix — what goa is missing (or has, but weaker)

| # | Gap | goa (evidence) | Best sibling (evidence) | Severity |
|---|-----|----------------|--------------------------|----------|
| TC1 | **Every registered tool schema ships with every request.** ~17.6KB / ~4.4K tokens fixed cost regardless of the turn's actual tool needs. MCP/plugin/skill tools add unconditionally. | `agent_streaming.go:2017-2019` (all schemas), `schema_size_test.go` (17,579 B) | zero: deferred tool loading with cache-stable layout (`loop.go:3067-3190`); pi: active-tool filter default 4 (`agent-session.ts:2603-2605`) | **High** |
| TC2 | **`run_skill` enum duplicates the `<available_skills>` catalog.** Skill names ship twice (schema enum + system prompt); the enum grows with every model-invocable skill and makes the tool schema session-dependent (prompt-cache instability when skills change). | `skills/runner.go:54-83` (enum), `skills/prompter.go` (catalog) | zero: skill tool has no enum, catalog budgeted (`skill.go:15-81`, `system_prompt.go:217-270`); pi: no skill tool (`system-prompt.ts:3-25`) | **High** |
| TC3 | **Tool-result truncation is head/tail only.** `truncateToolResult` and the spill preview keep both ends; the semantically important middle lines (test failures, diagnostics, per-file search matches) are discarded. | `agent_tools.go:272-285`, `tools/spill_policy.go` | zero: category-aware semantic budgeting (`output_policies.go:13-243`) | **Medium** |
| TC4 | **No zero-cost stale tool-output reclaim.** Oversized historical tool bodies sit in the window until a compaction LLM call. goa's pruner is threshold-based and fires only at compaction pressure. | `agent_compression.go:355-425` (threshold pruner only) | zero: `pruneStaleToolOutput` protects recent 40K tokens, replaces older large bodies with placeholder (`prune.go:9-60`) | **Medium** |
| TC5 | **Tool schema JSON is not projected per provider.** One canonical schema ships to every endpoint; providers with stricter/minimal schema support (gemini/moonshot) receive unsupported keys. | `agent_streaming.go:2019` (single `migrateSchemas`) | opencode: `ToolSchemaProjection` trims per provider (`tool-schema.ts:5-86`) | **Low–Med** |
| TC6 | **No dynamic tool set for the turn.** Tools cannot be hidden mid-session except by config toggles; there is no "drop tools on final step" and no permission-driven materialization. | `tool_registry.go` (all registered ship) | opencode: per-request materialize + last-step collapse (`runner/llm.ts:203-212`) | **Low–Med** |
| TC7 | **Skills catalog unbudgeted.** `<available_skills>` renders every model-invocable skill without a byte budget; pathological skill sets bloat every turn. | `skills/prompter.go:44-67` (no budget) | zero: 4096-byte budget + count summarization (`system_prompt.go:217-270`) | **Medium** |

### Already closed / non-gaps (not repeated as work)

- Spill policy (CX2), pre-compaction pruner (CX1), read dedup (E1.3), loop
  guardrails, real-usage floor + projected pressure (CX8), layered compression
  thresholds, cache-stable schema ordering. goa matches or beats the siblings on
  these.

---

## 3. Action plan (modular — pick/tune per item)

Priorities are ordered by expected context win per unit of effort. Each item is
independently shippable; TC1 is the flagship.

### P1 — Deferred tool loading (TC1) — HIGH · effort L

**Goal:** stop shipping every tool schema with every request. When the
deferral-eligible tool count is high, withhold non-core schemas and let the
model pull them on demand through a lightweight loader.

**Design (zero parity, adapted to goa):**

1. Add a `Deferred()`-style marker to a new optional tool interface
   (`tools/common/` or `internal/agentic/tool.go`): `interface { Deferred() bool }`.
2. Add a `tool_search` tool in `tools/` whose schema is tiny (one `query`
   string) and whose description embeds the compact catalog of deferred tools
   (name + one-line description, budgeted). Resolves `select:Name1,Name2` or
   keyword queries; returns full name+description+schema JSON for matches;
   unknown-name fallback lists available deferred tools.
3. In `ToolRegistry.Schemas()`/`agent_streaming.go`, partition tools into:
   - eager block (always: core tools — read, write, edit, bash, search, python,
     ask, goal, run_skill, …) alpha-sorted, byte-stable;
   - `tool_search` (always present, compact catalog byte-stable);
   - loaded-tail (append-only, deferred tools the model has pulled this run).
   Deferral activates only when the eligible count ≥ threshold (e.g. 8) and the
   loader is runnable in the current permission mode.
4. Agent loop: after a `tool_search` call, read `Meta["load_tools"]`
   (comma-separated names) and expose those tools on the next turn. The
   eager+catalog block must NOT reorder on load — only the tail grows — to keep
   the provider prefix cache warm.
5. Mark opt-in-heavy tools deferred by default: `webfetch`, `smartsearch`,
   `ssh_bash`, `bg_exec`, `terminals`, `memento`, `request_review`,
   `session_query`, MCP group tools (when group > N), plus user skills loaded
   via `run_skill`-adjacent surface. Core tools stay eager.

**Acceptance:**
- With deferral active, per-request tool-schema bytes drop from ~17.6KB to
  (core ≈ 8–10KB + catalog ≈ 0.5KB) — measurable via
  `TestTotalToolSchemaJSONCeiling` extended to the partitioned view.
- `tool_search` loads a deferred tool; the next request contains its schema and
  nothing else reordered.
- Deferred tools cannot be called before being loaded (clear error + redirect).
- Prompt-cache stability: two requests before/after a load differ only in the
  appended tail.
- Model-free: no LLM call in the loading path.
- Tests: `tools/tool_search_test.go`, agent-loop partition tests, schema-budget
  test on the partitioned view.

### P2 — `run_skill` enum → catalog-only (TC2) — HIGH · effort S

**Goal:** remove the duplicated skill-name enum from the `run_skill` schema.

**Design:**
1. `skills/runner.go:54-83`: drop the `enum` from `skill_name`; keep the
   description ("Name of the skill to execute — see the available_skills
   catalog in the system prompt").
2. Keep the `<available_skills>` catalog as the single discovery surface.
3. Optionally cap the catalog with zero's 4096-byte budget + count-summary
   (fold into P5/TC7).

**Acceptance:**
- `run_skill` schema no longer contains skill names; schema is byte-identical
  across sessions with different skill sets (prompt-cache stable).
- `run_skill` still validates the name at execution (unknown name → error
  listing available skills, as `skills/runner.go` already does).
- Saves ~N×40 bytes per request for N skills + JSON framing.

### P3 — Semantic output budgeting (TC3) — MEDIUM · effort M

**Goal:** replace uniform head/tail with category-aware retention for the
highest-value outputs.

**Design (zero parity, `output_policies.go`):**
1. Classify tool results at the `truncateToolResult`/`SpillPolicy` boundary by
   tool name + content heuristic:
   - `read` → file window (keep header + alternating start/end lines);
   - `bash`/`bg_exec` → process (first 10 + diagnostics + last 16, collapse
     repeats);
   - `verify`/test → test (failure lines + ±3 context + tail summaries);
   - `search`/`smartsearch`/`lsp` → search (per-file representative + summary
     lines);
   - `webfetch` → default head/tail.
2. Budget in bytes/tokens with the existing `truncateToolResult` cap; keep the
   omission marker (`[goa-system] … N sections omitted …`).
3. Keep semantics stable: the beginning and end still survive for read/bash
   fallback; only the middle is selected differently.

**Acceptance:**
- For a test-run result, retained lines include failure lines even when they sit
  in the middle beyond plain head/tail cut.
- For a search result, one line per matched file is retained across the file
  set before deep lines of one file.
- Byte budget respected (result ≤ cap + marker).
- Table-driven tests per category mirroring zero's `output_policies_test.go`.

### P4 — Stale tool-output pruning (TC4) — MEDIUM · effort M

**Goal:** reclaim context at zero token/latency cost before the LLM summarizer.

**Design (zero parity, `prune.go`):**
1. Add a model-free pass that walks history newest-first, protects the last N
   messages + a trailing recent-token window (e.g. 40,000), and replaces older
   large tool-result bodies (≥200 tokens) with a placeholder:
   `[goa-system] pruned <tool> output (~N tokens) — re-run the tool if needed`.
2. Gate: only run when reclaimable older output ≥ threshold (e.g. 20,000
   tokens), so short sessions are untouched.
3. Wire into the pre-compaction path (`pruneToolResultsPreCompact`) as a first
   pass; skip the LLM summarize when pruning alone resolves pressure (goa's
   existing early-return pattern at `agent_compression.go:92-95` already allows
   this).
4. Idempotent: never prune an already-pruned body; never drop messages; never
   touch non-tool messages (provider replay stays valid via ToolCallID).

**Acceptance:**
- Long dump-heavy session: stale read/bash bodies replaced with placeholders,
   recent 40K tokens byte-identical.
- No LLM call when pruning resolves the pressure (compaction event
   `tool_result_pruning` with skip).
- `go test -race` on new prune tests; idempotency asserted by running twice.

### P5 — Skills catalog budget (TC7) — MEDIUM · effort S

**Goal:** bound the `<available_skills>` rendering.

**Design (zero parity, `system_prompt.go:217-270`):**
1. `skills/prompter.go`: add a byte budget (4096) with count-summary for skills
   past it ("…and N more (call run_skill with a name; unknown names list all)").
2. Truncate per-line descriptions to 200 runes.
3. Always list at least one skill; never drop silently.

**Acceptance:**
- Pathological skill sets stay within budget; count-summary line present.
- Existing skill tests still pass; new budget test added.

### P6 — Provider schema projection (TC5) — LOW–MED · effort S

**Goal:** trim schema JSON per provider compatibility.

**Design (opencode parity, `tool-schema.ts`):**
1. In `migrateSchemas` (`agent_migrate.go:166-176`), apply a small
   normalization pass keyed on the provider family: drop `additionalProperties`
   where unsupported, flatten single-variant `anyOf`, strip `null` union arms.
2. Guard with the existing compat-detection (`compat_detect.go`).

**Acceptance:**
- Gemini/Moonshot requests omit unsupported keys; OpenAI requests unchanged
  (byte-identical to today so the schema budget test stays green).

### P7 — Per-turn tool collapse (TC6) — LOW–MED · effort M

**Goal:** drop tools on the agent's final step / when a turn must be text-only.

**Design (opencode parity, `runner/llm.ts:203-212`):**
1. When the agent loop decides a turn is the last step (or a stop-turn signal
   is pending), send `Tools: nil` + `ToolChoice: none`.
2. Implement as a small control flag on `provider.Context` rather than a new
   provider feature.

**Acceptance:**
- Final-step requests carry no tools; subsequent turns restore the full set.
- Existing `toolChoice` handling tests pass.

---

## 4. What goa should NOT copy

- **opencode's Effect `Schema` runtime** — goa's `map[string]interface{}`
  schemas + gojsonschema validation are adequate; adopting Effect is a
  rewrite with no context win.
- **dsh's full token-meter surface-fold + shadow-price protocol** — goa's
  real-usage floor + projected pressure (CX8) already achieves the accuracy goal
  with far less machinery.
- **pi's `activeToolNames` default of 4 tools** — too aggressive for goa's
  current architecture (which exposes goal/ask/todo unconditionally); P1's
  deferral achieves most of the win without dropping capability.
- **zero's semantic budgets for every category** — implement P3 for the
  highest-value categories only (test/process/search); diff/worker budgets are
  niche.

---

## 5. Suggested sequencing

1. **P2** (S, independent, immediate win on skills duplication).
2. **P5** (S, pairs with P2's catalog work).
3. **P1** (L, flagship; do after the quick wins so its partition includes the
   already-trimmed skill surface).
4. **P3 + P4** (M each; complementary — P3 improves retention quality, P4
   reclaims stale bodies; both land in the truncation/compaction seam).
5. **P6 + P7** (S/M; polish once the above are stable).

Each item keeps the gate green: `go build ./... && go vet ./...`,
`go test -count=1 ./...`, `-race` on touched packages, gocognit ≤15 /
gocyclo ≤12, staticcheck on new files.
