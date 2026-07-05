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

## Question are not render correctly
ask_user_question tool show odd escape like #30363d - possibly some incorrect ansi code

```
▾ thinking...
▏The user has provided a single command: "summarize". This is an ambiguous request.
▏1. Analyze the context: The project is about building a complex AI coding agent (goa). There are many files, skills, and
▏   architectural components.
▏2. Determine intent: When a user just says "summarize," they likely want a summary of the current state, the goal, or perhaps
▏   the available documentation/context.
▏3. Identify necessary information: Since no specific topic is given, I should ask for clarification to provide a useful
▏   summary.
▏I will use ask_user_question to clarify what needs summarizing.


✓ run tool
[ask_user_question] 1 question(s) answered:
[{"question":"What would you like me to summarize? Please specify the topic, file, or area of the project (e.g., \"the architect

#30363d╭─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
#30363d│ #c9d1d9❓ Topic for Summary
#30363d│                                                                                                                         #30363d│ #8b949ePlease provide context so I can give you a relevant summary.
#30363d│
#30363d│ What would you like me to summarize? Please specify the topic, file, or area of the project (e.g., "the architecture," "
#30363d│ current task plan," "a specific skill's documentation").
#30363d╰─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────

─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
~/dev/goa (✱ feature/self)                                                                                           coder │ SOLO
↑4.3K ↓225 45.0 tok/s TC:1 7.1%/32.0K (auto)                                                 (lmstudio) google/gemma-4-e4b • high
```
