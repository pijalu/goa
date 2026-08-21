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
## Cache stats are not working
The are lost during goals but should be preserved for all the session !
Output is not as expected:

* The result should show 10 vertical continuous bars (separated by spaces - with actual percentage, under each bar)
* The weight should not be by turn but follow the token/cache ratio 
* Each turn should have it's own line + show the number of tokens/cache hits/misses in addition of the bar
```
T1 : 00045.4kT - CM: 0-1 - CH: 93.0% ███████████████████
T2 : 00056.0kT - CM: 1-1 - CH: 97.3% ████████████████████
...
```

Current:
╭─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
│ # Cache hit — last completions (rightmost = newest)                                                                                                 │
│ 100%   █ █                                                                                                                                          │
│        █ █                                                                                                                                          │
│                                                                                                                                                     │
│  75%   █ █                                                                                                                                          │
│        █ █                                                                                                                                          │
│                                                                                                                                                     │
│  50%   █ █                                                                                                                                          │
│        █ █                                                                                                                                          │
│                                                                                                                                                     │
│  25%   █ █                                                                                                                                          │
│        █ █                                                                                                                                          │
│                                                                                                                                                     │
│      ─ ─ ─      0 100 100                                                                                                                           │
│ # Cache usage per turn                                                                                                                              │
│ T1      0.09%  T2     99.71% ████████████████████ T3     99.82% ████████████████████                                                                │
│ # Session total: 82.29% cache hit (weighted over 3 turns)                                                                                           │
│ # Cache misses                                                                                                                                      │
│ No cache misses detected. No cache drops detected.                                                                                                  │
╰─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
```

### Fix plan — /stats:cache redesign + session preservation
Part 1 (lost during goals) root cause:
`AgentManager.SendUserInputWithImages` called `turnRecorder.ResetTurn()`
BEFORE the steering/busy early-return. Every user message typed while a goal
turn owned the agent wiped the in-flight turn's accumulators, so the
finalized TurnRecord carried zero tokens and /stats:cache lost the session's
cache history exactly during goals. Fix: move ResetTurn AFTER the busy check
(only a turn that actually starts clears accumulators).

Part 2 (output format) changes in core/commands/stats_cache.go:
1. Per-turn section lines become the required format — one line per
   cache-active turn: `T<num> : <tokens>kT - CM: <full>-<partial> -
   CH: <rate>% <20-col band-colored bar>`; tokens = full prompt-side volume
   (PromptN + CacheRead + CacheWrite) zero-padded kT ("00045.4kT"); CM =
   cumulative full/partial miss counters through that turn (footer CM
   semantics); CH = per-turn rate.
2. Vertical last-10 chart bars widened to 4 columns so each bar's actual
   percentage renders under it ("%3.0f%%" label row); empty bands become
   4-space cells to keep columns aligned; baseline ──── per bar.
3. Session total line wording → "(token-weighted over N turns)" (totals sum
   already weights by tokens; matches bug 'Average cache').
4. Interpretation note: the report's sample "T1 … CM: 0-1" fixes the FORMAT;
   T1 cannot be a miss (nothing established yet), so values follow the
   footer's cumulative CM counters.

Test approach:
- Regression: user input mid-goal must NOT wipe in-flight turn accumulators
  (CurrentTurn keeps recorded token usage).
- Format tests: per-turn line exact shape incl. padded kT figure, CM
  counters, colored bar; chart geometry with 4-wide cells and under-bar
  percentage labels; multi-agent groups unchanged.
- Terminal-output validation: capture showCacheStats into a writer and
  assert rendered lines.

Validation steps: gates per guideline (each separately) + full race suite.



