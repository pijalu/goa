# Task prereq — golang-check cleanup: functional file splits + gocyclo zero

**Phase:** cross-cutting prerequisite · **Size:** large · **Depends on:** none — **schedule FIRST, before every other micro-task**

## Objective

Fix all golang-check issues in the two structural categories before any parity micro-task starts:

1. **Hard file-size violations → 0.** Split every file over the hard limit (1000 lines) **per
   functional subgroups** — cohesive type/function clusters with a single responsibility — not by
   mechanical line-count chops.
2. **`gocyclo -over 12` → 0 findings** (non-test code, same filter as the gate verify).

Rationale for running first: the oversized / high-cyclo files are exactly the files the 6b.*, 2b.*,
and 6c tasks need to modify. Landing the splits first keeps every later diff small and reviewable
and avoids re-basing structural splits on top of new parity code.

## Context to load (only these)

- The remediation checkpoint numbers in the main `docs/plan-more-codex.md` index.
- `.agents/skills/golang-check/SKILL.md` for tool invocation, fix rules, and severity tiers.
- The offending files/functions as reported by the tools (discover at runtime, worst-first).

## Constraints

- **Never fix a failure by removing functionality or weakening a check** — no `//nolint`, no
  threshold changes, no restructuring just to dodge the tool.
- Splits are per **functional subgroup**: each new file gets one cohesive responsibility (e.g.
  transport vs. session state vs. diagnostics); package API and behavior unchanged.
- Move/adapt each test alongside its code (same functional subgroup); keep every test green
  throughout — a refactor that breaks a test is a bug in the refactor.
- Behavior-preserving: public APIs, wire formats, and cache-identity semantics unchanged (Hard
  Rule 7 and the plan invariants apply).
- Do **not** introduce new `gocognit -over 15` or `staticcheck` findings vs. the checkpoint
  baseline; the existing gocognit/staticcheck backlog stays owned by the `gate` task.

## Budgets (do not exceed)

- Hard file-size violations (>1000 lines) → **0**; land on the 500-line soft target where a split
  falls there naturally.
- `gocyclo -over 12` → **0** findings (excluding `_test.go`, matching the gate filter).
- Zero new `gocognit -over 15` / `staticcheck` findings.

## Steps

1. Enumerate violations: run the golang-check script; list hard-size files and gocyclo functions
   worst-first.
2. Per oversized file: identify functional subgroups, split into one file per subgroup, move the
   matching tests, run focused package tests.
3. Per gocyclo finding: extract helpers / use early returns to genuinely reduce branching.
4. Re-run both checks until at zero, then run the full verify below.

## Verify

```bash
go vet ./... && \
go test ./... -count=1 -race && \
test -z "$(gocyclo -over 12 . | grep -v '_test.go')" && \
.agents/skills/golang-check/go-file-size-check.sh
```

## Completion criterion

Zero hard file-size violations and zero `gocyclo -over 12` findings repo-wide, full `-race` suite
green, `go vet` clean, no check weakened, no functionality removed, and no new gocognit/staticcheck
findings vs. baseline.

## Handover

```text
State: zero hard file-size violations (splits done per functional subgroup), gocyclo -over 12 at
zero, vet clean, full -race suite green. gocognit/staticcheck backlog untouched — still owned by
the gate task.
Decisions: split boundaries chosen by functional cohesion; package APIs unchanged; tests moved with
their code.
Next steps: schedule the parity micro-tasks (6b.1 → 6b.2 → 6b.3, 6c, 2b.1 → 2b.2 → 2b.3, 7), then
the gate task last to re-validate everything including the new code.
Risks: large splits risk subtle behavior drift; rely on the -race suite and per-package focused
tests after each split.
```
