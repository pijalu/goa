# Bug and feature Tracking

## Guideline
1. Create a detailed fix plan for each bug - the plan must contain test approach and validation steps - execute the plan and validate the fix when all elements are in place.
2. Any issues found must be fixed and the fix plan must be updated accordingly.
3. Issues found during testing must be fixed and the fix plan must be updated accordingly.
4. Each bug should be moved to archive when tested and closed as the associated plan.
5. Use interactive shell to validate the output of the tool - you must verify the actual terminal output.
6. Check code quality with each tool run separately (do not chain them with `;` or `&&`):
   - `go vet ./...`
   - `staticcheck ./...`
   - `gocognit -over 15 .`
   - `gocyclo -over 12 .`
   - `go test -count=1 -race -cover ./...`
   Fix any new issues introduced by the change. Pre-existing warnings are acceptable only if they are unrelated to the change and explicitly noted.

At the end of the session - the bug list should be empty and this file should only contain the guidelines for bug reporting.
If new items are added, restart the process.

## Must fix

- `--- FAIL: TestOrchestrateCommand_ResumeRebindsGoal (0.00s)` — `/orchestrate:resume` goal rebinding (`core/commands/orchestrate_command_test.go:259`). Note: passes locally standalone (×5, `-race`) and in full `core/commands` package run — reproduce in the failing environment to capture the actual output before fixing.

- **gpython: `AttributeError: "'file' has no attribute 'readlines'"`** — file objects returned by `open()` don't support `.readlines()`:
  ```
  ✗ python
  >>> path = "internal/function/function.go"
  ... with open(path) as f:
  ...     lines = f.readlines()
  ...
  Error: [python error: execution_error]
  Traceback (most recent call last):
    File "<python>", line 3, in <module>
  AttributeError: "'file' has no attribute 'readlines'"
  ```

- **Goal tool**: When multiple goal tool calls are executed, the request order should be kept.

## Workflow for bugs
1. Reproduce the failure before editing — ideally a command or script that triggers it on demand.
2. State the observed failure exactly (command + output).
3. Localize to the smallest region — ideally the specific lines — before editing. Precise localization is the strongest predictor of a correct fix.
4. Change one hypothesis at a time.
5. Prefer the minimal fix over a broad refactor.
6. Verify against the original failing command before declaring done.
7. Run the code-quality checks from guideline #6 separately and confirm the fix does not introduce new violations.
8. Move the bug list to `docs/archive/bugs.<fixdate>.md` when all items are closed.
