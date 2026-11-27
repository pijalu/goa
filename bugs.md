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

## Excessive CPU usage during streaming

**Observed:** `goa` CPU usage while streaming is much higher than expected —
over 20% of a core sustained during a response, even on small conversations.
Usage CLIMBS once the streamed answer grows past the visible screen / buffer
(i.e. as rows spill into scrollback) and stays high for the rest of the turn.
**Expected:** streaming should be dominated by network wait; per-chunk render
work should stay well under a few percent (incremental diff path, no
full-transcript recompose per chunk).
**Needs:** a profiling review (CPU profile during a long streaming turn,
`GOA_PROFILING` / pprof) to identify the hotspot before changing anything. The
"climbs past the viewport" correlation points at work proportional to transcript
length per chunk: a full-canvas recompose / `\x1b[3J` scrollback reset per chunk
(should be the settled-once deferred path), per-row `uniseg` width measurement
over the whole history, or chat-viewport re-wrapping of all entries on each
append.
