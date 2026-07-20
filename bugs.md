<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bug/Feature Tracking

## Guideline

1. Create a detailed fix plan for each bug/new feature - the plan must contain test approach and validation steps - execute the plan and validate the fix when all elements are in place.
2. Any issues found, even if not related to the bug/feature, must be fixed and the fix plan must be updated accordingly. You can add new items to the bug list as you find them.
3. Each item should be moved to archive when tested and closed as the associated plan.
5. Use filmstrip approach to validate the output of the tool - you must verify the actual terminal output.
6. Check code quality with each tool run separately (do not chain them with `;` or `&&`):
   - `go vet ./...`
   - `staticcheck ./...`
   - `gocognit -over 15 .`
   - `gocyclo -over 12 .`
   - `go test -count=1 -race -cover ./...`
   Fix any new issues introduced by the change. Pre-existing warnings are acceptable only if they are unrelated to the change and explicitly noted.

*At the end of the session*: the list should be empty and this file should only contain the guidelines for bug reporting.
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

# Open TODO

## Spurious `context.Canceled` — automatic turn termination without user action
**Referenced session:** `/Users/muaddib/dev/frigolite/.goa/sessions/1784544003_xj3msiq8.jsonl`
**Export (diagnostic bundle):** `/Users/muaddib/dev/frigolite/.goa/exports/goa-export-20260720-125911.zip`

### Description
Agent terminates with `context.Canceled` during active streaming, without any user Ctrl+C/Escape keystroke. After termination, the session machinery auto-submits a "resume" user message, which is immediately canceled again (within 2 seconds). Both events display "Generation stopped by user." in the TUI, misleadingly.

Observed in a session where the model made 19 sequential tool-calling rounds (never self-terminating). During the 19th round's final streaming (thinking tokens), the stream was aborted with `context.Canceled`. After auto-resume, the new stream was canceled at the first thinking delta.

### Root causes (two separate issues)

**Issue A — Transport-level `context.Canceled` misclassified as user cancel:**
- File: `internal/agentic/agent_streaming.go` — `handleStreamFailure`
- `shouldRetryStreamError` does not classify bare `context.Canceled` as retryable
- `isTransientStreamError` pattern list does not include `"context canceled"` or `"canceled"`
- Comment in `retry_classify.go:34-38` explicitly says "Context cancellation is NOT excluded" — but the actual `isTransientStreamError` pattern list has no matching entry, so bare `context.Canceled` from transport is always non-retryable
- When the outer context (`ctx.Err()`) is still nil (not user-canceled), but the stream error is `context.Canceled` (server-side abort), it should be retried, not surfaced as terminal
- Impact: Server-side connection drops that surface as `context.Canceled` terminate the turn irrecoverably, even though retrying would succeed

**Issue B — No per-turn consecutive-tool-calling-round guardrail:**
- File: `internal/agentic/agent_streaming.go` — `runStreamRound`
- Model made 19 consecutive rounds all ending `finish_reason: "tool_calls"`, never self-terminating
- All existing guardrails (`MaxToolRepeatTotal`, `MaxToolRepeatConsecutive`, `MaxToolCalls`, `LoopDetector`) key on exact (tool, input) repeats — all 104 tool calls had unique inputs, so none fired
- The horizon extension logic (lines 79-83) extends `maxStreams` by 50 when the model is "making progress", up to 250 — but there's no check for "still requesting tools without producing an answer"
- Fix: Add a configurable limit on consecutive tool-calling rounds (e.g., 10 rounds of `finish_reason: "tool_calls"` triggers a forced-answer hint)

### What's needed
1. In `shouldRetryStreamError`: when `context.Canceled` arrives but `ctx.Err() == nil`, treat as retryable (transport-level abort, not user cancel)
2. In `Interrupt()`: log every call with caller identity or stack trace
3. Add per-turn round counter guardrail (configurable, suggest default 10 consecutive `tool_calls` rounds → force answer)
4. Fix "Generation stopped by user." label to differentiate user-canceled from system/transport aborts

### Verification
RED: Reproduce by connecting to a provider that sporadically drops connections (or simulate with a test provider that returns `context.Canceled` mid-stream). Observed failure: turn terminates with "Generation stopped by user." even though user did not press any key.

GREEN: Same scenario with fix: retry succeeds, turn continues. `Interrupt()` calls are logged with caller identity.

## Full usage statistics
Goa should have a general usage statistics feature that provides insights into the tool's usage/models/providers.
It should extend the `/stats` command and provide a similar type of details as ../opencode-stats and ../opencode 

- default /stats should show these details by default 
- /stats:session should show session-specific statistics (the current session/turn stats)

The stats should be per project - the approach should support multiple goa agents.
