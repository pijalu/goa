<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Feature: z.ai Coding Plan quota reset API — status / opportunity / use (FEATURE)

Date: 2026-09-30 · Status: SHIPPED — implemented, contract tests proven RED
against a reverted implementation, gates clean.

## Reported

> goa fetches only quota consumption
> (`GET {origin}/api/monitor/usage/quota/limit`). ZCode additionally implements
> the Coding Plan **reset** surface: `/api/v1/coding-plan/reset/{status,opportunity,use,history/read}`.
>
> Contract details to preserve:
> - envelope `{code,msg,data}`; **`code 3301` = throttled** and its `next_try_at`
>   must pass through verbatim or the client re-polls into the limit;
> - bare HTTP 429 maps to the same cooldown;
> - `idempotency_key` mandatory, ≤64 chars, on both mutating calls;
> - dual auth: `zcodejwttoken` (Bearer) **plus** family OAuth token
>   (`oauth:zai:access_token`) sent raw, **no** cross-family fallback.
>
> **Expected.** `/quota` surfaces available 5-hour / week resets, and the client
> respects the server's retry boundary.

Evidence: `docs/research/zai-connection-review-20260930.md` §4 (ZCode source:
`packages/services/src/usage-stats/providers/bigmodelUsageQuotaProvider.ts`).

## Fix

**New fetcher** `plugins/bundled/provider-quota/fetchers/zai-coding-plan.js`,
registered in `plugin.js` as the reset surface of the zai fetcher
(`registerResetSurface("zai", …)`) and required by `fetchers/zai.js`, which
rides the status along with every successful monitor fetch (the require cache
hands both the same instance, so a cooldown set by one is honored by the other).

| Endpoint | Method | Function |
| --- | --- | --- |
| `/api/v1/coding-plan/reset/status` | GET | `status(ctx)` |
| `/api/v1/coding-plan/reset/opportunity` | POST | `requestOpportunity(ctx, key)` |
| `/api/v1/coding-plan/reset/use` | POST | `useReset(ctx, type, key)` |
| `/api/v1/coding-plan/reset/history/read` | POST | `markHistoryRead(ctx)` |

Contract handling:

- **Envelope** `{code,msg,data}`: `code 0` unwraps, `3301` is the throttle,
  any other code surfaces as `coding_plan_reset_api_error:<code>` (never a
  guess).
- **3301** returns `nextTryAt` **verbatim** and persists an enforced
  `cooldownUntil = max(next_try_at, now + 5min)` (ZCode's floor). While the
  boundary is live `status()` answers from storage — the 60s scheduler and
  every quota refresh stop hitting the endpoint, so a 30s poll cannot hammer
  it.
- **HTTP 429** (bare, no envelope — the backend answers that way when the call
  races the grant lock) maps to the same cooldown state with a 10-minute
  fallback boundary.
- **`idempotency_key`** is validated locally: mandatory and ≤64 chars on both
  mutating calls. An invalid key fails WITHOUT sending a request. An omitted
  key mints a UUIDv4 (`lib/ids.js`, shared with the Codex redeem id); the
  `use` key is retained across retries and cleared only on a terminal outcome,
  so a retry re-sends the identical request (the server dedupes a
  double-consume).
- **Dual auth**: `Authorization: Bearer <zcodejwttoken>` (config `zcodeJwt`, or
  plugin storage) **plus** `X-Bigmodel-Authorization: <family OAuth token>` sent
  RAW. The family follows the account's endpoint (`open.bigmodel.cn` → bigmodel,
  else zai) and there is **no cross-family fallback**: the other family's token
  is a different identity, and the inference API key never stands in. Missing
  credentials fail as `auth_required` with a specific reason
  (`coding_plan_reset_zcode_jwt_required` / `…_maas_jwt_required`).
  `history/read` omits the `Bigmodel-Target-Type` scope (the read cursor is per
  user, not per scope).

**Surfacing** (plugin.js):

- `/quota:resets` — Codex section (unchanged) plus a z.ai Coding Plan section:
  available 5-hour / week counts, earliest expiry, last-used history, the
  unread marker and the throttle boundary when throttled.
- `/quota:resets:read` — clears the unread-history marker (POST off the command
  path on a timer, like every other provider call).
- `/quota` — a `Coding Plan Resets` row on the z.ai provider line.
- Status-bar segment — `[38%|62%] +3↻` when resets are available.
- `/quota:reset` routes by ACTIVE provider: with z.ai active it spends a Coding
  Plan reset (5-hour by default, `:week` for the weekly one, `:claim` to ask the
  server for a new grant) through the shared confirm + single-flight path;
  otherwise the Codex credit flow is unchanged.

## Tests

`plugins/quota_zai_coding_plan_test.go` (quota harness, `plugins/*_test.go`
pattern) covers: status mapping incl. degenerate rows; `/quota:resets` and
`/quota` rendering; the segment count; 3301 verbatim `next_try_at` + persisted
cooldown honored without a second request; HTTP 429 → same cooldown; throttle
surfacing in both commands; idempotency-key validation (empty/oversized on both
mutating calls → zero POSTs); wire contract of opportunity/use/history-read
(URL, body, dual headers, scope); key retention across a failed use and clearing
after a terminal one; and no cross-family credential fallback in either
direction plus the positive bigmodel case.

RED evidence: with the three contract branches reverted (3301 branch, key-length
check, family resolution), `TestQuotaZaiCodingPlan_{Throttle3301,
ThrottledStatus,InvalidIdempotencyKey,NoCrossFamily}` fail — 3301 degrades to
`coding_plan_reset_api_error:3301`, an oversized key reaches the network
(`http_404`), and a bigmodel account silently authenticates with the z.ai
token.

## Validation

- `go build ./...`
- `go vet ./plugins/...`, `staticcheck ./plugins/...` — clean
- `go test -count=1 ./plugins/` and `go test -count=1 -race ./plugins/` — pass
- Recorded verify command (`go build ./... && go test -count=1 ./internal/agentic/...`) — exit 0
