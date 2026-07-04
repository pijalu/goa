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

# TODO

## Tool execution interrupted (goa-export)
Tool execution was interrupted/stopped during the bug-fixing session. Export saved at:
  /Users/muaddib/dev/goa/.goa/exports/goa-export-20260704-135610.zip
Investigate why the tool was stopped and whether export contains useful debugging data.

At this point the spinner was not visible - there were no visible indicator of issues

## Steering message are late
Steering message should be sent as soon as possible - check how ../pi does
The message should be kept as "Pending" on top of the input until sent successfully to the model - same as Pi

## Performance
Performance of the TUI is degrading with increasing content - pointing to redraw problems.
The performance/cpu usage is increasing just typing in the input field.

## search
Search tool should support working on a file - eg: search configSetters in *.go under ~/dev/goa/core/commands/config_cli.go should find all configSetters in that file
(the file can be specified as a path or glob pattern)
