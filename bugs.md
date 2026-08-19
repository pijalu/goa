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

# TODO

## /stats:cache is incomplete
/stats:cache is incomplete and does not match requirement - currenly show:
```
# Cache misses
T1 - CM: Full 0 (0t) / Partial 0 (0t) T2 - CM: Full 0 (0t) / Partial 0 (0t) Cache hit rate — latest completions (rightmost = newest)
# Cache usage per turn
T2 - CH: 63.74% |███████████████ No cache drops detected.
```

It should contains key sections
* Render the last 10 cache hit level - shown as barchart - with exact percentage under - color coded red < 90%, orange < 95%, green >=95% - make sure to have the bar/label correctly center
* Render bar chart with the average cache per turn - the bar per turn should be horizontal and use color code similar to 10 last cache - eg (use block instead of =):
```
T1: 85.98% ==================
T2: 97.07% ====================
```
* Provide a weighted total cache percentage of the complete session
* Provide the list of all cache miss: Turn - Percent of the miss vs non-miss + miss size in token

Key point: The stats must work across multi-agent/multi-goals - you can repeat the section per "goal/agent"
