# Model returns an error after force stop (OpenAI Responses flavors)

**Status:** fixed & validated (2026-09-02).

## Report

**Observed:** After a force stop (Esc) — which triggers the recovery stream —
or when the per-turn tool-call round limit fires, the model call fails with:

```
Error 400 - Error from provider (Console)  Upstream request failed
[invalid_request_error] only "auto" is supported for tool_choice. "none",
"required", and named function choices are not currently supported
```

The recovery/turn-summary round never runs; the user sees the raw provider
error instead of a final answer.

**Root cause:** the final-step text-only collapse (P7) sets `Context.NoTools`,
and `applyResponsesToolFields` in
`internal/agentic/provider/protocol/openai_responses.go` translated that into
`tool_choice: "none"` (dropping `parallel_tool_calls`). Strict Responses
upstreams — opencode Zen / muse ("Console") — accept only `"auto"` for
`tool_choice` and hard-400 on `"none"`. The same collapse on the
chat-completions, Mistral, and Anthropic flavors is fine because those
upstreams accept `"none"`.

**Expected:** the collapse round must produce a request the upstream accepts
while keeping the text-only semantics: omit the `tools` array and omit
`tool_choice` entirely (a request carrying no tools cannot yield tool calls,
so `"none"` is redundant), and keep dropping `parallel_tool_calls`.

## Fix plan & test approach

1. Failing test in
   `internal/agentic/provider/protocol/openai_responses_collapse_test.go`:
   build a Responses body with `NoTools: true` (plain + codex flavors) and
   assert the body has no `tool_choice` key, no `tools` key, and no
   `parallel_tool_calls` key.
2. Change `applyResponsesToolFields` to `delete(body, "tool_choice")` instead
   of setting `"none"` on the NoTools branch (single-line behavior change).
3. Update the two tests that pinned the old contract:
   `TestNoTools_CollapseResponses` (no_tools_test.go) and
   `TestCodexBodyNoToolsCollapse` (openai_codex_responses_test.go) now assert
   `tool_choice` is absent. Other flavors (completions, Mistral, Anthropic)
   keep sending `"none"` — their upstreams accept it.
4. Validate with the five quality gates run separately (bugs.md guideline 6).

## Validation

- RED confirmed: `TestResponsesNoToolsCollapseOmitsToolChoice/plain|codex`
  failed on pre-fix code (`tool_choice: "none"` present in the body).
- `go vet ./...` — clean.
- `staticcheck ./...` — clean (exit 0).
- `gocognit -over 15 .` — clean.
- `gocyclo -over 12 .` — clean.
- `go test -count=1 -race -cover ./...` — all packages pass, exit 0
  (`internal/agentic/provider/protocol` coverage 76.8%).
- The Codex WebSocket incremental-reuse fingerprint
  (`requestFingerprint.ToolChoice`, `ws_incremental.go`) reads `tool_choice`
  generically; an absent key yields `""`, which still differs from `"auto"`,
  so a collapse round continues to force a full (non-incremental) send —
  behavior preserved, covered by `TestRequestPropertiesMatchExhaustive`.
