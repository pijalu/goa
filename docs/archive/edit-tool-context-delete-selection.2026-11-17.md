# Edit tool floods agent context and makes deletion awkward

**Reported:** 2026-11-17
**Resolved:** 2026-11-17

## Original report

**Observed:** The `edit` tool returns the entire file as a before/after unified diff on every call. On a 600-line source file a single one-line change reprinted ~600 removed lines plus ~600 added lines (~20k tokens), so a normal refactor of ~15 edits consumed most of the context window on tool output alone, leaving little room for the actual work and forcing a context reset mid-task. Deleting text is also needlessly indirect: `replace` rejects an empty `new_string` (`missing_parameter: Empty 'new_string': this edit would DELETE the matched block without inserting anything`), so deletion requires `delete_lines` with exact line numbers — but line numbers go stale after any line-count-changing edit in the same batch (the tool itself warns `stale_line_numbers`), so `delete_lines` cannot be safely batched with other edits. A third friction: `replace` with an `old_string` that appears more than once hard-fails with `ambiguous_match` instead of applying to all occurrences or defaulting to the first.

**Expected:** The tool reports only what changed: matched region, old text, new text, and a short success line (or a bounded diff of the changed hunk, e.g. ±5 lines), not the whole file. Deletion should be a first-class, drift-safe operation — either allow empty `new_string` for `replace`, or provide a `delete` operation keyed by unique `old_string` (not line numbers) so it composes in the same batch as other edits. Ambiguous matches should offer a `replace_all` / `occurrence` option (or clear guidance) instead of aborting.

**Fix plan:** (1) Change edit result rendering to emit a compact per-edit summary plus a context-bounded hunk; add a snapshot test asserting output size stays O(changed lines), not O(file lines). (2) Allow empty `new_string` on `replace` for a true deletion, or add a string-keyed `delete` op; test a batch that deletes a line and edits later lines, asserting no stale-line failure. (3) Make ambiguous matches return a structured `ambiguous` result with occurrence count and support `replace_all`/`occurrence`; test both a single-occurrence and multi-occurrence edit. Validate with a large-file integration case: a 10+ edit refactor of a 600-line file must keep total tool output under a few thousand tokens and leave the file byte-correct. (4) Make multi-file edits (`files` and `glob`) non-atomic by default: process each file independently, apply valid file edits even when another file fails, and return a clear overall error with per-file `ok`/`fail` outcomes and no silent skips. Add optional `atomic: true` for all-or-nothing behavior: preflight every file before writing, leave every file untouched on validation/edit failure, and roll back already-written files on write failure. Reject `atomic` on single-file forms, where each call is already atomic. Test mixed valid/invalid inputs in both modes, write-failure rollback (including failure after bytes land), explicit rollback_failed reporting, exact resulting bytes, and per-file error reporting; update schemas and embedded docs.

## Fix

- Replaced whole-file before/after output with independent unified-diff hunks bounded to three context lines. Distant edits no longer reprint the unchanged content between them.
- Added explicit string-keyed `operation:"delete"`; empty `new_string` replacement remains rejected. Whole-line deletion removes the line rather than leaving an empty replacement line, while existing empty `new_content` semantics remain unchanged.
- Added one-based `occurrence` and `replace_all` selection within the existing fuzzy-match tiers. Ambiguity errors include the match count and selection guidance. Matches are deterministic, leftmost, non-overlapping, and spliced once without recursively matching replacement text.
- Made `files` and `glob` edits non-atomic by default. Every valid file is attempted, successful output is retained alongside aggregate errors, and every target has an `ok`, `fail`, or duplicate-path `skip` outcome.
- Added top-level `atomic:true` for complete preflight and all-or-nothing writes. Write/read-back failures restore exact original bytes in reverse order, including the currently failing write. Incomplete restoration is reported as `rollback_failed` with affected paths; single-file `atomic`, including nested single-file batch fields, is rejected.
- Updated schemas, operation validation, line-drift guards, renderer labels, examples, and embedded documentation. Promoted the already-used `go-difflib` module to a direct dependency without changing its version.

## Regression coverage

- A 600-line file with 12 distant edits: exact resulting bytes, 12 independent hunks, and output bounded by changes plus context.
- Explicit deletion in a composed batch, occurrence selection, replace-all, ambiguity counts, non-overlapping substring edits, invalid selectors, and single-file `atomic` rejection.
- Non-atomic validation/write continuation, malformed entries, glob behavior, successful-output preservation, and per-file outcomes.
- Atomic validation failure, write failure, post-write verification failure, CRLF/exact-byte restoration, rollback failure reporting, and atomic glob failure/success.
- Renderer coverage for atomic labels and multiple hunks under one file header.
- Empty replacement regression coverage proving non-delete operations still write one empty line.

## Validation

- The initial regression suite failed to compile against the missing write-failure seam; targeted tests passed after implementation.
- Final `go test ./tools -count=1 -timeout 120s` passed.
- `go vet ./...` passed.
- `staticcheck ./...` passed.
- `go test -count=1 -race -cover ./...` passed; `tools` coverage was 84.5%.
- `gocognit -over 15 .` reported only unrelated pre-existing findings in provider start-plan, MCP binary, app tool-factory, config defaults, `initGoalSystem`, and `AgentManager.executeRunner` tests/code.
- `gocyclo -over 12 .` reported only the unrelated pre-existing `alignOneMessage` finding in `internal/agentic/agent_history_appendonly_test.go`.
- Real edit execution plus renderer/`Filmstrip` validation passed on the 600-line/12-edit case: 1,404 output bytes, 12 hunks, exact bytes, ANSI-free frames, visible atomic labeling, and non-atomic per-file outcomes. The temporary validation source was removed; no server or browser was started.

**Commit:** recorded separately with the implementation.
