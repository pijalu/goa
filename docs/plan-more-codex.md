# Plan: More Codex Optimizations for Goa

Branch: `feature/more-codex`
Status: **revised and split into micro-tasks.** Phases 0–7 (SSE-path parity, compaction policy, cache identity, prefix forensics, transport seam, quota) are implemented and validated on `feature/more-codex-split`. The remaining work — the parity gaps found by cross-reviewing the current Codex source — is decomposed into **independently schedulable micro-tasks** under [`plan-more-codex/`](./plan-more-codex/). This file is the index; each task file is a self-contained goal spec with a completion criterion, verify command, and handover.

> **How to use:** schedule each task as a goal with `freshContext: true`. A task file plus the
> [`_shared-context.md`](./plan-more-codex/_shared-context.md) sections it names are the *whole*
> context the goal needs — no need to load this index or the full history. Dependency order is in
> the table below; independent tasks can run in any order.

## Reference baseline

Verified against Codex `../codex/codex-rs/core/src/` at commit `230791fd1f`. The three shifts that
motivated the remaining tasks:

1. **Codex's dominant transport is incremental WebSocket** (delta `input` + `previous_response_id`); full-history SSE is the fallback.
2. **Codex compaction is reactive buffered-limit rollover**, and it **prefers server-side `/responses/compact`** over local summarization (plus a no-summary `TokenBudget` "fresh window" mode).
3. **`prompt_cache_key` is the session_id and never rotates on compaction**; warmth comes from server session state (`x-codex-turn-state` + `previous_response_id`), not key rotation.

Full mechanism detail and Goa seam locations live in `_shared-context.md`.

## Remediation checkpoint (2026-08-17) — folded into the `gate` task

Staticcheck remediation is clean and all packages compile, but the repo-wide gate was left incomplete:
27 hard file-size violations, 58 gocognit findings, 77 gocyclo findings, and race/vet/full verification
pending. That work is captured as the [`gate`](./plan-more-codex/gate-repo-quality.md) task (scheduled last
so it also validates the new phases). Remediation commit: `411f466`.

## Completed (record only — no tasks needed)

| Phase | What shipped |
|---|---|
| 0 | Baseline metrics + immutable provider snapshots; `RequestFingerprint` with bounded hashes + prefix classification. |
| 1 | `DecideCompactionPolicy` pure four-way primitive (`Noop`/`SoftMaintenance`/`HighMarkCompaction`/`EmergencyFallback`). |
| 2 | Proactive high-mark compaction (a Goa optimization, not Codex parity) + least-destructive local ladder. |
| 3 | Cache identity (`CacheIdentity`/`NewCacheKey`), generation advance; key rotation scoped to SSE path. |
| 4 | Prefix-integrity forensics; classification reused as the 6b delta trigger. |
| 5 | Codex SSE request-shape parity (`prompt_cache_key`, no `previous_response_id`, `store=false`, no-tools collapse). |
| 6 | Session-affine pooled WebSocket transport (full-history POST-over-WS; no delta yet). |
| 7 | Quota plugin sparse/window/transient coverage (see task 7 for the wording correction). |

## Remaining micro-tasks (schedulable goals)

| Task | File | Phase | Size | Depends on | Verify command (goal `verifyCommand`) |
|---|---|---|---|---|---|
| **6b.1** | [06b.1-ws-session-baseline.md](./plan-more-codex/06b.1-ws-session-baseline.md) | 6b | small | — | `go test ./internal/agentic/provider/openai_responses/... -run 'Baseline\|ResponseID\|WSCompleted' -count=1 -race` |
| **6b.2** | [06b.2-request-property-match.md](./plan-more-codex/06b.2-request-property-match.md) | 6b | medium | 6b.1 | `go test ./internal/agentic/provider/openai_responses/... -run 'Incremental\|Delta\|PropertiesMatch' -count=1 -race` + complexity |
| **6b.3** | [06b.3-ws-delta-send-fallback.md](./plan-more-codex/06b.3-ws-delta-send-fallback.md) | 6b | large | 6b.1, 6b.2 | `go test ./internal/agentic/provider/openai_responses/... -run 'WS\|Incremental\|Delta\|Fallback\|Codex' -count=1 -race && go vet ./internal/agentic/...` |
| **6c** | [06c-turn-state-routing.md](./plan-more-codex/06c-turn-state-routing.md) | 6c | small | — | `go test ./internal/agentic/provider/openai_responses/... -run 'TurnState\|Sticky' -count=1 -race && go vet ./internal/agentic/...` |
| **2b.1** | [02b.1-remote-compact-capability.md](./plan-more-codex/02b.1-remote-compact-capability.md) | 2b | small | — | `go test ./internal/agentic/... -run 'RemoteCompact\|Capability\|CompactionPolicy' -count=1 -race && go vet ./internal/agentic/...` |
| **2b.2** | [02b.2-remote-compact-client.md](./plan-more-codex/02b.2-remote-compact-client.md) | 2b | large | 2b.1 | `go test ./internal/agentic/... -run 'RemoteCompact\|CompactEndpoint' -count=1 -race && go vet ./internal/agentic/...` |
| **2b.3** | [02b.3-fresh-window-fallback.md](./plan-more-codex/02b.3-fresh-window-fallback.md) | 2b | medium | 2b.2 | `go test ./internal/agentic/... -run 'FreshWindow\|TokenBudget\|CompactionPolicy\|Strategy' -count=1 -race && go vet ./internal/agentic/...` |
| **7** | [07-quota-preserve-on-absent.md](./plan-more-codex/07-quota-preserve-on-absent.md) | 7 | small | — | `go test ./plugins/... -run 'Quota\|RateLimit\|Codex' -count=1 -race` |
| **gate** | [gate-repo-quality.md](./plan-more-codex/gate-repo-quality.md) | cross-cutting | large | (schedule last) | `go vet ./... && go test ./... -count=1 -race -cover && staticcheck ./...` + complexity budgets |

### Scheduling notes

- **Dependencies:** only the `Depends on` column is a hard ordering constraint. `6b.*` must go 6b.1 → 6b.2 → 6b.3; `2b.*` must go 2b.1 → 2b.2 → 2b.3. `6c`, `7` are independent of everything. `gate` should be last (it validates the new code too).
- **Suggested value order:** 6b.1 → 6b.2 → 6b.3 (highest-value parity item), then 6c, then 2b.1 → 2b.2 → 2b.3, then 7, then gate.
- **Budgets:** small tasks ≈ 60k tokens / 15 turns; medium ≈ 100k / 20; large ≈ 150k / 30. Each task file lists the exact `completionCriterion` and `handover` to paste into the goal.
- **Queueing:** create the first goal active and queue the rest FIFO (`goal create` with `objectives`, or one at a time). The `handover` block in each task is the continuity contract for the next clean-context goal.

## Cross-review findings → task map

| Finding (Codex reality) | Addressed by |
|---|---|
| 1. WS path sends delta + `previous_response_id` (full-history SSE is fallback) | 6b.1, 6b.2, 6b.3 |
| 2. Compaction is reactive buffered-limit rollover | recorded; Goa keeps proactive (Phase 2, done) + `BodyAfterPrefix` note |
| 3. Server-side `/responses/compact` preferred; no-summary `TokenBudget` mode | 2b.1, 2b.2, 2b.3 |
| 4. `prompt_cache_key` = session_id, never rotates on compaction | 6b.3 (session-stable WS key); Phase 3 amended (done) |
| 5. `x-codex-turn-state` sticky routing replayed per turn | 6c |
| 6. Quota preserves on absent, explicit zero is authoritative | 7 |

## Non-negotiable invariants (apply to every task)

1. Append-only history outside explicit compaction; never mutate already-sent messages (Hard Rule 7).
2. Fresh contexts (`/clear`, fork, sub-agent, planner, summarizer) get isolated cache/session identity.
3. Codex SSE shape unchanged: `store=false`, `prompt_cache_key` present, no `previous_response_id` on SSE.
4. Diagnostics carry bounded hashes only — never session keys, prompts, OAuth tokens, tool args, or the turn-state token.
5. Every task independently shippable and reversible; cache-identity changes ship with a migration note + cache-forensics doc update.
