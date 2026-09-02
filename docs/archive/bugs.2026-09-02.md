# Bug and feature Archive — 2026-09-02

Completed entries moved here from bugs.md per guideline 4 (all items below are
fixed/closed; bugs.md holds only the guideline and the open "# To fix" list
from this date on).

## BUG: muse-spark-free on opencode Zen 400s on turn 1 of every session — Goa sends its session ID as `previous_response_id`, which must reference a server-issued `resp_*` object (2026-09-02)

**Observed** (export `.goa/exports/goa-export-20260902-200938.zip`): the very
first request of a session to `https://opencode.ai/zen/v1/responses` with
`muse-spark-1.2-contributor-free` fails with HTTP 400:

```
Error: 400 - {"error":{"param":"previous_response_id","type":"invalid_request_error",
"message":"Error from provider (Console): Upstream request failed: [invalid_request_error]
`previous_response_id` must start with \"resp_\", got \"1788372570_lom323ve\""}}
```

`1788372570_lom323ve` is the Goa session ID (`manifest.json → sessionId`), sent
verbatim as the Responses API `previous_response_id`. agent.log: "stream error
not retryable; surfacing immediately" — the session dies on turn 1.

**Root cause:** `core/agentmanager.go` seeds `StreamOptions.SessionID` with the
Goa session ID at session start, and
`internal/agentic/provider/protocol/openai_responses.go`
`applyResponsesSessionFields` then assigned it to `previous_response_id` on
every plain/Azure Responses request. But `previous_response_id` must reference
a **server-issued response object** (`resp_*`) — a client session ID is invalid
there by contract, and nothing in the SSE path ever captured a `resp_*` id to
chain with (the parser reads `status`/`usage` only). The path only ever
"worked" on upstreams that silently ignored the parameter; the Zen gateway now
validates the format and 400s. Third incident in the same defect class on the
OpenCode gateways: 2026-08-29 rejected encrypted-content + previous_response_id
as a pair (bugs.2026-08-30.md — fixed by gating the include, leaving the bogus
chaining in place), Codex rejects the parameter outright (fixed by a
codex-only carve-out). Each prior fix carved around the root defect.

Reference-client check (`dev/opencode`): opencode's session layer sets
`promptCacheKey = input.sessionID` (`provider/transform.ts`) and **never**
sends `previous_response_id` over SSE
(`packages/llm/src/protocols/openai-responses.ts` — the field only exists as an
explicit caller option, unused by the session layer). Session affinity =
`prompt_cache_key`; that is the whole contract.

**Fix (implemented 2026-09-02):**
- `protocol/openai_responses.go` `applyResponsesSessionFields`: no flavor sends
  `previous_response_id` over SSE anymore; session affinity rides
  `prompt_cache_key` only (all flavors, opencode parity). The `isCodex`
  parameter went away.
- `responsesWantsEncryptedContent`: with nothing chaining server-side, the
  `reasoning.encrypted_content` include now rides **every** flavor by default
  (reasoning continuity must be carried by the stateless full-history replay —
  matching what the Codex flavor already pinned and what opencode sends for
  reasoning models). `CompatFlags.SupportsEncryptedContent` remains the
  per-model escape hatch in both directions.
- `openai_responses/provider.go` (legacy package, unreferenced but carrying the
  same bug — a trap for any future rewiring): same root fix; plain flavor now
  sends `prompt_cache_key` like the live protocol path. The WebSocket
  continuation path (`websocket.go`) still chains with real `resp_*` ids from
  the baseline capture — correct and untouched.
- Comments/docs aligned: `schema/variant.go` (SupportsEncryptedContent),
  `docs/PROVIDER-CACHE.md` (affinity table, §8, cross-refs),
  `skillrunner/runner.go` (Rule-7 comment).

**Tests (RED first):** rewrote
`protocol/openai_responses_encrypted_test.go` — per-flavor absence of
`previous_response_id` with affinity on `prompt_cache_key`; include on all
flavors for reasoning models (with and without session); compat tri-state
override; non-reasoning gating; and the export-shaped regression
(`muse-spark-1.2-contributor-free`, session `1788372570_lom323ve` → no
`previous_response_id`, `prompt_cache_key` carries the session, include
present). Replaced the bug-pinning `TestOpenAIResponsesSendsPreviousResponseID`
with an omission test; updated `protocol_test.go`
`TestOpenAIResponsesPromptCacheKey` and legacy
`TestBuildResponsesBodyPlainSession` to the new contract. All failed RED
against the old code (exact export shape reproduced: body carried
`previous_response_id: "1788372570_lom323ve"`), GREEN after.

**Validation:** `go vet ./...` clean; `go test -count=1 -race -cover ./...`
PASS (0 FAILs; touched packages: protocol 75.5%, legacy openai_responses 78.4%,
schema 71.0%); `staticcheck ./...`, `gocognit -over 15 .`, `gocyclo -over 12 .`
zero findings. Live acceptance still belongs to the reporter's key: one goa
session against `muse-spark-1.2-contributor-free` must complete turn 1 without
the 400.

**Residual notes:** the legacy `internal/agentic/provider/openai_responses`
package is unreferenced (no importers, no init registration; its WS transport
is the parked plan-more-codex work) — deleting it is a separate scope decision.
Real `resp_*` server-side chaining would be a new feature (capture
`response.completed` id per session, omit on first turn) — not needed for
correctness since Goa always replays full history.
