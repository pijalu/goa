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

## Standalone Todo markers survive session/tool lifecycle changes

**Observed:** the footer can show `coding-posture ⬩⬩⬩+1` after the visible
work is over. Four open standalone Todos legitimately render this way; the
glyph sequence alone is not a rendering failure. The lifecycle review found
that `/new` neither clears nor republishes the standalone Todo list, and
runtime disable/re-enable can replace the tool with an empty instance while
`SessionPendingTodos` remains preserved at its old value. The lifecycle audit
also reproduced stale source state and footer markers after `/session:restore`
and `/fork`; standalone Todo changes are footer events, not persisted agent
events, so replay cannot reconstruct or clear them.

**Expected:** the footer always reflects the current session's actual
standalone list. New, restored, and forked sessions start with no standalone
Todos or markers; removing/replacing the tool cannot leave an orphaned count;
ordinary add/update/complete/remove/clear and active-goal precedence remain
unchanged, including the intentional `⬩⬩⬩+1` overflow for four genuinely open
items.

**Reproduction and test plan:**
1. Drive the real tool, production event handlers and rendered footer through
   four adds, then `/new`; assert both the list and rendered markers reset.
2. Reproduce tool disable/re-enable with four open items and assert no stale
   markers survive the new empty tool state. Cover any shared-state/session
   ownership fix at both startup registration and runtime-factory call sites.
3. Restore a saved session with no persisted standalone Todo state after four
   adds, and fork a session with an existing item. Assert both operations clear
   the source list and rendered footer without delaying the first fresh frame.
4. Re-run the existing goal/session isolation filmstrip to prove goal-linked
   Todos, paused goals, completion restoration, and valid overflow rendering
   are not regressed. Capture and inspect actual terminal footer output.

**Implementation and validation plan:** localize ownership and reset
notification at the application/tool lifecycle boundary; make the smallest
root-cause fix that keeps tool state and footer writes atomic from the user's
perspective. Do not suppress or rewrite the valid overflow marker. Run the
focused race tests and filmstrips, all quality gates separately, archive this
item, and commit it independently from per-model idle configuration.
