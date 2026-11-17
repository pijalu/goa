# Bug and feature Tracking

## Guideline
1. Create a detailed fix plan for each bug - the plan must contain test approach and validation steps - execute the plan and validate the fix when all elements are in place.
2. Any issues found must be fixed and the fix plan must be updated accordingly.
3. Issues found during testing must be fixed and the fix plan must be updated accordingly.
4. Each bug should be moved to docs/archive when tested and closed as the associated plan.
5. Use interactive shell/filmstrip to validate the output of the tool - you must verify the actual terminal output.
6. Check code quality with each tool run separately (do not chain them with `;` or `&&`):
- `go vet ./...`
- `staticcheck ./...`
- `gocognit -over 15 .`
- `gocyclo -over 12 .`
- `go test -count=1 -race -cover ./...`
Fix any issues.
! For cognitive and cyclomatic complexity, Pre-existing warnings are acceptable only if they are unrelated to the change and explicitly noted !

At the end of the session - the bug list should be empty, change committed and this file should only contain the guidelines for bug reporting.
If new items are added, restart the process.

Use goals to execute the fix plan - focus on micro tasks goals with new contextto lower context usage - use todos for micro tasks that should share context

Commit at the end of each fix with a clear and descriptive commit message

## Report format
Describe the bug or feature request under `# To fix` below. Keep one section
per item with a short title, the observed behavior, and the expected behavior.

# To fix

## Edit tool floods agent context and makes deletion awkward

**Observed:** The `edit` tool returns the entire file as a before/after unified diff on every call. On a 600-line source file a single one-line change reprinted ~600 removed lines plus ~600 added lines (~20k tokens), so a normal refactor of ~15 edits consumed most of the context window on tool output alone, leaving little room for the actual work and forcing a context reset mid-task. Deleting text is also needlessly indirect: `replace` rejects an empty `new_string` (`missing_parameter: Empty 'new_string': this edit would DELETE the matched block without inserting anything`), so deletion requires `delete_lines` with exact line numbers — but line numbers go stale after any line-count-changing edit in the same batch (the tool itself warns `stale_line_numbers`), so `delete_lines` cannot be safely batched with other edits. A third friction: `replace` with an `old_string` that appears more than once hard-fails with `ambiguous_match` instead of applying to all occurrences or defaulting to the first.

**Expected:** The tool reports only what changed: matched region, old text, new text, and a short success line (or a bounded diff of the changed hunk, e.g. ±5 lines), not the whole file. Deletion should be a first-class, drift-safe operation — either allow empty `new_string` for `replace`, or provide a `delete` operation keyed by unique `old_string` (not line numbers) so it composes in the same batch as other edits. Ambiguous matches should offer a `replace_all` / `occurrence` option (or clear guidance) instead of aborting.

**Fix plan:** (1) Change edit result rendering to emit a compact per-edit summary plus a context-bounded hunk; add a snapshot test asserting output size stays O(changed lines), not O(file lines). (2) Allow empty `new_string` on `replace` for a true deletion, or add a string-keyed `delete` op; test a batch that deletes a line and edits later lines, asserting no stale-line failure. (3) Make ambiguous matches return a structured `ambiguous` result with occurrence count and support `replace_all`/`occurrence`; test both a single-occurrence and multi-occurrence edit. Validate with a large-file integration case: a 10+ edit refactor of a 600-line file must keep total tool output under a few thousand tokens and leave the file byte-correct. (4) Make multi-file edits (`files` and `glob`) non-atomic by default: process each file independently, apply valid file edits even when another file fails, and return a clear overall error with per-file `ok`/`fail` outcomes and no silent skips. Add optional `atomic: true` for all-or-nothing behavior: preflight every file before writing, leave every file untouched on validation/edit failure, and roll back already-written files on write failure. Reject `atomic` on single-file forms, where each call is already atomic. Test mixed valid/invalid inputs in both modes, write-failure rollback, exact resulting bytes, and per-file error reporting; update schemas and embedded docs.
