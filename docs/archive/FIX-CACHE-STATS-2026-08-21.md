# FIX — Cache stats: /stats:cache redesign + session preservation

> **Closed**: 2026-08-21 · commit `90d02f0` (feature/multi-agent)
> **Source**: bugs.md report "Cache stats are not working"

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




## Test approach & validation results
- TestSendUserInput_MidGoalTurnPreservesStats — user input while a goal turn
  owns the agent must NOT wipe in-flight accumulators (pre-fix: ResetTurn ran
  before the steering check and zeroed them). PASS
- TestSendUserInput_IdleResetsAccumulators — real new turns still start from
  clean state. PASS
- TestWriteCacheAvgPerTurn / _MissCounters — required line format, padded kT,
  cumulative CM counters incl. bust turns. PASS
- TestStatsCommand_CacheView — terminal-output validation of rendered lines:
  'T1 : 000000.4kT - CM: 0-0 - CH: 75.0%', bust line 'T3 : 000000.5kT - CM: 1-0
  - CH: 0.0%'. PASS
- Chart geometry tests updated for 4-column cells with under-bar percentage
  labels ('100%', ' 50%', '  0%'). PASS
- Gates (each separately): go vet ./... OK · staticcheck ./... OK · gocognit/
  gocyclo only pre-existing warnings in untouched files · go test -count=1 -race
  -cover ./... all green.
