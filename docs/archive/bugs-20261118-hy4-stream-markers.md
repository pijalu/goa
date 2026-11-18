# Hy4 tagged reasoning/tool markers leak into assistant output

**Status:** Fixed and validated on 2026-11-18.

**Severity:** Medium — visible protocol garbage also contaminated assistant text retained in the session.

## Observed

With `opencode-go` / `hy4-flash-free`, assistant text contained tagged XML-like markers, for example:

```text
Need edit.</think:6124c78e>Need add grounded,
```

Hy4 control markers had to be removed without changing surrounding prose or duplicating, dropping, or reordering native tool calls.

## Source evidence

- Source bundle: `/Users/muaddib/dev/panelcraft/.goa/exports/goa-export-20261118-081708.zip` (`manifest.json` identifies `opencode-go` and `hy4-flash-free`).
- `logs/http.jsonl`, request at `2026-11-18T08:15:19.872618+01:00`, contained a raw SSE delta whose `delta.content` was `.</think:6124c78e>`; the bytes came from upstream, not the TUI.
- `logs/agent.log:580-585` contained `<tool_calls:6124c78e>`, `<tool_call:6124c78e>`, `<arg_key:6124c78e>`, and `<arg_value:6124c78e>` in thinking, followed by `</think:6124c78e>` in content. `session/events.jsonl` contained four visible content deltas carrying `</think:...>`.
- The OpenAI-completions parser forwarded content and thinking directly. Generic agent auto-heal recognized only unsuffixed tool markup, so it could not safely tokenize Hy4's hexadecimal-suffixed grammar.

## Resolution

- Added the profile capability `compat.response_dialect`, with exact-model Hy4 defaulting only for OpenAI-completions `hy4-flash-free`. Layered `none` explicitly disables it; other models, APIs, and legacy `ParseResponse` retain standard behavior.
- Added a model/profile-aware protocol response entry point and wired both generic runtime response-parse call sites through it.
- Added independent stateful content/thinking normalizers. Possible marker prefixes are buffered across deltas, known complete control markers are removed, unrelated XML and non-hex tags pass through, and EOF leftovers are classified without leaking partial known markers.
- Complete matching `<tool_calls:suffix>` wrappers recover tagged calls with typed JSON and multiline arguments. Recovered calls use `hy4-tag-N` IDs and negative content indexes, preserving native IDs, arguments, order, and non-negative indexes.
- Incomplete outer wrappers and malformed/incomplete argument pairs produce deliberately invalid arguments for the existing `truncated_tool_arguments` gate. Identifiable calls remain paired in history for recovery but never reach a concrete tool implementation.
- Added protocol, resolver, real generic-runtime, agent execution/history, and real app filmstrip/terminal-emulator regressions.

## Validation evidence

- Parser regressions cover the captured marker, a different suffix, every two-delta split boundary, one-byte deltas, content and thinking, typed/multiline arguments, adjacent tagged calls, tagged-before-native and native/tagged/native ordering, incomplete wrappers, malformed argument pairs, unrelated XML, non-hex tags, DeepSeek fields, non-Hy4 models/APIs, legacy parsing, and explicit `none`.
- `go test -count=1 -race -timeout 120s ./internal/agentic/provider/schema ./internal/agentic/provider/protocol ./internal/agentic/provider ./internal/agentic ./internal/app` passed. Existing generic auto-heal tests ran in these suites and remained green.
- Real protocol → agent → app event replay produced clean thinking/content, exactly one successful recovered `read` widget, and exactly one result. Every filmstrip frame plus terminal visible rows and native scrollback were inspected by `TestAgentTranscript_Hy4TaggedCallFilmstripAndTerminalStayClean`.
- Real PTY replay used the built Goa binary and `e2e/ptydrive` against a throwaway OpenAI-compatible server, with all Hy4 bytes split into 3-byte SSE deltas. `/tmp/goa-hy4-validate/pty.raw` contained no Hy4 marker or suffix. The stripped terminal contained `PTY tagged call recovered.`, the successful bash widget, and `PTY final response complete.`; the saved session contained one `hy4-tag-1` call/start/result and no control bytes. `hy4-pty-artifact.txt` contained exactly one `PTY-HY4-OK` line after two model rounds.
- Live `opencode-go` / `hy4-flash-free` headless tool turn exited 0 and returned `LIVE-HY4-DONE`. The provider emitted a native streaming bash call; output and saved history had no tagged markers, history had one call/start/result with valid JSON, and `live-hy4-artifact.txt` contained exactly one `LIVE-HY4-OK` line. This validates the unchanged native path alongside the tagged PTY replay.
- `go vet ./...` passed; `staticcheck ./...` passed.
- `gocognit -over 15 .` reported only six unchanged baseline functions (`provider/TestResolveStartPlanAPIKey`, `tools/TestAgentMCPCloseRemovesExtractedArtifacts`, `app/TestMakeToolFactory_BuildsEveryConfigurableTool`, `core/(*AgentManager).executeRunner`, `config/TestConfigurableTools_AllDefaultsMirrorEmbeddedConfig`, and `app/initGoalSystem`). No changed/new function remained above budget.
- `gocyclo -over 12 .` reported only unchanged baseline `agentic/alignOneMessage` in `agent_history_appendonly_test.go`.
- Final `go test -count=1 -race -cover -timeout 120s ./...` passed across the repository. An earlier full run had one unrelated transient `internal/filefind/TestComplete_FDRescueBounded` failure; that test passed standalone and the complete rerun passed without any filefind changes.
- Replay server PID 4279 and port 18097 were stopped and verified closed. PTY Goa and driver processes exited; the live headless process exited normally.
