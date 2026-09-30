<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bug: shipped `activity_timeout` too short for reasoning models, and the byte guard raced the event watchdog (MEDIUM)

Date: 2026-09-30 · Status: FIXED — implemented, tests RED-before-fix, gates clean.

## Reported

> **Observed.** `config/user.yaml` pins `activity_timeout: 60s`, but the shipped
> default is `2m` (`provider.DefaultStreamIdleTimeout`). 60s is short for a
> reasoning model — export A request 17 legitimately ran 80.7s. The same key also
> drives *two* racing guards (byte-level `idleTimeoutReader` and the event-level
> watchdog), both wired from `execution.activity_timeout`.
>
> **Expected.** The default tolerates long reasoning turns, and the two guards do
> not race on one budget.

Evidence: `docs/research/zai-connection-review-20260930.md` §2 (request 17: 80.7s
of pure silence before its first token — a reasoning model emits no bytes while
it thinks).

## Root cause

Two independent defects on one code path.

**1. The default was tuned for chatty models.** The byte-idle budget shipped at
`2m` (`internal/agentic/provider/idle_timeout.go`,
`DefaultStreamIdleTimeout`) and the embedded `execution.activity_timeout` pinned
`45s` — below the 80.7s silence a single reasoning turn legitimately produces.
Either value alone killed healthy reasoning turns.

**2. One budget, two owners.** `StreamOptions.IdleTimeout` was passed unchanged
to both guards: the HTTP layer wrapped the body in `idleTimeoutReader` with it
(`provider/runtime.go:184`, `provider/openai/stream.go:40`), and
`Agent.effectiveEventStallTimeout` returned the same value verbatim
(`internal/agentic/agent_streaming.go`). On a completely silent stream both
timers expired on the same wall clock and the scheduler decided which error the
user saw — the byte guard's `ErrStreamIdle`, or the watchdog's `stream stalled`
error that the warn-then-retry path (and the held-open-complete-answer
finalization) depends on. A silent stream could therefore skip the recovery
path entirely, by luck of the race.

## Fix

**One owner per silence window.** `provider.EventStallTimeout(byteBudget)`
(`internal/agentic/provider/idle_timeout.go`) is the single source of the split:
the event watchdog gets `3/4` of the byte budget, the byte reader keeps all of
it as the outer backstop. `Agent.effectiveEventStallTimeout` now delegates to it
instead of echoing `opts.IdleTimeout`, so for any silence interval

```
warn lead  <  event stall (3/4)  <  byte budget (full)
```

The watchdog owns the window because it is the guard with recovery semantics; the
byte guard remains a strict backstop for a socket that delivers nothing at all,
and it can no longer pre-empt the watchdog.

The same function is reused by the config layer
(`config.ActivityPairViolation` → "is not shorter than the 3m45s stall window")
and by the `/config` menu labels, so a warning lead is checked and displayed
against the deadline the agent actually uses — a lead between the event stall and
the byte budget could never fire and is refused rather than silently ignored.

The WebSocket transport carried a *third* copy of the race: its message-idle
backstop sat at a fixed `wsStreamIdleTimeout` (2m) that `selectTransport` never
synchronized with the byte budget, so on a WS stream the backstop would have
expired before the 3m45s watchdog. `selectTransport` now resolves the same byte
budget the SSE reader uses and hands it to `WebSocketTransport.IdleTimeout`
(`internal/agentic/provider/runtime.go`), so the split holds on both transports.

**Defaults raised above a long reasoning turn.** `DefaultStreamIdleTimeout`
`2m → 5m`; embedded `execution.activity_timeout` `45s → 5m`,
`activity_warn_after` `30s → 3m20s` (two thirds of the budget, and strictly
inside the 3m45s event stall). Both keys stay configurable, live-editable, and a
per-provider `providers[].idle_timeout` still overrides the budget for one
provider.

**Documented** in `docs/CONFIGURATION.md` § "Stall Timing (provider silence)":
the byte-budget vs event-stall distinction, the reasoning-model trade-off, the
guard table, and tuning examples. `docs/LIMITS.md` no longer claims
`activity_timeout` is an unconsumed legacy key.

## Tests

- `internal/agentic/provider/idle_timeout_guards_test.go` (new)
  - `TestEventStallTimeout_AlwaysStrictlyInsideByteBudget` — 5 budgets, the
    split is strictly inside and positive.
  - `TestEventStallTimeout_UnsetBudgetUsesProviderDefault` — the fallback matches
    the reader's own default.
  - `TestEventStallTimeout_OutlastsALongReasoningTurn` — the shipped stall keeps
    ≥2x margin over the observed 80.7s reasoning silence.
  - `TestEventStallTimeout_BackstopFiresFirst` — measured, not derived: with a
    byte guard armed on a blocking reader, the reader is still waiting when the
    event stall elapses, and only reports `ErrStreamIdle` after the full budget.
  - `TestWebSocketBackstop_UsesTheByteBudget` — the WS transport carries the same
    byte budget (default 5m, or the configured one) and its backstop stays
    strictly outside the event stall.
- `internal/agentic/agent_stall_guards_test.go` (new)
  - `TestStallGuards_SingleOwnerOrdering` — warn < event stall < byte budget for
    the shipped pair, the provider-default fallback, and a user-pinned window.
  - `TestStallGuards_SilentStreamOwnedByEventWatchdog` — a fully silent stream
    ends the turn with the watchdog's stall error (and the retry path engaged),
    never with the byte guard's idle-timeout error.
- `config/stall_timing_test.go` — shipped pair pinned at `5m` / `3m20s`, the lead
  verified against the event stall, and the ≥2x reasoning-silence margin.
- `config/bare_stall_duration_test.go`, `config/loader_report_test.go`,
  `config/write_guard_test.go` — fixture pairs moved off the now-contradictory
  `60s/45s` (45s is no longer inside 60s's 45s event stall) to legal pairs
  (`60s/30s`, `40s/40s`), and default-value assertions re-pinned to `5m`.
- `core/commands/config_stall_timing_test.go` — menu labels now report the event
  stall the agent retries on (`225 (warn at 200)`), the stale-lead drop is
  exercised with a lead that fits the byte budget but not the event stall
  (`60s/40s → 50s`), and the refusal boundary is `3m45s`.

## RED-before-fix evidence

With `EventStallTimeout` reverted to returning the byte budget verbatim (the
pre-fix shared budget), keeping the new tests:

- `TestStallGuards_SingleOwnerOrdering` FAIL — `event stall = 5m0s, want 3m45s`,
  and "the event stall must fire strictly before the byte guard".
- `TestEventStallTimeout_AlwaysStrictlyInsideByteBudget` FAIL on all five budgets.
- `TestEventStallTimeout_UnsetBudgetUsesProviderDefault` FAIL.
- `TestEventStallTimeout_BackstopFiresFirst` FAIL — `byte guard fired at the
  400ms event stall (err=stream idle timeout …)`: the pre-fix race, observed.

The shipped-default assertions in `config/stall_timing_test.go` and the core
menu tests failed against the old `45s` / `2m` values before the source change.

## Validation

- `go build ./...` — clean.
- `go vet ./...`, `staticcheck ./...` — clean.
- `gocognit -over 15`, `gocyclo -over 12` on the touched packages — clean.
- `go test -count=1 ./internal/agentic/... ./config/... ./core/... ./provider/...
  ./internal/app/...` — pass.

## Residual risk

- A genuinely dead connection now costs up to 5 minutes before the retry (3m45s
  for the watchdog, 5m for the byte backstop) instead of 45s. The stall warning
  at 3m20s is the user-visible signal; users who want the old speed should pin
  `execution.activity_timeout` (the docs carry tuning examples).
- The `3/4` split is a fixed fraction, not adaptive: a provider that emits
  frequent unmapped events (which deliberately do not re-arm the watchdog) is
  still judged on the event stall, not on the byte budget. That is the intended
  semantics — unmapped events are invisible to the user.
- The same `execution.activity_timeout` key also reaches non-agent paths that
  read the byte budget only (compaction requests derive their timeout from
  `DefaultStreamIdleTimeout`); those inherit the longer window and were not
  retuned separately.
- The WebSocket message-idle backstop moves from its own fixed 2m to the shared
  byte budget (5m by default). A half-open WS connection therefore takes longer
  to be reclaimed by the transport; the agent's watchdog still ends the turn at
  3m45s.
