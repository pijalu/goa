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

### BUG: flaky CI failure — TestQuota_CarouselPrefersAPIProvidersOverLocal renders an empty segment

**Observed:** intermittent failure in the CI quality job (`go test -count=1 -race
-cover -timeout 5m ./...`, ubuntu-latest), not reproduced locally (darwin/arm64:
single run, `-count=20`, and full-package `-race` all pass):

```
--- FAIL: TestQuota_CarouselPrefersAPIProvidersOverLocal (0.07s)
    quota_plugin_test.go:364: carousel should prefer anthropic over local: ""
FAIL github.com/pijalu/goa/plugins 11.533s
```

The asserted segment render returned `""` — either no segment with id "quota"
was registered in the UIBridge at render time or its Render() produced an
empty string. Timing-sensitive suspects to investigate (plugins/quota_harness_test.go,
bundled/provider-quota/plugin.js):
- `quotaTestEnv.load` drains the async 0-delay prime then immediately stops the
  scheduler (`drainPrime` + `scheduler.Stop`); a slow CI machine may interleave
  a timer callback differently than locally.
- Segment render may legitimately return "" when the cache entry for the
  active provider holds an error/no data (e.g. the anthropic fetch never landed
  in `_cache` before `quota refresh` ran, or refresh raced the stopped scheduler).

**Expected:** the test passes deterministically under CI conditions. Root cause
must be fixed (timing/hook design in the plugin or harness) — do NOT weaken the
assertion or paper over it with retries/sleeps.

**Resolution (FIXED):** root cause was NOT quota data — it was the plugin
scheduler's timer lifecycle. A `setTimeout` one-shot deregistered itself from
the scheduler map BEFORE its first fire attempt; when that attempt was deferred
by an active JS frame (`invokeSafeWithReschedule`, vmActive>0), `fireOnce`
parked in a 50ms back-off loop that `Scheduler.Stop()`/`Clear()` could no
longer reach (no map entry → stop channel never closed). The zombie goroutine
from an earlier test's cold-`/quota` async render later entered a VM frame in
the microsecond gap between this test's `quota refresh` and `renderSegment()`;
`buildSegmentRender` skips renders while `vmBusy()` and returns "".
Fix: one-shots stay registered until terminal state, deregistered after
`fireOnce` returns (plugins/scheduler.go bfa0c0d). Regression tests:
TestScheduler_StopCancelsDeferredOneShot / TestScheduler_ClearCancelsPendingOneShot /
TestScheduler_OneShotRemovedAfterRun (red before fix). Validation: plugins
package 5× green under -race; carousel test 50× under -race.

### ENHANCEMENT: model/provider switch persists to ~/.goa unconditionally — project .goa must be the primary (and normally only) store

**Observed:** `persistModelSwitch` (core/commands/config_persist.go) writes the
project pin gated on `execution.auto_save_model`, but THEN always calls
`SaveHomeProvidersAndModels(cfg)` which rewrites `active_provider` +
`active_model` into `~/.goa/config.yaml`. Consequence: switching the model in
one project updates the global home default, which leaks to every other
project that has no explicit pin. Current tests even mandate the dual write
(core/commands/model_persist_test.go: "Home config should also be updated",
and TestModelChange_PersistsWithoutAutoSave asserting the home update).

**Expected:** a `/model` (or provider/model add/remove) change stores first —
and by default ONLY — in the project `.goa` directory. `~/.goa` must NOT be
updated for a model change unless the project config cannot be changed (no
project directory configured, or the project write fails e.g. read-only tree);
in that case fall back to writing home so the choice still survives restart.
Rationale: models are usually project-related; changing one place for all
projects is not a good design. Update the persistence code and the tests that
currently assert the home write (model_persist_test.go, config_persist_test.go)
plus any UI copy claiming "saved globally".

**Resolution (FIXED):**
- `config.ErrNoProjectDir` sentinel added; `CascadeLoader.SaveProjectActiveModel`
  returns it when no project dir is configured (was silent nil) — callers can
  now distinguish "project layer unchangeable" from success.
- `persistModelSwitch` (core/commands/config_persist.go) rewritten:
  `auto_save_model=true` (default) → write the per-project pin ONLY; on ANY
  project-layer failure (ErrNoProjectDir, unwritable tree) fall back to
  `SaveHomeProvidersAndModels`. `auto_save_model=false` keeps legacy home-only.
  A successful pin no longer touches ~/.goa at all.
- Tests updated/added: flipped home-write assertions (model_persist_test.go,
  PerProjectPinEndToEnd), explicit opt-out configs for the two
  *PersistsToHomeConfig tests, new fallback tests
  (TestPersistModelSwitch_ProjectErrorFallsBackToHome,
  TestPersistModelSwitch_UnwritableProjectFallsBackToHome), sentinel test
  (TestSaveProjectActiveModel_NoProjectDirIsNoop).
- Docs updated (CONFIGURATION.md auto_save_model comment, COMMANDS.md /model).
- Out of scope (noted as follow-ups): `/config set active_model` still writes
  the key to ~/.goa via the generic /config-setter path;
  `saveModelThinkingLevel` still persists thinking levels via home
  providers+models (carries active_model along); mergeExecution copies
  AutoSaveModel unconditionally, so a config file omitting the key resets the
  embedded default true→false (pre-existing tri-state bool limitation).
