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



## Provider not always updated
Provider can sometimes be incorrect in the status bar, usually after changing a model: eg: This model is openrouter but show as openai-codex
```
(openai-codex) stealth/ox-alpha
```

Make sure the provider/couple are *always* updated together

## Average cache
The average cache should be calculated/weighted by the number of tokens cached - so a 10k token miss (0%) and a CH 5k token hit (100%) not show 50% but 33%.

eg:
```
newLevel = (currentLevel × currentTokens + latestLevel × latestTokens)
           / (currentTokens + latestTokens)
```

This global rate should show as 1st value in CH percentage (replace the last X)
