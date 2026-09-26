# Provider 400: tool arguments must be valid JSON — FIXED, archived 2026-09-26

Archived from `bugs.md` per guideline 4. Original entry logged in commit
`558b88d`; fixed in commit `eb2a210`.

---

## Provider 400: tool arguments must be valid JSON

Observed: `Error: 400 - Error from provider (Console Go): Upstream request failed:
[invalid_request_error] arguments must be valid JSON - /Users/muaddib/dev/goa/.goa/exports/goa-export-20260907-090335.zip`
Expected: tool call arguments containing a filesystem path (zip export path) are sent as
valid JSON (properly escaped/quoted); no provider 400. Investigate argument
serialization for the tool call carrying the export path (likely missing JSON
escaping or raw string interpolation) and add a regression test.

### Root cause

`convertResponsesAssistant` (`internal/agentic/provider/protocol/openai_responses.go`)
replayed `b.ToolArguments` **verbatim** into the `function_call` item when
serializing assistant history for the `/responses` wire format. Every other
request builder (chat completions, anthropic, google, bedrock, mistral) already
routed arguments through `SafeToolArguments`; the `/responses` builder — shared
by the `openai`, `codex` and `azure` protocol variants — was the one hole.

The malformed arguments in the reported session were a **bare filesystem
path** (the provider echoed the offending value back after the dash). A bare
path is not truncated JSON — it cannot be repaired — but as long as it is
re-sent raw it fails the provider's JSON validation, so ONE malformed
historical tool call poisoned every subsequent request: a permanent,
session-ending 400 exactly as the sanitizer's doc comment warns.

### Fix (commit `eb2a210`)

`convertResponsesAssistant` now serializes `"arguments":
schema.SafeToolArguments(b.ToolArguments)`:
- valid JSON passes through unchanged,
- truncated JSON is repaired (model intent preserved),
- unrecoverable shapes (bare path, garbage) degrade to `"{}"` — valid JSON.

The tool-execution side already reports the parse error to the model in the
tool result, so degrading the replayed arguments loses nothing.

### Regression tests (`internal/agentic/provider/protocol/toolargs_sanitize_test.go`)

Written test-first; all three failed on `main` before the one-line fix:
- `TestConvertResponsesAssistant_SanitizesMalformedToolArguments` — truncated
  arguments on the `/responses` wire are repaired with intent preserved
  (`path` / `new_string` survive).
- `TestConvertResponsesAssistant_BarePathArgumentsDegradeToValidJSON` — the
  reported failure shape (arguments = the zip path verbatim) degrades to valid
  JSON instead of poisoning the session.
- `TestBuildResponsesRequest_MalformedHistoricalToolCallDoesNotPoisonRequest`
  — full `ForAPI(schema.ApiOpenAIResponses).BuildRequest` with a poisoned
  history (bare-path call + truncated call, each followed by its error tool
  result): every `function_call` item in the marshaled body carries
  valid-JSON arguments.

### Validation

Gates run separately, post-change: `go vet ./...` exit 0 · `staticcheck ./...`
exit 0 · `gocognit -over 15 .` exit 0 (no output) · `gocyclo -over 12 .` exit 0
(no output) · `go test -count=1 -race -cover ./...` exit 0, explicitly counted
0 FAIL lines. Provider packages all green (protocol, openai, openai_responses,
schema included).
