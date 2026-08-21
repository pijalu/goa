# Task 6c — `x-codex-turn-state` sticky routing

**Phase:** 6c · **Size:** small · **Depends on:** none (complements 6b; can run before or after)

## Objective

Capture the server-issued `x-codex-turn-state` token at the start of a Codex turn and replay
it on every subsequent request within that turn — on SSE (header) and WS (`client_metadata`)
— so Codex can pin the session to a warm backend. Without it Goa's session affinity relies
only on `prompt_cache_key`, which is weaker than Codex's own routing.

## Context to load (only these)

- `_shared-context.md` §1 (turn-state), §2 (Codex request body build, WS transport), §3 (invariant 4 — redaction)
- `internal/agentic/provider/openai_responses/provider.go` (SSE send + response header handling)
- `internal/agentic/provider/openai_responses/websocket.go` (WS headers / client_metadata)
- Codex ref (read-only): `../codex/codex-rs/core/src/client.rs` — `X_CODEX_TURN_STATE_HEADER` (145), turn-state capture (≈268–281), replay (≈1624–1626)

## Design constraints

- Token is **turn-scoped**: captured on the first response of a turn, replayed only within that turn, discarded at turn end.
- **Never leaked** across turns or sessions; concurrent sessions must not share it.
- **Redacted from diagnostics** (invariant 4) — treat like a session secret; hash if it ever appears in forensics.
- No behavior change when the server does not send the header (absent → nothing to replay).

## Steps

1. Read `x-codex-turn-state` from the response headers at turn start for the Codex flavor; store it on the per-session provider state (or turn context).
2. Replay it as a request header on subsequent Codex SSE requests in the same turn.
3. On the WS path, add it to `client_metadata` (mirror Codex) for subsequent requests in the same turn.
4. Clear the stored token at turn boundary; ensure a new turn starts with no stale token.
5. Ensure concurrent sessions each hold their own token (no shared mutable global).

## Tests

- Fake server emits `x-codex-turn-state` on turn-start response; assert it is replayed on later requests of that turn.
- Assert it is absent on the very first request (nothing captured yet).
- Assert it is not present in the next turn or in another concurrent session.
- Assert it never appears in logs/forensic artifacts (redaction check).

## Verify

```bash
go test ./internal/agentic/provider/openai_responses/... -run 'TurnState|Sticky' -count=1 -race && go vet ./internal/agentic/...
```

## Completion criterion

Codex flavor captures `x-codex-turn-state` at turn start and replays it within the turn on both SSE (header) and WS (`client_metadata`), scopes it strictly to its turn/session, redacts it from diagnostics, and the turn-state tests + vet pass with `-race`.

## Handover

```text
State: x-codex-turn-state captured at Codex turn start and replayed within the turn on SSE
(header) and WS (client_metadata); turn/session-scoped; redacted. Tests <names> pass with -race.
Decisions: token is turn-scoped, per-session, treated as a secret in diagnostics; absent header =
no replay.
Next steps: none required for 6b/2b ordering; independent. If 6b.3 lands after, ensure its WS
client_metadata carries the same turn-state.
Risks: confirm exact header casing/name against a live Codex response; the WS field name in
client_metadata must match what the backend reads.
```
