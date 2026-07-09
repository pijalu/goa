<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bug Tracking

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

## Workflow for bugs
1. Reproduce the failure before editing — ideally a command or script that triggers it on demand.
2. State the observed failure exactly (command + output).
3. Localize to the smallest region — ideally the specific lines — before editing. Precise localization is the strongest predictor of a correct fix.
4. Change one hypothesis at a time.
5. Prefer the minimal fix over a broad refactor.
6. Verify against the original failing command before declaring done.
7. Run the code-quality checks from guideline #6 separately and confirm the fix does not introduce new violations.
8. Move the bug list to `docs/archive/bugs.<fixdate>.md` when all items are closed.

# Open Bugs

4. **Delegate tool spawns new agents instead of reusing idle agents**
   - Observed: session shows `coder`, `coder·2`, `coder·3` after sequential delegations.
   - Expected: the orchestrator should reuse an idle agent of the same role when one is available.
   - Fix: investigate `multiagent/orchestrator.go` / `foreground_orchestrator.go` agent pool / lifecycle logic; implement agent reuse in the orchestrator adapter so released agents are returned to a per-role pool and reused on the next `Delegate` of the same role.
   - Test: orchestrator runtime test that a second delegation to the same role reuses the finished agent.

5. **Screen tearing / spurious full redraws**
   - Observed: compositor appears to redraw content with one line less, causing tearing.
   - Fix: review `tui/compositor.go` diff / render logic to ensure only changed regions are redrawn; footer height changes (orchestration stats line) may be triggering full redraws.
   - Test: golden / filmstrip diff test that footer height change does not cause chat viewport full redraw.
