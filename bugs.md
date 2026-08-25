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

### BUG: ReplayRunner.Submit can block forever (deadlock) under concurrent Submits

**Observed:** `go test -count=1 -race ./...` times out after 10m in
`tui/agentctx.TestReplayRunner_ConcurrentSubmitClose`: Submit goroutines stuck
on chan send (replay.go Submit), runner goroutine stuck delivering result #5
into the cap-4 `results` channel nobody drains during the storm, main test
blocked on WaitGroup.

**Root cause:** `Submit`'s drain-then-send over the cap-1 `reqCh` channel is
not atomic (TOCTOU): concurrent Submits interleave between draining the slot
and the blocking fallback send. When the loop goroutine cannot return to
receive (its `run()` is blocked pushing into the full `results` buffer), every
submitter blocks forever.

**Expected:** `Submit` never blocks, keeps atomic latest-wins coalescing, and
the runner stays closable regardless of results consumption.

## Fix plan

1. Replace the cap-1 request channel with a mutex-guarded `pending
   *ReplayRequest` slot plus a cap-1 wake signal; Submit sets pending and
   signals non-blockingly; loop drains latest-wins until empty.
2. Test approach: the existing `TestReplayRunner_ConcurrentSubmitClose`
   (4×25 concurrent Submits, no results consumer, then Cancel+Close) is the
   regression — it deadlocked pre-fix.
3. Validation: `-race -count=10` on that test, then the full gate battery.
