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

## Thinking loop not configurable from CLI flags
Config still does not list thinking loop as configurable — there are no "temporary" command flags to disable/enable loop detection (both tool and thinking) as requested. (reopen !)

## Input line: up-arrow at start of empty line does not work
Pressing the "up" arrow key when at the start of a new, empty input line does nothing or does not navigate correctly.

## Input line: cannot navigate to an empty line
The input line reader does not allow cursor movement/navigation to an empty line.

## Goal tool triggers cache stat collapse — disable by default
The `goal` tool seems to trigger cache stat collapse. Investigate why !
Goal files should be located under `.goa/goals`

Goal tool should be disablabe via configuration such as other non-mandatory tools

## Spinner disappears after 1st tool call
The spinner disappears after the first tool call instead of continuing to show activity for subsequent calls.

## Steering messages not processed — appear in chat but are not enqueued
Adding a steering message does not work — messages are directly sent (appear in the chat) but do not seem to be processed. They should be enqueued and kept on top of the input line until the moment they are sent to the model.

## config selection list cursor
The cursor in the config selection list is put at random position - it should be set at the "search>" input

## smartsearch
Review smartsearch in the optic of a agent tool and check optimization/options required to make it more useful

## Reviews
* Stability: Run a stability review on all TUI code - there are known issues with stability that need to be addressed - especially possible panic and crash that are not handled gracefully and lead to an unexpected end of processing.
* Perf: Run a critical review on code quality/optimization on all TUI code
* Functional: Critical review workflow/swarm/multi-agent/multi-model/goal implementation testing

## Orchestration
There should be an orchestrator mode where a top-level orchestrator manages the execution of multiple agents (with their own model) - the TUI should allow a goal view with tabs (for each agent) so the user can switch between agents and see their progress.

The goal/orchestrator should show the progress of all agents in a single view (per agent: stats as the status bar) and a chat view so the user can see the conversation between the orchestrator and the agents and add steering messages to guide the agents.

This can use all existing option: goal, workflows, ...
