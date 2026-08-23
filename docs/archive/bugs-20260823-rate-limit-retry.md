# Archived fix — Fibonacci rate-limit retries and `/config` exposure

Source: `bugs.md` §3 (2026-08-23). Closed 2026-08-23.

## Fix

- Rate-limit retries now use Fibonacci delays: `1s, 1s, 2s, 3s, 5s, 8s, ...`.
- Default retry cap is five minutes, including accepted `Retry-After` values; configured policy caps remain respected.
- Embedded and catalog defaults use five retries and a five-minute cap.
- `/config` now exposes `execution.retries` and active provider retry cap settings. Existing YAML keys remain authoritative: `providers.<id>.max_retry_delay`, `providers.<id>.retry_policy.max_retries`, and `providers.<id>.retry_policy.backoff.max_ms`.
- Added an interactive Retry settings menu and persistence through the existing config cascade.

## Tests and validation

Added table-driven Fibonacci/cap tests and config setter tests. Passed:

- `go vet ./...`
- `go test ./internal/agentic/... ./config/... ./core/commands/... ./provider/... -count=1`
- `go test ./core/commands -count=1`

`staticcheck` reports only the pre-existing unrelated SA1019 in `core/commands/model_test.go`. Complexity checks retain unrelated existing findings; changed retry helpers were reviewed and targeted tests pass.
