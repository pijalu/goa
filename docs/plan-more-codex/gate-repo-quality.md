# Task gate — Repository-wide quality gate completion

**Phase:** cross-cutting (from the remediation checkpoint) · **Size:** large · **Depends on:** none directly, but schedule last so it validates the new phases too

## Objective

Finish the repository-wide gate that the 2026-08-17 remediation checkpoint left incomplete:
clear the hard file-size violations, reduce gocognit/gocyclo findings to budget, and run the
full race/vet/cover gate. Do **not** weaken checks or suppress findings to make it pass.

## Context to load (only these)

- The remediation checkpoint numbers in the main `docs/plan-more-codex.md` index.
- Project `.golangci`/complexity config if present; otherwise use the budgets below.
- The offending files/functions as reported by the tools (discover at runtime).

## Constraints

- **Never fix a failure by removing functionality or weakening a check** — refactor the code to genuinely reduce complexity/size.
- Prefer behavior-preserving structural splits (extract small, single-responsibility helpers) — consistent with the SOLID / low-complexity rules.
- Keep every test green throughout; a refactor that breaks a test is a bug in the refactor, not the test.

## Budgets (do not exceed)

- `gocognit -over 15` → 0 findings
- `gocyclo -over 12` → 0 findings
- Hard file-size violations → 0
- `go vet ./...` → clean

## Steps

1. Enumerate current violations: run `gocognit`, `gocyclo`, and the file-size check; list worst-first.
2. Refactor the highest-complexity / oversized files first via structural splits; run focused tests after each.
3. Re-run the complexity/size checks until at budget.
4. Run the full gate (below).

## Verify

```bash
go vet ./... && \
go test ./... -count=1 -race -cover && \
staticcheck ./... && \
test -z "$(gocognit -over 15 . | grep -v '_test.go')" && \
test -z "$(gocyclo -over 12 . | grep -v '_test.go')"
```

## Completion criterion

`go vet`, the full `-race -cover` suite, `staticcheck`, and the complexity budgets (gocognit ≤15, gocyclo ≤12, zero hard file-size violations) all pass repo-wide, with no check weakened and no functionality removed.

## Handover

```text
State: repo-wide gate green — vet, -race -cover suite, staticcheck, gocognit≤15, gocyclo≤12, zero
hard file-size violations. No checks weakened; no features removed.
Decisions: complexity reduced via behavior-preserving structural splits, not suppression.
Next steps: none — plan + gate complete.
Risks: large refactors risk subtle behavior drift; rely on the -race suite and per-file focused tests
after each split.
```
