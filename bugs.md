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

## Runaway-loop guardrail false-positives on tool-progress turns

Evidence: `.goa/exports/goa-export-20260824-092731.zip` (issue.md: goal
auto-paused with `runaway loop detected: the assistant repeated the same
response 3 consecutive times without progress (repeated: "(empty response)")`
while the agent was actively executing different tools/tests each turn).

Observed: `internal/agentic/agent_loop_progress.go` fingerprints an assistant
turn via `hashAssistantMessage` = Content + Thinking + `len(ToolCalls)`, and
`isMeaningfulAssistantMessage` treats every assistant message — including
empty ones — as meaningful. An agent working in goal mode legitimately emits
little or no prose while running different tools every turn; those turns all
hash to `\x00\x00 0` (or equal short text) and score as repeats. Three such
turns trip the latch and kill the session despite real progress.

Expected: a turn that executed tool calls successfully must count as progress;
only true no-op turns (no content, no thinking, no tool calls) may accumulate
repeat strikes, and text-only repetition keeps its existing behavior.

Fix plan:
1. `hashAssistantMessage`: include a per-tool fingerprint (name + stable arg
   digest), not just the tool-call count.
2. Strike gating: when this turn issued ≥1 tool call with a non-error result,
   reset the repeat counter instead of incrementing it.
3. Empty-response strikes only apply when Content+Thinking are empty AND the
   turn carried zero tool calls.
4. Tests (`agent_loop_guardrail_test.go`): identical prose + different tool
   calls ⇒ no strike; empty turn WITH tools ⇒ no strike; three truly-empty
   no-tool turns ⇒ latch; identical prose-only replies ⇒ warning then latch.
5. Validate by replaying the export scenario through those tests; full
   agentic suite green.

## Command list can render an incorrect space (blank line) between entries

Observed: the command palette list occasionally shows one or more blank lines
between consecutive rows, breaking the contiguous item layout. Example capture:

```
search>
────────────────────────────────────────────────────────────
› Active model x-preview-f-free
Bash warn on shell file edits: on
Compression 2 active
Execution mode yolo
Goals 7 days


Loop detection warn:10 stop:15
Manage models Add, edit, remove, or select models
MCP servers none installed
(15 more)
────────────────────────────────────────────────────────────
↑↓ nav / type filter / enter / esc / + add / - delete
```

Expected: no blank/space lines between entries — `Goals 7 days` should be
immediately followed by `Loop detection warn:10 stop:15` like every other row.
