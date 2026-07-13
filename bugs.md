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
## docs/docs.go
The docs.go should only contains document required for goa agent to understand goa - not about changes and fixes.
Execute a cleanup of all documentation to ensure it only contains the required content

Bug fixes, older plan should be moved to archive.

## Steering stop
See /Users/muaddib/dev/goatest3/.goa/exports/goa-export-20260713-093519.zip

Steering text was sent very late - check with Pi at ../pi if the steering on goa is sent as soon as it could be ! (steering should be sent as soon as possible without breaking the flow)

## Tool call streaming
write (and edit) should support streaming of tool call: when the LLM is streaming a tool call, the details should be visible to the user in real time to avoid a "freezing" feeling when the tool call is being prepared.

Eg: if a write is being prepared - the TUI should show a write tool bubble with content being streamed (couple line visible/ctrl-o to expand/summary of size being updated in real time) like Pi (../pi)

## Tool call timing
When executing a tool call, especially for execution tools like bash, the TUI should show the elapsed time in addition of the timeout duration.


# Bugs found by qa-e2e skill

## B1: Empty `--prompt ""` launches TUI instead of headless mode

**Scenarios:** 3 (Error handling)

**Observed:** `goa --yes --timeout 30s --prompt ""` opens the full TUI instead of
running in headless mode. The command hangs waiting for TUI input.

**Root cause:** In `internal/app/bootstrap.go`, `promptImpliesHeadless()` checks
`o.prompt != ""` — when the prompt is explicitly set to empty string, it returns
false, so goa falls back to TUI mode. The intent of `--prompt ""` is to run
headless with an empty/invalid prompt; the tool should produce an error about
empty input rather than opening a TUI.

**Fix:** Change `promptImpliesHeadless()` to also check whether the `--prompt` flag
was explicitly set (not just whether the value is non-empty).

## B2: `goa build -o` target must be `./cmd/goa/` not `.`

**Scenarios:** Setup

**Observed:** Running `go build -o /tmp/goa ./goa` builds a library archive
(`current ar archive`) instead of an executable binary.

**Root cause:** The module root package (`github.com/pijalu/goa`) is a library,
not a main package. The main entry point is in `cmd/goa/`.

**Note:** This is a documentation/build knowledge issue, not a code bug. The
qa-e2e skill build instructions should use `./cmd/goa/`.

## B3: Headless mode output format uses structured markers

**Observed:** The headless renderer outputs lines like `-- user`, `-- thinking start`,
`-- assistant`, `-- stats`, `-- summary`. While this is useful for parsing, the
qa-e2e skill's validation instructions assume simpler output. Validation should
account for the actual output format.

**Mitigation:** The skill validation is flexible enough (checks for substrings),
so this is not a blocker. It may cause false negatives if validation expects
exact output matching.
