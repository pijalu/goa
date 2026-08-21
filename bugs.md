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

### Fix plan — atomic provider/model couple
Symptom `(openai-codex) stealth/ox-alpha`: footer provider label comes from
ProviderManager.Active() while the model string comes from cfg.ActiveModel —
any path that updates one surface without the other renders a mixed couple,
and the live agent can keep the old credentials/model.

Drift sources found:
1. `/provider <id>` (switchProvider) and the picker (applyProviderSelection)
   mutate cfg + persist but NEVER call propagateModelSwitch → agent session
   keeps old model/stream options (next turn uses stale creds).
2. `/model` paths mutate+persist cfg BEFORE ProviderManager.SetActive; if
   SetActive fails (provider unknown to the manager copy, e.g. post hot
   reload) cfg says X, manager/agents say Y → mixed state + label.
3. `config set active_model` propagates only when the provider CHANGED;
   same-provider switches never reach the live agent.
4. core/dream.go resolveModel permanently overwrites ActiveProvider/
   ActiveModel with the dream couple and never restores them.

Changes:
1. New `applyCoupledSwitch(host, cfg, saver, providerID, modelID)` in
   core/commands/model.go: validate+push into the manager FIRST
   (SetActive error ⇒ abort with nothing mutated anywhere), then commit to
   cfg, persist, propagateModelSwitch (agent SetModel + stream options +
   thinking level), FooterRefresh.
2. Rewire runModelCommand, applyModelSelection, switchProvider,
   applyProviderSelection through it (fixes 1 & 2).
3. config_cli: propagate on every active_model switch (fixes 3).
4. dream.go: defer-restore the previous couple around resolveModel (fixes 4).

Test approach:
- recording PM/AM harness: /provider switch reaches AgentManager (SetModel)
  and stream options rebuild; picker path likewise.
- SetActive failure leaves cfg couple untouched (no mixed state, no save).
- config set active_model on the SAME provider still pushes SetModel.
- dream resolveModel restores prior couple.

Validation steps: gates per guideline (each separately) + full race suite.
