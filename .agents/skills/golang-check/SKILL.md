---
name: golang-check
description: Run Go static analysis and complexity checks — cognitive complexity (gocognit), cyclomatic complexity (gocyclo), and staticcheck (go vet on steroids). Use before committing, after large refactors, or when CI fails lint/analysis checks.
---

# Go Static Analysis & Complexity Checks

Run `gocognit`, `gocyclo`, `staticcheck`, and the file-size check on the Go codebase. Fix root causes; never bypass a check with `//nolint`, threshold changes, or restructuring just to dodge the tool.

## Quick Start

```bash
cd "$(dirname "$(go env GOMOD)")" 2>/dev/null || cd "$(pwd)"
.agents/skills/golang-check/golang-check.sh
```

The script runs:
- `gocognit -over 15 ./...`
- `gocyclo -over 12 ./...`
- `staticcheck ./...`
- `go-file-size-check.sh` (hard max 1000 lines, soft target 500)

## Fix Rules

1. Address the root cause; do not silence the tool.
2. Keep functions small and focused; extract helpers and use early returns when over threshold.
3. Apply SOLID principles: single responsibility, open/closed, dependency inversion.

## Severity Tiers

| Tier | Examples | Priority |
|------|----------|----------|
| 🔴 Build Blocker | Import cycles, compile errors | First |
| 🟠 Correctness | SA4006, SA4011, SA1019 | Second |
| 🟡 Dead Code | U1000 unused symbols | Third |
| 🔵 Complexity | gocognit/gocyclo over threshold | Fourth |
| ⚪ Style | ST1005, S1xxx | Last |

## Workflow

1. Run the script and save raw output.
2. For each staticcheck code, run `staticcheck -explain <code>`.
3. Categorize each finding by severity tier.
4. Produce a consolidated markdown fix plan with columns: File, Check, Explanation, Fix.
5. Wait for user confirmation before applying fixes.
6. Fix in priority order, re-running the relevant check after each fix.

## Troubleshooting

- `gocognit: command not found` → `go install github.com/uudashr/gocognit/cmd/gocognit@latest`
- `gocyclo: command not found` → `go install github.com/fzipp/gocyclo/cmd/gocyclo@latest`
- `staticcheck: command not found` → `go install honnef.co/go/tools/cmd/staticcheck@latest`
