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

### Fix plan — token-weighted session average (footer CH 1st value)
Root cause: the footer's first CH value is an unweighted mean of the last ≤10
per-round rates (`CacheHitTrend.window` / `AvgPct`) — every round counts the
same regardless of how many tokens went through the cache pipeline.
Interpretation note: the footer renders `CH:<avg>%▸<last>%`; the global
token-weighted rate becomes the **1st** value; the most-recent-round rate stays
as the 2nd (the "last") value.

Changes:
1. `internal/app/stats.go`: drop `window`/`AvgPct`/`AvgPrevPct`;
   `CacheHitTrend` gains `GlobalPct`, `GlobalPrevPct`, `GlobalHasPrev`.
2. `internal/app/stats_tokens.go`: keep a running weighted level on App
   (`cacheHitGlobalLevel`/`cacheHitGlobalWeight`). Per cache-active round:
   rate = metrics.CacheHitPct(read, write, prompt);
   weight w = CacheRead+CacheWrite, falling back to PromptN when both are 0
   (uncached miss still carries its full prompt weight — matches the report's
   10k-miss/5k-hit → 33.3% example under goa's normalized PromptN, verified
   against computePromptN which strips cached tokens from PromptN);
   newLevel = (level·W + rate·w)/(W+w); W += w.
3. `formatLastCacheHitPart`: render CH:<GlobalPct>%▸<Pct>%, each element
   colored by its own evolution (existing >=1pt grow / >=5pt drop scheme).
4. `cacheHitTrendFromTotals` (orchestrator rows/headless): totals-derived rate
   already is token-weighted — feed it to both GlobalPct and Pct.

Test approach:
- Table-driven fold test over App.handleTokenStats path reproducing the
  report example (miss 10k → 0%, full hit 5k → 100%, expect 33.3%).
- Footer string tests: CH shows weighted global as 1st element, ▸last as 2nd;
  coloring per element preserved.
- Regression: existing footer/stats suites updated to the new struct.

Validation steps:
- `go vet ./...`; `staticcheck ./...`; `gocognit -over 15 .`;
  `gocyclo -over 12 .`; `go test -count=1 -race -cover ./...` (each run
  separately).
- Verify rendered footer segment in a stats filmstrip/unit capture shows the
  weighted figure after mixed-rate rounds.
