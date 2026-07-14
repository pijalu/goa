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

## TUI issues: 1 empty line at bottom
The TUI displays an empty line at the bottom of the terminal - it will never be filled:
```
▄▄▄▄▄      ▄▄▄▄▄             ▄▄▄▄▄▄      ▄▄▄▄      ▄▄▄▄
▄▄▄▄▄▄ ▄▄▄▄▄      ▄▄▄▄▄▄ ▄▄  ▄▄    ▄███ ████ ▄███ ████ ▄███ ████
▄ ▄▄▄ ▄       ▄  ▄       ▄  ▄▄      ████ ████ ████ ████ ████ ████
▄ ▄▄               ▄         ▄ ▄    ████ ████ ████ ████ ▄▄▄▄▄████
▀▄   ▄▄▄     ▄  ▄ ▄     ▄          ▀███▄████ ▀███▄███▀ ████▄████
▄▄▄▄▄▄ ▄   ▄▄▄▄▄▄      ▄     ▄▄▄▄ ████
          ▄          ▄     ▀▀▀▀▀▀▀▀
         ▄
     ▄▄▄▄

goa coding agent v0.1.0-dev
Ctrl+C/D exit  |  / commands  |  Tab complete  |  ↑↓ history

╭──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
│ ⟡ Context loaded: /Users/muaddib/dev/goa/AGENTS.md                                                                       │
╰──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
╭──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
│ ⟡ 13 skills (1 inline, 12 forced inline · mode: inline)                                                                  │
╰──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
╭──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
│ ⟡ Connected to LM Studio (google/gemma-4-e4b).                                                                           │
╰──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯

────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────

────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
~/dev/goa (✱ feature/ui-issues)                                                                                 coder │ YOLO
                                                                              (lmstudio) google/gemma-4-e4b • high

```


## TUI issues: TUI selection widget breaks scrolling/history redraw — RESOLVED 2026-07-14

Fixed and archived: see `docs/archive/bugs.2026-07-14.md`.
Root cause: the editor autocomplete popup was appended to the base render,
growing the base canvas past the terminal height and pushing base content
into scrollback on open (irrecoverable on close). Fix: popup is now a
`LayerOverlay` via the new `PopupRenderer` interface, so the base canvas
height is constant and scrollback is never touched. Regression:
`tui/autocomplete_popup_scrollback_test.go`.
