# Code Quality Gate — Plan & Final Gate Record (K-series)

Objective: bring the repository through the quality gates enforced by
`.agents/skills/golang-check/golang-check.sh`:

- `go vet ./...` clean
- `go test -count=1 -race -cover ./...` fully green
- cognitive complexity ≤ 15, cyclomatic complexity ≤ 12 (gocognit/gocyclo)
- staticcheck clean
- file size: **hard max 1000 lines** per Go file; 500 lines is a soft *target*
- accepted residue must be documented here

## Hard-limit fixes applied in the final gate (K18)

| File | Problem | Fix |
|------|---------|-----|
| `internal/agentic/agent_struct_config.go` | staticcheck SA1019: deprecated `math/rand.Read` in `newCacheContextID` | switched to `crypto/rand.Read`; cache-context IDs are security-relevant so CSPRNG is correct |
| `tools/editfile.go` | HARD limit violation: 1022 lines (>1000) | extracted the pure line-operation engine (`runOp`, `replaceLines`, `replacePattern`, insert/delete helpers, indent normalizers) into `tools/editfile_ops.go` (704 + 332 lines); zero behavior change |

## Complexity fixes applied (tests only; no coverage removed)

Test tables whose inline closures pushed parent `Test*` functions over the
thresholds were decomposed into named helpers:

- `plugins/manifest_hooks_test.go` — TestLoadManifest_HooksYAML (cognit 28 → OK):
  manifest YAML hoisted to a const, full-schema assertions to
  `checkFullSchemaHooks`, case runner to `runManifestCase`.
- `plugins/hook_enforcement_test.go` — TestHookEnforcer_GrantGate (cognit 22 → OK):
  approval branches to `applyHookDecision` / `approveDefVersion`, per-case body
  to `runGrantGateCase` over a named `grantGateCase` table type.
- `plugins/grants_test.go` — TestGrantStore_RoundTripAndPreservesOthers (cyclo 14 → OK):
  split into `approveGrantFor` / `assertGrantA` / `assertGrantBPreservesA`.
- `plugins/quota_resets_completion_test.go` — both completion tests (cognit 22/23 → OK):
  shared helpers `quotaCompleter`, `completionValues`, `hasString`,
  `indexOfString`; assertion groups extracted per completion level.
- `core/goal/gate_test.go` — VerifyChallengesThenCloses (cyclo 14) and
  EscalationAutoBlocks (cyclo 13): phase helpers `assertVerifyChallenge`,
  `assertEvidenceClosesGoal`, `createVerifyGoal`, `assertAutoBlocked`.
- `core/goal/todo_test.go` — TestGoalMode_TodoLifecycle (cyclo 13): mutation and
  replay phases to `assertTodoMutation` / `assertTodoReplayRoundTrip`.
- `core/plan/store_test.go` — TestStoreCreate (cyclo 13): byte-count via
  `bytes.Count`, plan.json decode via `readPlanSnapshot`.
- `core/plan/annotations_test.go` — TestAnnotationsSummary_Basic (cyclo 13):
  header/open/resolved sections into named assert helpers.

## K18 final gate results

| Gate | Result |
|------|--------|
| `go vet ./...` | clean |
| `go test -count=1 -race -cover ./...` | green — all packages pass under `-race` |
| gocognit > 15 | no findings |
| gocyclo > 12 | no findings |
| staticcheck | no findings |
| hard file limit (1000) | no violations |

## Accepted soft residue: files above the 500-line soft target

The 500-line value is a soft target only (`go-file-size-check.sh` reports it,
fails only ≥1000). **168 files** currently exceed it (~35% of them are `_test.go`
files, where long tables are idiomatic). Distribution by directory:

| Files | Directory |
|-------|-----------|
| 26 | core/commands (config menus/skills/orchestrate command layer) |
| 23 | tui (editor/footer/selector/viewport render+test pairs) |
| 18 | tools (tool implementations + their tests) |
| 16 | internal/agentic (+ provider subpackages) |
| 12 | core (agentmanager, sessionstore, loopdetector…) |
| 11 | internal/app |
| 11 | config |
| 6  | plugins |
| 4  | multiagent · 4 core/plan · 3 internal/python/stdlib · 3 internal/lsp |
| 3+2+1s | tools/goal, tools/search/bm25, tui/orchestrator, remaining singles |

This is the accepted residue of previous decomposition rounds (each generation
of splitting produced cohesive sub-files that remain just past target). Further
splitting is deferred until a file approaches the hard limit or needs feature
work; mechanically halving cohesive units now would trade real cohesion for a
cosmetic line count.
