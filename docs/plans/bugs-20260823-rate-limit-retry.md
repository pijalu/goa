# Fix plan — 429 retry policy and `/config` exposure

Source: `bugs.md` §3 (2026-08-23).

## Objective

Rate-limit stream retries use Fibonacci delays (`1s, 1s, 2s, 3s, 5s, ...`) capped at five minutes by default. A valid `Retry-After` remains authoritative when within the cap. Global execution retries default to five, and `/config` can display, edit, and persist the existing global retry budget and provider backoff cap without introducing parallel settings.

## Implementation

1. Change retry defaults and backoff calculation in `internal/agentic`: use a capped five-minute default and Fibonacci local schedule for rate-limit failures, while preserving policy-specific overrides and jitter semantics where explicitly configured. Implemented and covered by retry classification tests.
2. Change embedded/default config `execution.retries` to 5 and ensure config-to-agentic conversion supplies the five-minute global cap through existing `execution.retries` and provider `max_retry_delay`/`retry_policy.backoff.max_ms` fields. Embedded default and provider catalog defaults now use 5 retries and five minutes.
3. Add `/config` key setters and completions for `execution.retries` and the existing provider retry settings (provider selection syntax as supported by config CLI), with cascade persistence through existing saver APIs. Added `execution.retries` and `providers.<id>.max_retry_delay`; both use existing persistence.
4. Add table-driven tests for Fibonacci sequence/cap/Retry-After, default loading and conversion, and `/config:set` persistence and completion.
5. Run required gates separately: vet, staticcheck, cognitive/cyclomatic checks, race/coverage tests. Archive this bug and commit only after interactive/config and test validation.

## Acceptance

No rate-limit retry delay exceeds five minutes by default; default sequence is Fibonacci; accepted Retry-After wins. `/config` exposes editable persistent retry settings using existing YAML keys, and all tests/gates pass.
