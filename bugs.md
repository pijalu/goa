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

### Hy4 tagged reasoning/tool markers leak into assistant output

**Severity:** Medium — visible protocol garbage also contaminates the assistant text retained in the session.

**Observed:** With `opencode-go` / `hy4-flash-free`, assistant text contains tagged XML-like markers, for example:

```text
Need edit.</think:6124c78e>Need add grounded,
```

**Expected:** Hy4 control markers are never rendered or persisted as assistant prose. Removing them must preserve the surrounding text byte-for-byte and must not duplicate, drop, or reorder native tool calls.

**Review / evidence:**

- Source bundle: `/Users/muaddib/dev/panelcraft/.goa/exports/goa-export-20261118-081708.zip` (`manifest.json` identifies `opencode-go` and `hy4-flash-free`).
- This is present in the upstream response, not invented by the TUI: `logs/http.jsonl`, request at `2026-11-18T08:15:19.872618+01:00`, has a raw SSE delta with `delta.content` equal to `.</think:6124c78e>`.
- `logs/agent.log:580-585` also shows the related Hy4 dialect (`<tool_calls:6124c78e>`, `<tool_call:6124c78e>`, `<arg_key:6124c78e>`, and `<arg_value:6124c78e>`) arriving in thinking, followed by `</think:6124c78e>` in content. `session/events.jsonl` contains four visible content deltas carrying `</think:...>`.
- `internal/agentic/provider/protocol/openai_completions.go:581-586` forwards every non-empty `delta.content` string unchanged. `internal/agentic/agent_stream_content.go:21-37` suppresses only recognized tool-call markup; it has no streaming handling for Hy4's `:hex-suffix` think/tool tokens.

**Fix plan and test approach:**

1. Add a protocol/agent replay regression from the captured SSE shape. Include the exact marker above, a different suffix, markers split across deltas, and adjacent native tool calls. Assert clean displayed/final text and unchanged native calls.
2. Add a streaming-safe Hy4 dialect normalizer at the provider/protocol boundary, selected by the resolved model/profile rather than by globally deleting arbitrary XML. Buffer possible partial tags until they can be classified; do not leak a partial marker while waiting for the next delta.
3. Normalize both content and thinking paths before display and history assembly. Cover the related tagged tool-call tokens present in the export; route any complete recovered call through the existing tool controller exactly once, and never execute an incomplete tagged call.
4. Keep ordinary assistant prose containing unrelated XML unchanged, and verify that existing DeepSeek/OpenAI reasoning fields and generic auto-heal behavior are unaffected.

**Validation:**

- Run the new parser and agent streaming regressions, including split-marker and no-duplication cases, then the existing provider/agent suites with the race detector.
- Replay the export through the real display path and inspect the actual terminal/filmstrip: no `</think:...>` or tagged tool markup may appear.
- Run one live `hy4-flash-free` tool-using turn and confirm clean output, intact history, and exactly one execution per valid native call.
