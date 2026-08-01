<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Recontext — Start-Prompt Size Reduction Plan

Branch: `feature/recontext`
Goal: reduce the fixed per-request context overhead (system prompt + tool schemas + skill listings) to the minimum that preserves identical behavioral meaning.

## 1. Measured baseline (this repo, coding-posture mode, no MCP/memory)

### 1a. System prompt sections (assembled by `internal/app/prompt.go:buildSystemPrompt`)

| # | Section | Source | Chars |
|---|---------|--------|-------|
| 1 | Mode body | `prompts/mode/coding-posture/definition.md` (SPDX + frontmatter stripped by `internal/embeddoc`) | 2,592 |
| 1a | `<tool_usage>` | hardcoded in `prompt.go` | 60 |
| 2 | `<project_context>` | `AGENTS.md` (project root) | 2,552 |
| 3 | `<active_skills>` | mode default skills (empty in coding-posture) | 0 |
| 4 | `<memory>` | memory store (empty here) | 0 |
| 5 | `<available_skills>` | `prompts/available_skills.md` + 13 skill `description:` frontmatter lines | ~3,266 |
| 6 | `<goa_documentation>` | generated from `docs.List()` | ~356 |
| 7 | `<mcp_instructions>` | none configured | 0 |
|   | **Total system prompt** | | **~8,800 chars ≈ 2,200 tokens** |

### 1b. Tool schemas (sent with EVERY request; measured by marshalling `Schema()`)

| Tool | JSON bytes | Description chars |
|------|-----------:|------------------:|
| **goal** | **9,881** | **6,657** ← 43% of payload |
| edit | 1,236 | 29 |
| search | 1,140 | 91 |
| lsp | 980 | 398 |
| pty_exec | 811 | 35 |
| ask_user_question | 779 | 31 |
| python | 706 | 458 |
| bg_exec | 663 | 28 |
| webfetch | 652 | 24 |
| bash | 641 | 154 (incl. load-bearing cwd note) |
| read / agent_swarm / task_outcome / todo_list / smartsearch / memento / verify / plan / ssh_bash / terminal / write / plan_mode / read_media_file | ~4,800 total | small |
| Session tools (run_skill, agent, goa, send_message, receive_message) | ~1,850 | small |
| **Total tool schemas** | **~24,600 bytes ≈ 6,150 tokens** | |

### 1c. Grand total fixed overhead

**~33,400 chars ≈ 8,400 tokens per request**, of which:

- `goal` tool description: 6,657 chars (20% of everything)
- skill descriptions in `<available_skills>`: ~2,900 chars (4 long ones = 1,245)
- mode body: 2,592 chars
- project AGENTS.md: 2,552 chars
- tool parameter descriptions (inside schemas): ~3,500 chars spread over edit/search/lsp/pty_exec/ask_user_question/python

### 1d. Findings that shape the plan

1. `prompts/goal/goal.md` (6,657 chars) is the single largest item — verbose prose documentation embedded as the `goal` tool's `Schema().Description` (`tools/goal/descriptions.go` → `tools/goal/goal.go:106`).
2. Four skill descriptions dominate `<available_skills>`: tui-test (412), go-debug (312), qa-e2e (283), golang-check (238) chars.
3. `python` (458) and `lsp` (398) tool descriptions are 3–5× longer than peers.
4. SPDX headers are already stripped from mode bodies (`embeddoc.stripLeadingComment`) — no win there.
5. Tool `.short.md`/`.long.md` files are NOT in the start prompt; they serve the on-demand DocEngine (`/docs`). Out of scope unless requested.
6. System-prompt budget logic (`systemPromptBudget`) caps only the system prompt; tool schemas are unbudgeted and unaffected by it.
7. Test coupling is low: no test asserts exact wording of `goal.md`; `core/commands/docs_test.go` references short-doc text only.
8. All target files are git-tracked, including `.agents/skills/*` and `.goa/skills/qa-e2e`.
9. Note: the currently running binary may emit a different first block than the source tree (stale binary suspicion). Baseline re-measurement must use a freshly built binary.

## 2. Optimization targets (ranked by savings)

| ID | Target | File(s) | Current → Target | Save (chars) |
|----|--------|---------|------------------|--------------|
| T1 | goal tool description | `prompts/goal/goal.md` | 6,657 → ~2,000 | ~4,600 |
| T2 | 4 long skill descriptions | `.agents/skills/{tui-test,go-debug,golang-check}/SKILL.md`, `.goa/skills/qa-e2e/SKILL.md` | 1,245 → ~560 | ~680 |
| T3 | python tool description | `tools/python.go` Schema() | 458 → ~180 | ~280 |
| T4 | lsp tool description | `tools/lsp.go` Schema() | 398 → ~150 | ~250 |
| T5 | verbose param descriptions | `tools/editfile.go`, `tools/search.go`, `tools/ask/ask_user.go`, `tools/pty_exec.go`, `tools/bgexec.go`, `tools/webfetch.go`, `tools/plan/task_outcome.go` | — | ~400–600 |
| T6 | mode bodies | `prompts/mode/{coding-posture,coder,planner,reviewer}/definition.md` | 4,908 → ~4,200 | ~500–700 |
| T7 | project AGENTS.md | `AGENTS.md` | 2,552 → ~2,100 | ~400 |
| T8 | goa_documentation line | `internal/app/prompt.go` | 356 → ~300 | ~50 |
| | **Total estimated savings** | | | **~7,000–7,700 chars ≈ 1,750–1,900 tokens/req (~21–23%)** |

Compression rules applied everywhere:
- Keep every behavioral rule, prohibition, trigger condition, and enum contract; cut only redundancy, hedging, and explanatory prose.
- Keep load-bearing keywords (tool names, action names, file paths, mode names) for skill/tool matching.
- No information moves to a place the model can't reach; moved detail goes into the on-demand doc (skill body / LongDoc) when it isn't already there.

## 2b. Scope additions (approved during execution)

- **S1 — SPDX must never reach the model.** Leak inventory: (a) `internal/context.go:findContextFile` injects AGENTS.md raw into `<project_context>` — live start-prompt leak; (b) `docs/docs.go:Get` serves raw embedded docs to read/webfetch `goa://`; (c) `tools/common/docloader.go:ReadDoc` serves raw .short.md/.long.md via DocEngine (reachable by model through the `goa` command tool). Already clean: mode definitions (embeddoc strips), goal.md (fixed in Phase 1 via `prompts/goal/descriptions.go`), skills (SPDX is inside YAML frontmatter `#` comments, never sent). Fix: strip leading HTML comment blocks at each single-point loader + guard test asserting "SPDX-License-Identifier" absent from all model-facing producers.
- **S2 — Config enable/disable for embedded skills.** `skills.disabled: [names]` in config; disabled embedded skills are not registered (thus absent from `<available_skills>`, `/skills` banner, and the `run_skill` enum). Load-time only — no mid-session re-filtering once the system prompt is sent. Internal consumers (e.g. dream) keep working: if a skill the app needs is disabled, that feature degrades gracefully (documented).
- **S3 — Startup prompt-size display.** On new session / first start / reload, the startup banner shows the assembled prompt context size: system prompt chars (+ est. tokens) and tool-schema chars (+ est. tokens). Implemented in `startAgentSession` (the single session-start path); informational only, never blocking.

## 3. Task breakdown

### Phase 0 — Baseline & branch (no code changes)
- [x] 0.1 Create branch `feature/recontext`.
- [x] 0.2 Baseline recorded via throwaway measurement (later formalized as the Phase 6 guard tests).
- [x] 0.3 SKIPPED with rationale: unit-level measurement reproduces buildSystemPrompt deterministically; the stale-binary question does not affect source-tree changes.

### Phase 1 — T1: compress `prompts/goal/goal.md` (biggest win, highest care)
- [x] 1.1 Rewrite goal.md dense/telegraphic: 6,822 → 3,421 chars effective (49.9%); all 23 behavioral rules preserved.
- [x] 1.2 Strip SPDX header at load (`prompts/goal/descriptions.go` via embeddoc).
- [x] 1.3 Tests green: prompts, tools/goal, core, internal/agentic.

### Phase 1b — S1: SPDX-never-seen-by-model
- [x] 1b.1 `internal/context.go:findContextFile` — strip leading HTML comment block(s) from context file Content (fixes live system-prompt leak).
- [x] 1b.2 `docs/docs.go:Get` — strip leading comment before serving.
- [x] 1b.3 `tools/common/docloader.go:ReadDoc` — strip leading comment.
- [x] 1b.4 Guard tests: context strip fixture; docs.Get no-SPDX; ReadDoc no-SPDX; broad "no SPDX in any mode body / tool description / goal description / assembled system prompt" test.

### Phase 1c — S2: config enable/disable for embedded skills
- [x] 1c.1 Add `skills.disabled: []string` to config (default empty; document in docs/SKILLS.md if exists).
- [x] 1c.2 Filter at skill-registry load: disabled embedded skills not registered → absent from prompt listing, banner, run_skill enum.
- [x] 1c.3 Tests: disabled skill not listed/registered; default keeps all.

### Phase 1d — S3: startup prompt-size display
- [x] 1d.1 In `startAgentSession`, compute system prompt size + total tool-schema JSON size (chars + est. tokens at 4 chars/tok) and add a `⟡` startup banner line.
- [x] 1d.2 Verify it fires on first start, new session, and reload (all go through `startAgentSession`).
- [x] 1d.3 Test: banner line format via existing prompt_test fixtures.

### Phase 2 — T3/T4/T5: tool Schema descriptions
- [x] 2.1 `tools/python.go`: compress description; keep: gpython, Py3.4 subset, jail-confined, prefer-over-bash-python3 guidance, stdlib boundary.
- [x] 2.2 `tools/lsp.go`: compress; keep: 4 ops, multi-language, prefer-over-grep-for-symbols.
- [x] 2.3 Trim verbose param descriptions in editfile/search/ask_user/pty_exec/bgexec/webfetch/task_outcome (keep enum semantics and behavioral notes like edit's `operation` meanings and bash's cwd note untouched).
- [x] 2.4 `go test ./tools/...` + `core/commands/docs_test.go` (short-doc coupling check).

### Phase 3 — T2: skill descriptions
- [x] 3.1 Shorten `description:` frontmatter in tui-test, go-debug, golang-check, qa-e2e SKILL.md files to ≤ ~150 chars, keeping trigger keywords ("TUI", "dlv", "gocognit/gocyclo/staticcheck", "QA e2e"); move "Use when…" detail into the skill body where not already present.
- [x] 3.2 Quick pass over built-in `skills/*/SKILL.md` descriptions (already ≤ 90 chars; only trim outliers).
- [x] 3.3 `go test ./skills/...` and verify `/skills` banner rendering unchanged.

### Phase 4 — T6: mode bodies
- [x] 4.1 Compress `coding-posture/definition.md` (keep all 4 Always rules, core loop, all 11 modes with their discriminating rule each — one line each).
- [x] 4.2 Same pass on `coder`, `planner`, `reviewer` definitions.
- [x] 4.3 `go test ./prompts/... ./core/...` (mode registry tests).

### Phase 5 — T7/T8: project context + self-doc
- [x] 5.1 Trim `AGENTS.md` redundancies (merge duplicated testing/complexity lines; keep all 6 Hard Rules, module/architecture facts, coverage targets, conventions).
- [x] 5.2 Micro-trim `buildSelfDocSection` wording.
- [x] 5.3 Re-run prompt tests.

### Phase 6 — Guard test (Hard Rule 3: every fix needs a test that would have caught it)
- [x] 6.1 **Build-time check only** (`go test`): hard ceilings over *embedded* assets — goal description ≤ 3,600 chars; each tool description ≤ 500 chars (goal excepted); each *built-in* skill description ≤ 200 chars (embedded FS only, never user/project dirs); each mode body ≤ 3,000 chars; total default-tool schema JSON ≤ 22 KB. Fails CI on future bloat.
- [x] 6.2 **No runtime enforcement**: goa must never stop working because context grew — users can add skills that bloat context by design; the existing runtime `systemPromptBudget` already degrades gracefully (drops low-priority sections, never errors). Do not add runtime rejection.
- [x] 6.3 Test style follows repo patterns (table-driven, <100ms).

### Phase 7 — Verification gate (repo standard)
- [x] 7.1 `go vet ./...`
- [x] 7.2 `go test -count=1 -race -cover ./...`
- [x] 7.3 `gocognit -over 15` + `gocyclo -over 12` (no Go logic changes expected, but run anyway)
- [x] 7.4 Build fresh binary; dump `/prompt`; produce before/after table (system prompt, tool schemas, total tokens).
- [x] 7.5 Optional: `qa-e2e` skill smoke run against local LM to confirm no behavioral regression in tool usage.

### Phase 8 — Commit(s)
- [x] 8.1 Conventional commits, one per phase group: `refactor(prompts): compress goal tool description`, `refactor(tools): trim schema descriptions`, `refactor(skills): shorten skill descriptions`, `refactor(prompts): compress mode + project context`, `test(app): add prompt-size guard ceilings`.
- [x] 8.2 Update this plan's checkboxes; summary PR-style comment with before/after numbers.

## 4. Explicit non-goals (unless you say otherwise)

- Tool `.short.md` / `.long.md` docs (on-demand DocEngine only, not in start prompt).
- Sub-agent / pipeline / companion prompts (`prompts/task/*`, `prompts/pipeline/*`, `prompts/companion*.md`) — not in the main start prompt.
- Changing the `systemPromptBudget` tiers or adding tool-schema budgeting (behavior/design change, separate discussion).
- MCP instructions, memory section (runtime-data dependent, nothing to compress statically).
- `internal/agentic/AGENTS.md` (24.9 KB) — only loaded when working inside that directory tree, not part of root session start prompt.

## 5. Risks & mitigations

| Risk | Mitigation |
|------|-----------|
| LLM behavior drift from terser descriptions | Keep all rules/keywords; line-by-line diff review mapping each deletion to redundancy; full test suite; optional e2e smoke. |
| goal.md is effectively a spec | Phase 1 gets its own commit + goal-package test run; any doubt = keep the sentence. |
| Hidden test coupling to exact strings | Grep for assertions before each edit; docs_test.go already identified. |
| Size regressions later | Phase 6 guard test with hard ceilings. |

## 6. Success criteria — FINAL RESULTS (feature/recontext)

| Metric | Before | After | Delta |
|--------|-------:|------:|------:|
| Tool schemas (default set incl. goal) | 22,770 B | 18,897 B | **−17.0%** |
| System prompt (this project) | 8,826 chars | 7,809 chars | **−11.5%** |
| **Total fixed overhead/request** | **31,596 (~7,900 tok)** | **26,706 (~6,675 tok)** | **−15.5% (−1,225 tok)** |
| goal tool description | 6,822 | 3,421 | −49.9% |
| Longest skill description | 416 | 150 | −64% |
| SPDX/MD comments in model-facing text | 5 leak paths | 0 (fence-aware strip + guards) | −100% |

The ≥20% estimate was optimistic: mode bodies and AGENTS.md proved already
lean (~300 chars combined). Dominant wins delivered: goal.md −50%, all
comment leaks eliminated, tool schemas −17%, skills −700 chars/request.

Delivered beyond the original scope: S1 (comment stripping everywhere +
fence-aware embeddoc.StripHTMLComments), S2 (`skills.disabled` config),
S3 (startup `⟡ Prompt context:` info bubble), build-time size-ceiling guards
(no runtime enforcement), themeHex default-fallback fix (pre-existing main
failure), bugs.md triage entries for the follow-up bugs goal.

- All gates green: `go vet ./...`, full `go test -count=1 -race -cover ./...`,
  gocognit/gocyclo within budget on new code (commentStripper split).
