# Bug Fix Plan — remove pre-compaction tool-result pruning

## Bug

At the hard ceiling (95%) with method `summarize`, goa compacted via
`tool_result_pruning` instead:

```
⚡ Context compacted (tool_result_pruning): 95% → 49% pruning resolved
context pressure; summarize skipped
```

The user: **there should be no pre-pruning.** At the hard ceiling the agent
must summarize. Pruning is only acceptable as the *summarize-overflow
fallback* (to make room for a summarize retry).

## Root cause

`internal/agentic/agent_compression.go` `compactOrdered` runs
`pruneToolResultsPreCompact()` (CX1) **ahead of** the summarize. When the
prune re-measures under the hard ceiling it returns `skip=true` and the
function early-returns with "pruning resolved context pressure; summarize
skipped" — so the configured summarize never runs. The pruning pass rewrites
historical tool results in place (head + PruneMarker + tail), destroying
content the summarize would have condensed.

## Fix

Pre-compaction tool-result pruning is now **configurable, default OFF** (the
chosen design — a hard removal would strand the CX1 feature for the
dump-heavy sessions it was built for):

1. `config.ToolResultPruningSettings` gains `Enabled *bool` +
   `PruningEnabled()` — absent at every cascade layer = off; the merge is
   tri-state (only an explicitly set pointer overrides a lower layer).
2. `agentic.ToolResultPruningConfig` gains `Enabled bool` (zero value off).
3. `compactOrdered` runs `pruneToolResultsPreCompact()` only when
   `Enabled` — by default it proceeds straight to `summarizeHistory`.
4. The summarize-overflow fallback (`shrinkToolPayloadsToFitLocked` →
   `pruneToolResultsLocked`) is NOT gated: pruning still runs there to make
   room for a summarize retry, exactly per the bug requirement.
5. `buildToolResultPruningConfig` maps YAML → SDK (default off).

## Tests

- New `TestAgent_CompactNoPrePruningByDefault` (prune_test.go): dump-heavy
  history at the hard ceiling with the default config → exactly one summarize
  LLM call, NO `tool_result_pruning` compaction event, NO prune marker in the
  landed history. Verified to FAIL without the fix (summarize call count = 0).
- The four CX1 pre-prune tests now opt in with
  `ToolResultPruningConfig{Enabled: true}` and still pass (they pin the
  enabled path: skip-when-resolved, fallthrough-when-insufficient,
  transaction boundary, stale pass).
- New `TestMergeToolResultPruningEnabled` (config): default off, true
  override, false override, unset-preserves-lower.

## Validation

1. `go vet` — clean
2. `staticcheck` — clean
3. `gocognit -over 15` / `gocyclo -over 12` on changed files — clean
4. `go test -count=1 -race -cover ./internal/agentic/ ./config/ ./core/` —
   pass (agentic 85.2%, config 80.4%, core 74.1%)

## Execution log

Status: **verified closed** — see Validation above.
