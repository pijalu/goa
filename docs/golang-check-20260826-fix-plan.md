# golang-check Fix Plan — 2026-08-26

Source: full run of `.agents/skills/golang-check/golang-check.sh` (gocognit >15, gocyclo >12, staticcheck, file-size hard 1000 / soft 500).
Raw output: `/tmp/golang-check-raw.txt` (transient).

## Summary

| Check | Result | Findings |
|---|---|---|
| 🔴 Build blockers (vet/compile) | PASS | 0 |
| 🟠 Correctness (SA4xxx, SA1xxx) | PASS | 0 |
| 🟡 Dead code (U1000) | FAIL | 1 |
| 🔵 Complexity gocognit >15 | FAIL | 20 (8 production, 12 tests) |
| 🔵 Complexity gocyclo >12 | FAIL | 39 (~7 production-only, rest tests/shared) |
| ⚪ File size | FAIL | 9 hard (>1000): 4 prod + 5 test · 168 soft (>500): ~91 prod, ~77 test |

The codebase is correctness-clean; all remaining work is dead code, complexity, and file organization. No `//nolint`, no threshold changes.

---

## Tier 🟡 P0 — Dead code (fix first, trivial)

| File | Check | Explanation | Fix |
|---|---|---|---|
| `tools/tool_search_test.go:68` | staticcheck U1000 | `func schemaNames` is unused (U1000 = "unused code"). Likely orphaned after a test refactor. | Delete the function. If its assertion logic is still valuable, wire it into the test that should use it instead of deleting blindly — check git history first. Re-run staticcheck. |

---

## Tier 🔵 P1 — Production complexity (root-cause refactors)

Each fix extracts cohesive helpers (SRP), keeps behavior identical, and adds unit tests for newly extracted pure functions. Gate per item: relevant package tests + re-run of the specific check.

### P1.1 `e2e/perfdrive/main.go:50` `main()` — cognit 33 / cyclo 22

| | |
|---|---|
| Why over | One monolithic main: 13 flag defs + arg validation + PTY spawn + drain goroutine + prompt injection + sampling loop with wait-file/grace state machine + CSV write, all inline. |
| Fix | Decompose into linear orchestration: `type driveConfig` (parsed flags), `parseFlags() (*driveConfig, error)` (flag defs + required check → exit 2 moves here), `startProcess(cfg) (*drivenProc, error)` (exec+pty+drain goroutine), `(d *drivenProc) sendPrompt(text) error`, `collectSamples(d, cfg, notify func(sample)) []sample` (sampling loop; extract `markerDeadline(markerSeenAt time.Time, seen bool, grace, cfg) (time.Time, bool)` for wait-file state), `writeCSV(path string, samples []sample) error`. `main` becomes: parse → start → prompt → sample → write, cyclo ≤6 each. |
| Tests | `writeCSV` and `markerDeadline` become unit-testable (`t.TempDir`). |

### P1.2 `tools/lsp.go:196` `(*LSPTool).runRefactoring` — cognit 30 / cyclo 21

| | |
|---|---|
| Why over | Switch over 5 ops with validation + call + formatting inlined per case; `rename` case alone nests 4 branches (nil-check, newName check, preview policy, apply). Stale comment says "runSymbols handles documentSymbol". |
| Fix | Table dispatch: `var refactorOps = map[string]func(context.Context, lspRefactoringManager, *LSPTool, lspParams, string) (string, error)` with one small method per op: `refactorPrepareRename`, `refactorRename` (split into `previewRenameEdit` + `applyRenameEdit`), `refactorCompletion`, `refactorCodeAction`, `refactorFormatting`. Handler lookup + "unavailable"/"unknown op" guard only → cyclo ~4. Fix the stale comment while there. Mirror the same pattern already used by `formatLocations`/`formatHover`. |
| Tests | Existing `tools/lsp_test.go` op coverage must stay green; add unknown-op case if missing. |

### P1.3 `tools/lsp.go:117` `ExecuteContext` (cyclo 14) and `:268` `runAdvanced` (cyclo 14)

| | |
|---|---|
| Why over | Same switch-with-inline-work shape as P1.2 (ExecuteContext routes query/symbols/refactoring/advanced with param validation inline; runAdvanced has 5 cases). |
| Fix | ExecuteContext: keep a single routing table `map[string]handlerFunc`; move shared line/character validation to `validatePosition(p) error` called once before dispatch. runAdvanced: per-op methods `advancedImplementation`, `advancedWorkspaceSymbols`, … sharing a generic `callAndFormat` where profitable; switch becomes pure dispatch. |
| Tests | Existing LSP tool tests; add position-validation table test. |

### P1.4 `tools/search/bm25/fielded.go:68` `(*FieldedScorer).Score` — cognit 20 / cyclo 14

| | |
|---|---|
| Why over | Nested loops (terms × 5 fields) with tf/idf math, dedupe map, identifier boost, coverage/confidence post-processing all in one function. |
| Fix | Extract pure helpers: `dedupeTokens([]string) []string` (reusable — likely duplicates logic in `expandQuery`), `(s *FieldedScorer) termContribution(fd docFields, q string) float64` (inner field loop + BM25 term score), `identifierBoost(query string, d DocumentMeta, score float64) float64`, `confidenceFor(score, coverage float64) float64`. Score becomes guard clauses + one loop calling helpers → cognit ≤8. |
| Tests | Unit tests for `termContribution` (zero-tf short-circuit, avg<1 clamp) and `confidenceFor` boundaries (score=0 → coverage; saturation cap 1). |

### P1.5 `tools/search/bm25/codeaware.go:155` `ChunkSource` — cognit 18 / cyclo 14

| | |
|---|---|
| Why over | Param normalization + two chunking strategies (region-based with clamping loop; sliding-window fallback) inlined in one function. |
| Fix | Extract `normalizeChunkParams(window, overlap int, analyzer CodeAnalyzer) (int, int, CodeAnalyzer)`, `regionChunks(path string, lines []string, regions []Region) []DocumentMeta` (validation/clamp loop), `windowChunks(path string, lines []string, window, overlap int) []DocumentMeta` (sliding-window loop incl. final-chunk break). ChunkSource = normalize → regions → early return → windows. Each helper cyclo ≤6. |
| Tests | Table test on `windowChunks`: window>len(lines), exact multiple, overlap≥window fallback. |

### P1.6 `internal/lsp/manager.go:259` `(*Manager).spawn` — cognit 17

| | |
|---|---|
| Why over | Resolve + create + six protocol handler registrations (closures mutating `running`) + initialize handshake + initialized notification + two error-cleanup paths in one body. |
| Fix | Extract `(m *Manager) registerClientHandlers(client *Client, running *serverClient)` (all OnNotification/OnRequest wiring — the workspace/diagnostic/refresh comment moves with it) and `(m *Manager) handshake(ctx context.Context, client *Client, spec *ServerSpec, root string) (*serverClient, error)` (Initialize + Initialized + cleanup-on-error returning capabilities-applied client). spawn = resolve → factory → register → handshake. |
| Tests | Existing manager tests green; no behavior change. |

### P1.7 `core/commands/config.go:413` `(*configMenu).settingRetrySettings` — cognit 17

| | |
|---|---|
| Why over | SelectOption callback containing a switch whose cases each open another nested ShowInput callback with accept/apply/re-enter branching. |
| Fix | Extract per-item prompts: `(m *configMenu) promptMaxRetries(cfg *config.Config)` and `(m *configMenu) promptProviderRetryDelay(cfg *config.Config)` (each contains its own input callback; cognit ≤7 each); replace the inner switch with a `map[string]func(*config.Config)` handler lookup. Follows the existing per-setting-method convention in this file (`settingTheme` etc.). |
| Tests | Existing config menu command tests must stay green. |

### P1.8 `tools/smartsearch.go:589` `(*SmartSearchTool).formatResults` — cognit 17

| | |
|---|---|
| Why over | Header block (rebuilt warning, counts, score range) + per-result loop (relpath computation, optional id suffix, match truncation) + footer, all inline. |
| Fix | Extract `relPath(abs string) string` (ProjectDir-relative with `..` guard), `formatMatchLine(buf, m smartLineMatch)` (sanitize/truncate-at-140), `formatOneResult(buf, i int, r bm25.SearchResult, matches []smartLineMatch)`, `formatHeader(buf, query, results, idx, rebuilt)`. formatResults = four sequential calls. |
| Tests | Unit-test `relPath` (outside-project prefix guard) and `formatMatchLine` truncation boundary at exactly 140 columns. |

### P1.9 `plugins/plugin.go:334` `(*JSBridge).buildCompletionWrapper` — cognit 16

| | |
|---|---|
| Why over | Wrapper closure contains VM lock dance, recover, export-shape unwrapping ([]interface{} → map checks → Completion filtering) in one closure. |
| Fix | Extract pure `completionsFromExport(v any) []Completion` (handles array/map/nil shapes, drops empty & `<nil>` values). Closure keeps only enterVM/lockVM/recover/call/export. The extracted function is JS-free and directly unit-testable. |
| Tests | New `completionsFromExport` table test: non-array export, non-map items, missing value, `<nil>` value, valid entries. |

### P1.10 `tools/bash.go:211` `(*BashTool).ExecuteContext` — cyclo 13 (cognit ok)

| | |
|---|---|
| Why over | Seven sequential guard clauses (parse, empty, escalation, blocked, allowed, analyzed, confinement) plus result-handling precedence ladder (ctx-cancelled → timedOut → tooLarge → hint → out). |
| Fix | Extract `(t *BashTool) validateInput(ctx context.Context, p *bashParams) error` (all pre-run guards, early returns) and `(t *BashTool) reportResult(ctx, p, output, err, duration, timedOut, tooLarge, hint) (string, error)` (post-run precedence ladder). ExecuteContext ≈ 5 branches. Behavior-preserving; ordering inside each helper must match current semantics exactly (cancelled beats timeout beats tooLarge). |
| Tests | Existing `tools/bash_test.go` precedence tests are the safety net; add explicit cancelled-vs-timeout precedence case if absent. |

### Test-function complexity (P4 — lower priority)

12 gocognit offenders are tests (worst: `TestPluginHooks_MessagePreSend_Decisions` 44, `TestLoadManifest_HooksYAML` 28, `TestQuotaResets_CompletionOffersCreditsByExpiry` 23…). Root cause: long arrange sections + assertion ladders + repeated payload walking. Standard treatment when touched:

| Pattern | Fix |
|---|---|
| Giant single test asserting many decisions | Split into focused subtests via `t.Run` (each branch becomes its own function scope) or separate `TestX_Case` funcs |
| Repeated JSON/payload extraction ladders | Extract `mustDecode(t, …)`, `assertPayloadField(t, …)` helpers using `require` |
| Duplicated environment setup | Shared `newTestXxx(t)` constructor |

Do not chase these opportunistically across all packages; apply when a file is already being modified (see P3).

---

## Tier ⚪ P2 — Hard file-size violations (>1000 lines, build gate)

Split along existing seams only — move code verbatim first (compile-safe), then trim. The repo already uses the `<file>_<aspect>.go` convention (`agentmanager_lifecycle.go`, `config_cli_helpers.go`, `openai_completions_timings.go`).

### Production (4)

| File | Lines | Explanation | Fix |
|---|---|---|---|
| `core/commands/config_cli.go` | 1067 | CLI handlers + persistence machinery + ~25 setter factories in one file. | Move setter section (`configSetter` type + all `set*` funcs, from ~line 477 to EOF ≈ 590 lines) to new `core/commands/config_cli_setters.go`. Remaining ≈ 480. No logic changes. |
| `multiagent/foreground_orchestrator.go` | 1084 | Orchestrator core + output-event forwarding state machine + accessors mixed. | Move output-forwarding cluster (`agentOutputState`, `handleAgentOutputEvent`, `handleAgentContentEvent`, `handleAgentEndEvent`, `finishThinking`, `toolResultPreview`, `recordRoleEvent`, `forwardCacheStats`, ≈ lines 310–446) to `multiagent/orchestrator_output.go`; move companion-count/setter accessors (`SetPromptRegistry`…`CompanionCount`) to `multiagent/orchestrator_config.go`. Core < 800. |
| `internal/agentic/provider/protocol/openai_completions.go` | 1025 | Request building + message conversion + cache-control injection under banner comments. | Split at the banner seams: message conversion (`convertMessages`…`convertTools`, image/dataURL helpers ≈ lines 260–420) → `openai_completions_messages.go`; cache-control section (`cacheControl` type through `supportsLongCacheRetention`, ≈ lines 424–end) → `openai_completions_cache.go`. Keep request-building in the base file. |
| `core/agentmanager.go` | 1017 | Turn-record types + steering plumbing inflate the core file; siblings already exist (`_lifecycle`, `_events`, `_modes`, `_state`, `_accessors`). | Move turn-record types (`TurnTokenUsage/TurnRecord/TurnToolCall/TurnToolResult/TurnTiming`, ~60 lines) → `core/agentmanager_turns.go`; move steering cluster (`SendUserInput*`, `steeringSourceAdapter`, `DispatchPendingSteering`, `SteeringQueue/SetSteeringQueue`) → `core/agentmanager_steering.go`. |

### Tests (5)

| File | Lines | Explanation | Fix |
|---|---|---|---|
| `core/commands/team_test.go` | 1108 | 38 test funcs across team CRUD/lifecycle/membership. | Split by area: `team_create_test.go` (create/validation), `team_lifecycle_test.go` (start/stop/status), remainder stays. Shared fakes → `team_testhelpers_test.go`. |
| `config/config_test.go` | 1035 | 43 test funcs mixing loader, merge, provider assertions (`assertProvider` cognit 19 also lives here). | Split into `config_providers_test.go` (provider/model assertions — home of `assertProvider`, refactor it per P1 patterns) and `config_loadmerge_test.go`. |
| `plugins/quota_resets_test.go` | 1067 | Reset-store tests vs completion-flow tests (contains the two worst quota tests). | Move completion tests (`TestQuotaReset_Completion`, `TestQuotaResets_CompletionOffersCreditsByExpiry`, neighbors from ~line 862) → `quota_resets_completion_test.go`; simplify them per P4 patterns while moving. |
| `provider/manager_test.go` | 1046 | Registry, resolution, and fallback concerns interleaved. | Split into `manager_registry_test.go` and `manager_resolution_test.go` along existing test-name prefixes. |
| `tools/bash_test.go` | 1004 | 70 test funcs: security policy vs execution/formatting. | Move security tests (`checkBlocked/checkAllowed/checkAnalyzed/confinement/escalation`) → `bash_security_test.go`; execution/timeout/format stay in `bash_test.go`. |

Verification for every split: `go build ./...`, affected `go test -count=1 -race ./pkg/...`, then re-run size check.

---

## Tier ⚪ P3 — Soft violations near hard limit (prod files ≥850 lines)

Not gating today but will trip the 1000 hard limit with modest growth. Schedule as dedicated splits (same seam technique as P2), ordered by proximity:

| File | Lines | Proposed split |
|---|---|---|
| `internal/agentic/agent_streaming.go` | 991 | stream event pump vs renderer/progress plumbing → `agent_streaming_events.go` |
| `config/loader.go` | 975 | load cascade vs save/migrate → `loader_save.go` (its tests already split that way) |
| `tools/search/bm25/index.go` | 986 | index build/persist vs query/scoring entrypoints → `index_query.go` |
| `internal/lsp/manager.go` | 935 | spawn/handshake (after P1.6) vs registry/lifecycle → `manager_lifecycle.go` |
| `internal/agentic/agent.go` | 947 | run-loop vs tool-execution glue → `agent_tools_bridge.go` |
| `config/config_merge.go` | 952 | merge primitives vs per-section merge tables |
| `internal/python/stdlib/re.go` | 938 | pattern compile vs match/exec engines |
| `app/bootstrap.go` | 912 | config/bootstrap phases → `bootstrap_providers.go` |
| `app/submithandler.go` | 850 | submit pipeline stages |
| `plugins/plugin.go` | 847 | bridge wrappers vs manifest lifecycle (post-P1.9) |
| `plugins/bridge_extended.go` | 829 | per-domain bridge extensions |
| `tui/chat_viewport_components.go` | 887 | component builders vs layout |
| `config/wizard_render.go` | 881 | render views already partially split (`wizard_render_views.go`) — continue |
| `skills/loader.go` | 826 | discovery vs parse/validate |

Remaining ~77 soft files (500–850, prod+test): boy-scout rule only — when a file is touched for other reasons, extract obvious units until it's under 500 or the natural seam is exhausted. Do not batch-refactor these.

---

## Execution order & gates

1. **P0** dead code (single delete) → rerun staticcheck.
2. **P1.1–P1.10** complexity fixes, worst-first (P1.1→P1.10); after each: package tests + targeted recheck; add unit tests for extracted pure helpers.
3. **P2** hard file-size splits (prod first, then tests); after each: `go build ./...` + package race tests.
4. **P3** near-limit splits, one PR/file.
5. **Final gate**: `go vet ./... && go test -count=1 -race -cover ./... && .agents/skills/golang-check/golang-check.sh` — expect exit 0 except accepted soft-violation residue (<500-line misses documented above).

Rules honored throughout: behavior-preserving refactors, no `//nolint`, no threshold edits, root causes fixed, new helpers get tests.
