# Task 7 — Quota preserve-on-absent wording + regression pin

**Phase:** 7 (Quota resilience) · **Size:** small · **Depends on:** none

## Objective

Align Goa's quota-merge semantics with the verified Codex behavior: preserve prior
credits/plan/limit fields **only when the new snapshot omits them** (sparse/absent), and treat
an explicit authoritative zero/exhausted as a real state change — and pin that with a
regression test. Phase 7 is already implemented; this task corrects the prose-level
"preserve on transient zero" mischaracterization and locks the behavior.

## Context to load (only these)

- `_shared-context.md` §1 (quota), §3 (invariant 4)
- `plugins/bundled/provider-quota/fetchers/codex.js` (the quota fetcher)
- Existing quota tests: `plugins/quota_fetchers_test.go` (and any quota plugin tests)
- Codex ref (read-only): `../codex/codex-rs/core/src/state/session.rs` — `merge_rate_limit_fields` (338–358)

## Design constraints

- Preserve-on-**absent**, not preserve-on-**zero**. An explicit authoritative zero must NOT be masked.
- Default `limit_id` to `"codex"` when missing (Codex behavior).
- No change to refresh scheduling/coalescing — already covered.

## Steps

1. Read the current codex quota merge logic and its tests.
2. If behavior already preserves-on-absent: add a regression test proving an explicit authoritative zero replaces the old value (this is the gap the plan flagged). If behavior preserves-on-zero, fix it to preserve-on-absent.
3. Confirm `limit_id` defaults to `"codex"` when missing.
4. Update any user-facing docs/comments that say "preserve on transient zero" to "preserve on absent/sparse".

## Tests

- Sparse snapshot (fields omitted) → prior positive values preserved.
- Authoritative zero/exhausted → old value **replaced** (regression pin).
- Missing `limit_id` → defaults to `"codex"`.
- Existing window mapping / refresh coalescing tests still pass.

## Verify

```bash
go test ./plugins/... -run 'Quota|RateLimit|Codex' -count=1 -race
```

## Completion criterion

Quota merge preserves prior fields only on absent/sparse data, treats explicit authoritative zero as a real state change (pinned by a regression test), defaults `limit_id` to `codex`, and the quota tests pass with `-race`.

## Handover

```text
State: quota merge = preserve-on-absent; explicit authoritative zero replaces prior value (regression
test pinned); limit_id defaults to "codex". Tests <names> pass.
Decisions: preserve-on-absent only; do not mask explicit zero; scheduling/coalescing untouched.
Next steps: none — phase 7 closed.
Risks: confirm the fetcher's field-mapping for "absent" vs "explicit zero" against real Codex payload
shapes; some backends may omit vs zero inconsistently.
```
