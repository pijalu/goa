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

## Last-used model is global (home) instead of per-project; no usage-based default

**Observed:** Switching models persists `active_provider` / `active_model`
to the HOME config (`saveHomeProvidersAndModels`), so every project shares
one "last used" model. When no default model is configured, startup falls
back to an arbitrary/hardcoded choice rather than learning from usage.

**Expected:** The last-used model is saved first to the PROJECT layer
(`.goa/config.yaml`) so each project keeps its own active model; home stays
as fallback. When no default model exists in any config layer, pick the
most-used model from the persistent usage stats (`.goa/usage.json`) instead.

**Fix plan:**
1. Persist model switches to the PROJECT layer first via
   `ConfigSaver.SaveProjectField`; fall back to the existing home write
   only when there is no writable project dir.
2. On boot, resolve active model: config value → else most-used entry from
   usage store (top by request count among configured providers/models) →
   else existing fallback.
3. Tests: (a) switch in project A writes `.goa/config.yaml` there and
   leaves home untouched; (b) second project keeps its own pinned pair;
   (c) empty config + seeded usage store selects top-used model; (d) empty
   stats fall back as before.
4. Validation: `go test ./internal/app ./core/commands -run
   'ModelSwitch|ActiveModel|Usage'`; quality gates separately; filmstrip
   check of footer showing each project's own model; commit.

# TODO

*(open items tracked under `# To fix` above; move each to docs/archive when
tested and closed.)*
