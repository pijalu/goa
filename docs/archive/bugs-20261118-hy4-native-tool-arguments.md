# Fix plan — Hy4 truncated native tool arguments

Source: `bugs.md` item “Hy4 truncated native tool arguments reach `edit` and fail as invalid input”.
Evidence: `/Users/muaddib/dev/panelcraft/.goa/exports/goa-export-20261118-081708.zip`.

## Root cause and constraints

- Native OpenAI `EventToolCallEnd` flows directly through `handleStreamToolCallEnd` to `shouldBufferToolCall`; it bypasses `ToolLoopController.PrepareCall`.
- `streamAccum` accumulates native arguments without classifying JSON completeness. `SafeToolArguments` is history-only and must never produce executable repaired input.
- Existing silent-round and exact-repeat controls do not guarantee bounded recovery: text/thinking resets the silent-round streak, and SDK round limits may be disabled.
- Native calls without execution need an assistant-call/tool-result pair, a visible typed error result, and no concrete `Execute` or `EventToolStart`.

## Implementation

1. Add table-driven native-argument classification for empty input, valid objects, truncated JSON, malformed JSON, trailing data, and valid non-object JSON. Empty input retains existing `{}` behavior; executable native input must be one complete JSON object.
2. Gate native `EventToolCallEnd` finalization before budget guards and scheduler. Keep protocol accumulation and history serialization unchanged. Never repair-and-execute.
3. Generalize non-executing synthetic results with a typed reason. Truncation must emit a stable `Error:` protocol diagnostic, `metaToolError=true`, and one visible `EventToolResult`, while preserving append-only assistant/tool history. Valid sibling calls retain exactly-once scheduling.
4. Add an explicit per-turn malformed-native-call recovery budget rather than relying only on configurable loop guards. Permit two reissue opportunities per episode; a further malformed response ends the turn with a visible diagnostic. Reset after a valid native call or new user turn.
5. Handle `edits` encoded either as an array or one string containing an array in `tools/editfile.go`; decode exactly once, reject nested encoding or invalid element shapes, and keep this compatibility separate from truncation handling.
6. Split classifier, synthetic-result, and recovery helpers to remain within gocognit 15 / gocyclo 12.

## Regression tests

- Sanitized fixtures reproduce all six captured calls, including the 229-byte call and `finish_reason:"tool_calls"`; tests must not require the absolute export path.
- Agent-level instrumented edit tool plus temp file proves zero executions, zero starts, and unchanged bytes for every malformed fixture.
- Mixed valid/truncated batch proves valid siblings execute once and truncated siblings never execute.
- Reissued complete native call succeeds once; always-malformed sequence proves exact recovery bound with other loop guards disabled and prose/thinking present.
- Following request retains original assistant arguments, exactly one paired error result per call, strict-provider-safe serialization, and unchanged session/cache identity.
- Valid flat, batched, and string-encoded batched edits apply exactly; invalid encoded edits do not touch files.
- Existing generic auto-heal and valid native-call suites remain green.

## Validation and closure

- Run new tests RED before implementation, then GREEN with race detector and timeouts.
- Replay protocol → agent → real `App.handleAgentOutputEvent` through `uiScenario`; assert visible typed protocol error and no concrete start.
- Replay through a real PTY/local SSE stub and inspect `tui.TermEmulator` visible rows and scrollback; stop every server/process and verify ports.
- Run all quality gates separately, record results, archive this plan and bug report, remove the fixed item from `bugs.md`, and commit as `fix(agent): reject truncated native tool arguments before execution`.

## Execution log

Status: **verified closed**.

- Added native argument classification at agent finalization, before scheduling. Empty arguments retain `{}` semantics; only a complete JSON object is executable. Malformed, truncated, and wrong-shape arguments receive typed provider-protocol errors.
- Added paired, non-executing synthetic tool results with visible error metadata. No concrete tool start or execution occurs. A valid sibling still executes exactly once.
- Added an explicit two-reissue recovery budget per malformed-native-call episode. A valid native call or new user turn resets the episode; exceeding the budget terminates the turn without rewriting cache identity or retained assistant arguments.
- Preserved raw retained arguments and used `SafeToolArguments` only for provider-bound history serialization. Migrated provider tool-result blocks now retain `IsError`; this was added after the strict-next-request regression exposed its omission.
- Added one-level string-encoded `edits` compatibility for `edit`. Native arrays and one encoded array work; malformed, nested, and wrong-element payloads fail before writing.
- Split test assertions and UI checks after complexity gates identified new test-only findings; all findings introduced by this fix are now below budget.

### Regression and terminal evidence

- RED before implementation: all six captured cases executed concrete `edit`; the valid-reissue control executed twice; the bounded-recovery control executed eight times; the mixed batch executed twice.
- GREEN: all six sanitized captures, valid reissue, bounded recovery, mixed valid/truncated batch, strict next request, cache identity, and argument classification pass. The protocol replay preserves malformed raw arguments for the agent gate.
- Targeted race tests pass for `internal/agentic`, `internal/agentic/provider/protocol`, `tools`, and `internal/app`.
- Real `uiScenario` filmstrip and `tui.TermEmulator` validation shows `truncated_tool_arguments`, `not executed`, and terminal `ToolError`; no running frame or old generic `invalid_input` error appears.
- Real PTY/local SSE replay artifact: `/tmp/goa-hy4-native-pty-terminal.txt`. Actual terminal output contains `Error: [provider error: truncated_tool_arguments]`, explicit non-execution guidance, and `RECOVERY_COMPLETE`; `[edit error: invalid_input]` is absent and no new `test.txt` was created. Server pid 98974 and port 61489 were stopped and verified closed; the Goa PTY was stopped.
- The first PTY harness attempt did not start its stub because Python `HTTPServer` blocked in `socket.getfqdn`; the harness was changed to use a local bind identity and flushed port-file write. This was not an application failure.

### Quality gates (each run separately)

1. `go vet ./...` — pass.
2. `staticcheck ./...` — pass.
3. `gocognit -over 15 .` — no findings in changed code. Unchanged baseline findings remain in `provider/manager_auth_startplan_test.go`, `tools/mcp_binary_test.go`, `internal/app/tool_factory_coverage_test.go`, `core/agentmanager_turns.go`, `internal/app/subsystems_goal.go`, and `config/tools_default_mirror_test.go`.
4. `gocyclo -over 12 .` — no findings in changed code. Unchanged baseline finding remains in `internal/agentic/agent_history_appendonly_test.go` (`alignOneMessage`).
5. `go test -count=1 -race -cover -timeout 180s ./...` — pass across the repository; changed-package coverage: `internal/agentic` 86.5%, `internal/app` 67.2%, `tools` 84.5%, `internal/agentic/provider/protocol` 79.9%.
6. `git diff --check` — pass.

## Archived bug report

### Hy4 truncated native tool arguments reach `edit` and fail as invalid input

**Severity:** High — valid edit operations repeatedly cannot run; malformed calls must remain non-executing to avoid partial or truncated writes.

**Observed:** `edit bugs.md` fails with:

```text
Error: [edit error: invalid_input]
Cannot parse parameters: unexpected end of JSON input
Hint: Ensure your input is valid JSON with the required fields.
```

The export contains six such failures. The failed argument strings end inside `new_content` without its closing quote or the object's closing brace. Failures occur at both large sizes and only 229 bytes, so this is not explained by a single large-edit/output-limit threshold. A separate earlier call also double-encoded `edits` as a JSON string and failed with `cannot unmarshal string ... into ... []tools.editFileParams`; that is a valid-JSON wrong-type case, not the truncation root cause.

**Expected:** An incomplete native tool call is classified before concrete tool execution. No file is touched, the model receives explicit guidance to reissue a complete native call, and recovery is bounded. Complete valid edits keep working unchanged.

**Review / evidence:**

- Same source bundle and model as the Hy4 marker report. `session/events.jsonl` records six `unexpected end of JSON input` edit results, including `chatcmpl-tool-a96a68b02be3cce7`, `chatcmpl-tool-b2dbe9310e9455a6`, and the 229-byte `chatcmpl-tool-b798cfad8033f18a`.
- The truncation is already on the wire: the raw response at `2026-11-18T08:16:36.87405+01:00` ends the last argument fragment with `the straight line between the endpoints`, then emits `finish_reason:"tool_calls"` and `[DONE]`; it never sends the closing JSON string/object. The response at `08:17:01.309198+01:00` shows the same pattern for the 229-byte call.
- Native argument accumulation passed malformed strings through finalization to `EditFileTool.Execute`. `SafeToolArguments` sanitized later request serialization only and was not safe for execution repair.
- Text-markup auto-heal did not cover malformed native arguments.

**Resolution:** The implementation, regressions, terminal evidence, and quality results above close this report. Malformed native arguments are never repaired and executed; valid calls retain existing behavior.
