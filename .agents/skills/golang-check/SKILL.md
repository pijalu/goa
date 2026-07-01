---
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Copyright (C) 2026 Pierre Poissinger
name: golang-check
description: Run Go static analysis and complexity checks — cognitive complexity (gocognit), cyclomatic complexity (gocyclo), and staticcheck (go vet on steroids). Use before committing, after large refactors, or when CI fails lint/analysis checks.
---

# Go Static Analysis & Complexity Checks

Run three complementary Go analysis tools to catch complexity issues, bugs, dead code, and style problems before they reach CI.

## Tools

| Tool | What it catches | Install |
|------|----------------|---------|
| `gocognit` | Functions exceeding cognitive complexity threshold | `go install github.com/uudashr/gocognit/cmd/gocognit@latest` |
| `gocyclo` | Functions exceeding cyclomatic complexity threshold | `go install github.com/fzipp/gocyclo/cmd/gocyclo@latest` |
| `staticcheck` | Bugs, dead code, incorrect usage, performance issues | `go install honnef.co/go/tools/cmd/staticcheck@latest` |
| `go-file-size-check` | Go files exceeding agent-friendly line limits | Built-in script (see below) |

## Quick Start

```bash
# Run all checks on the entire codebase
cd $(dirname $(go env GOMOD)) || cd .

# 1. Cognitive complexity
gocognit -over 15 ./...

# 2. Cyclomatic complexity
gocyclo -over 12 ./...

# 3. Staticcheck analysis
staticcheck ./...

# 4. Go file size limits (hard max 1000 lines, soft target 500 lines)
.agents/skills/golang-check/go-file-size-check.sh
```

## General Guidelines

### Hard Rules

1. **Fix should never bypass a check** — A fix must address the root cause, not silence the tool. Never add `//nolint`, increase the threshold, or restructure code just to dodge the check. If the tool flags it, the code has a real problem — fix it properly.

2. **Size of work does not matter — clean and reusable code are the main strategy** — There is no deadline pressure that justifies introducing technical debt. A small, well-structured fix is always faster than a large, messy one that will need rework. Invest in the right structure from the start.

### Coding Strategy

Use **SOLID principles** to ensure clean, reusable, and maintainable code:

- **S**ingle Responsibility — Each function, type, and package should have one clearly defined responsibility.
- **O**pen/Closed — Design for extension without modification (interfaces, composition, strategy pattern).
- **L**iskov Substitution — Subtypes must be substitutable for their base types without breaking correctness.
- **I**nterface Segregation — Keep interfaces small and focused; consumers should not depend on methods they don't use.
- **D**ependency Inversion — Depend on abstractions (interfaces), not concrete implementations.

**Practical guidance for agents:**

- **Split complex items into manageable units** — A function over the complexity threshold is a signal to extract, not to add a suppression comment. Break large functions into smaller, named helpers. Use lookup tables, early returns, and strategy dispatch instead of long switch/if chains.
- **Document correctly so agents can understand code without losing time reading it** — Write clear doc comments on every exported symbol. Use `// Package` doc to explain the package's responsibility. Document invariants, edge cases, and the *why* behind non-obvious code. Good documentation is the primary tool for enabling other agents (and humans) to work with the code efficiently.

## Cognitive Complexity (`gocognit -over 15`)

Measures how hard code is to **understand** — counts nesting, boolean operators, recursion, and breakpoints in flow.

```bash
# Run on entire module
gocognit -over 15 ./...

# Run on a specific package
gocognit -over 15 ./internal/server/...

# Sort by complexity (descending) to identify hottest paths
gocognit -over 0 ./... | sort -t' ' -k2 -rn | head -20

# Exclude generated files or mocks
gocognit -over 15 ./... | grep -v "_mock.go" | grep -v "_test.go" | grep -v "generated.go"
```

**Counts toward:**
- Deeply nested conditionals (`if` inside `if` inside `if`)
- `if` chains without `else if`
- Boolean operators (`&&`, `||`) in conditions
- `switch`/`case` with many branches
- Recursion, `goto`, `break`, `continue`

## Cyclomatic Complexity (`gocyclo -over 12`)

Measures how many **independent paths** exist through a function — counts `if`, `for`, `case`, `&&`, `||`, etc.

```bash
# Run on entire module
gocyclo -over 12 ./...

# Run on a specific package
gocyclo -over 12 ./internal/server

# Show top offenders regardless of threshold
gocyclo -avg -total .

# Generate markdown report
gocyclo -over 12 . 2>&1 | awk '
  BEGIN { print "| File | Func | Complexity |"; print "|---|---|---|" }
  { printf "| %s | %s | %s |\n", $3, $2, $1 }
'
```

**Counts toward:**
- Each `if`, `else if`, `for`, `range`, `case` (in `select`/`switch`)
- Each `&&` or `||` in a condition
- Each `switch`/`case` label

## Staticcheck (`staticcheck ./...`)

An advanced Go linter — catches bugs, dead code, incorrect usage, performance issues, and style concerns that `go vet` misses.

```bash
# Run all checks
staticcheck ./...

# Run with more verbose output (includes ignored checks)
staticcheck -verbose ./...

# Run a specific set of checks (e.g., ST1xxx = style, S1xxx = simplified code)
staticcheck -checks "ST1,S1" ./...

# Run all checks except a specific category (e.g., ignore performance checks)
staticcheck -checks "-ST1" ./...

# Fail on any issue (exit code 1 if any findings)
staticcheck -f=stylish ./...
```

### Exit Codes

| Exit | Meaning |
|------|---------|
| 0 | No issues found |
| 1 | Issues found |
| 2 | Internal error (bad flags, no Go files, etc.) |

### Common Check Categories

| Prefix | Category | Example findings |
|--------|----------|-----------------|
| `S1` | Simplified code | Unnecessary `else`, redundant Sprintf, unused params |
| `ST1` | Style | Naming conventions, doc comments, receiver names |
| `U1` | Unused | Unused variables, functions, types, fields |
| `SA1-SA9` | Serious analysis | Nil pointer deref, incorrect stdlib usage, range var capture |
| `QF1` | Quick fix | Suggest replacements (e.g., strings.ReplaceAll) |
| `T1` | Testing | Incorrect test helpers, subtest naming |

### Staticcheck Configuration

Staticcheck reads config from `staticcheck.conf` in the module root:

```bash
# Example: staticcheck.conf
# Exclude specific checks that are intentional or noisy
checks = ["all", "-ST1000", "-SA1019"]  # -ST1000 = dot imports, -SA1019 = deprecated code
```

See https://staticcheck.dev/docs/configuration/ for full options.

## Go File Size Check

Keep Go source files small enough for agents and humans to load into context,
reason about, and edit without losing coherence.

| Limit | Value | Policy |
|-------|-------|--------|
| Hard max | 1000 lines | Must never be exceeded |
| Soft target | 500 lines | Aim to stay below this |

Blank lines and comments count because they still occupy context window and
vertical screen space.

```bash
# Find every Go file over the hard limit
find . -type f -name '*.go' -not -path './vendor/*' -not -path './.git/*' -exec wc -l {} + | awk '$1 > 1000 {print $0}' | sort -rn

# Find files over the soft target
find . -type f -name '*.go' -not -path './vendor/*' -not -path './.git/*' -exec wc -l {} + | awk '$1 > 500 {print $0}' | sort -rn
```

### Remediation Rules

1. **Never bypass the check** — do not add exceptions or increase limits to
   accommodate new code. If a file is too big, split it.
2. **Split by responsibility** — each Go file should represent one cohesive
   concern (types, core behavior, rendering, persistence, tests).
3. **Prefer small, focused packages** — if a file is large because it owns
   multiple subsystems, promote those subsystems to separate files or packages.
4. **Document the split** — update package docs and add clear file-level
   comments so agents know where logic lives.

## Combined Analysis Script

Run all checks in one go, with formatted output:

```bash
#!/bin/bash
# golang-check: run all Go static analysis and file-size checks
set -euo pipefail

PROJECT_DIR="$(dirname $(go env GOMOD))"
HAS_ERRORS=0

echo "=== gocognit (cognitive complexity > 15) ==="
gocognit -over 15 "$PROJECT_DIR" || { echo "FAIL: cognitive complexity exceeded"; HAS_ERRORS=1; }

echo ""
echo "=== gocyclo (cyclomatic complexity > 12) ==="
gocyclo -over 12 "$PROJECT_DIR" || { echo "FAIL: cyclomatic complexity exceeded"; HAS_ERRORS=1; }

echo ""
echo "=== staticcheck ==="
staticcheck "$PROJECT_DIR/..." || { echo "FAIL: staticcheck issues found"; HAS_ERRORS=1; }

echo ""
echo "=== go file size (hard max 1000, soft target 500) ==="
"$PROJECT_DIR/.agents/skills/golang-check/go-file-size-check.sh" || { echo "FAIL: file size limits exceeded"; HAS_ERRORS=1; }

echo ""
if [ "$HAS_ERRORS" -eq 0 ]; then
  echo "✓ All checks passed"
else
  echo "✗ Some checks failed"
fi
exit $HAS_ERRORS
```

## Analysis & Fix Plan Workflow

After running all three checks, use this structured workflow to analyze every finding and produce a user-confirmed fix plan.

### 1. Collect All Findings

```bash
cd "$(dirname $(go env GOMOD))"

# Save raw output from each tool
gocognit -over 15 . > /tmp/golang-check/gocognit.txt 2>&1
gocyclo -over 12 . > /tmp/golang-check/gocyclo.txt 2>&1
staticcheck ./... > /tmp/golang-check/staticcheck.txt 2>&1
```

### 2. Categorize by Severity

For each finding, retrieve its official staticcheck explanation, then group by severity tier. Start by fetching all needed explanations in bulk:

```bash
# Fetch explanations for all staticcheck codes found in the output
grep -oE '\b(ST[0-9]+|SA[0-9]+|S[0-9]+|U[0-9]+|QF[0-9]+|T[0-9]+)\b' /tmp/golang-check/staticcheck.txt \
  | sort -u | while read code; do
    echo "=== $code ==="
    staticcheck -explain "$code"
    echo
done > /tmp/golang-check/explanations.txt
```

Use the fetched explanations to annotate each finding in the plan. The severity tiers:

| Tier | Category | Examples |
|------|----------|---------|
| 🔴 **Compile / Import Cycle** | Blocks build | Import cycles, unresolved dependencies |
| 🟠 **Correctness** | Potential runtime bugs | `SA4006` (unused value), `SA4011` (ineffective break), `SA1019` (deprecated API usage) |
| 🟡 **Dead Code** | Unused functions, fields, types if removed cleanly | `U1000` — unused variables, fields, functions |
| 🔵 **Complexity** | Functions exceeding thresholds | gocognit > 15, gocyclo > 12 — refactoring targets |
| ⚪ **Style / Cleanup** | Low risk but improves code quality | `ST1005` (error string capitalization), `S1040` (redundant type assertion) |

### 3. Analyze Each Issue

For every finding, use `staticcheck -explain <code>` to retrieve the official explanation, then answer these questions:

```bash
# Retrieve the explanation for a specific check code
staticcheck -explain U1000
```

```
File: path/to/file.go:42
Check:  SA4006 — this value of X is never used
─────────────────────────────────────────────
Explanation:  [paste from staticcheck -explain SA4006]
Root cause:   [short explanation of why the tool flagged it]
Impact:       [what happens if left unfixed — dead code, future bug, readability debt]
Fix:          [actionable step - remove the assignment, replace with _, add error handling, etc.]
```

**Common patterns reference** (supplement explanation with these concrete examples):

| Finding | Root Cause Pattern | Typical Fix |
|---------|-------------------|-------------|
| `U1000` — field/func unused | Dead code from refactor, leftover scaffolding | Remove the field/func, or add an intentional use |
| `SA4006` — value never used | Assignment result not checked (often `err`) | Check the error, or use `_` to discard explicitly |
| `SA4011` — ineffective break | `break` inside a `select`/`switch` inner block instead of the outer loop | Use labeled `break` or `return` |
| `SA1019` — deprecated API | Using an API removed/deprecated in newer Go versions | Replace with the recommended modern alternative |
| `ST1005` — error string capitalized | `errors.New("Failed...")` / `fmt.Errorf("Invalid...")` | Lowercase first letter: `"failed..."` / `"invalid..."` |
| `S1040` — type assertion to same type | `x.(SomeType)` where `x` is already `SomeType` | Remove the redundant assertion |
| Complexity > threshold | Deeply nested logic, long switch chains, too many if-else branches | Extract functions, use early returns, replace with lookup tables |

### 4. Produce a Consolidated Fix Plan

After analyzing all findings, present a single plan to the user for confirmation. Each table row **must include an Explanation column** showing the summary from `staticcheck -explain`:

````markdown
## 🛠 Fix Plan

### 🔴 Build Blockers (must fix)
| # | File | Issue | Explanation | Fix |
|---|------|-------|-------------|-----|
| 1 | `a/b.go:42` | Import cycle | ... | Detail |
| 2 | `c/d.go:15` | Compile error | ... | Detail |

### 🟠 Correctness (should fix)
| # | File | Check | Issue | Explanation | Fix |
|---|------|-------|-------|-------------|-----|
| 3 | `e/f.go:77` | SA4006 | Unused value `err` | ... | Check or discard with `_` |

### 🟡 Dead Code (should fix)
| # | File | Check | Item | Explanation | Fix |
|---|------|-------|------|-------------|-----|
| 4 | `g/h.go:20` | U1000 | Unused field `foo` | ... | Remove |
| 5 | `i/j.go:55` | U1000 | Unused function `bar` | ... | Remove |

### 🔵 Complexity (refactoring targets)
| # | File | Func | Score | Explanation | Suggested Approach |
|---|------|------|-------|-------------|-------------------|
| 6 | `k/l.go:100` | `parseRootFields` | 50/30 | Function has too many independent paths | Extract switch cases into map-driven dispatch |
| 7 | `m/n.go:200` | `runeWidth` | 34/34 | Deeply nested conditionals make it hard to follow | Replace multi-branch with lookup table |

### ⚪ Style / Cleanup (low risk)
| # | File | Check | Issue | Explanation | Occurrences |
|---|------|-------|-------|-------------|------------|
| 8 | `provider/*.go` | ST1005 | Capitalized error strings | ... | 16 files |

**Total: X items**

> Apply all fixes? (yes/no/skip-<N>)
````

### 5. Present to the User

After collecting and analyzing everything:

1. **Fetch explanations** — `staticcheck -explain <code>` for each unique check code found
2. **Read each raw finding** — inspect the source file around each flagged line to confirm the issue
3. **Categorize** — assign each finding to the appropriate severity tier
4. **Draft the plan** — produce the consolidated table (step 4 above) with the Explanation column filled in
5. **Ask for confirmation** — wait for the user's response before applying any changes

```markdown
I ran all three checks and found <N> items total. Here's the breakdown:

🔴 Build blockers: <N>
🟠 Correctness: <N>
🟡 Dead code: <N>
🔵 Complexity: <N>
⚪ Style/cleanup: <N>

<consolidated plan table with Explanation column>

Do you want me to proceed with all fixes, or skip specific items?
```

### 6. Apply Fixes (On Approval)

Once the user confirms (or specifies skip items):

- Fix them in priority order (🔴 → 🟠 → 🟡 → 🔵 → ⚪)
- After each fix, re-run the relevant check to verify it's resolved
- After all fixes, run the full combined script to confirm clean results
- If new findings appear during remediation, add them to the plan and ask for confirmation

### Example Output

After running the checks, the analysis section should look like this:

````
I ran all three checks and found 8 items total. Here's the breakdown:

🔴 Build blockers: 1 (import cycle in cmd/goa)
🟠 Correctness: 3 (unused values, ineffective break, deprecated API)
🟡 Dead code: 2 (unused fields in google provider)
🔵 Complexity: 1 (parseRootFields at 50/30)
⚪ Style/cleanup: 1 (16 ST1005 violations in provider files)

## 🛠 Fix Plan

### 🔴 Build Blockers
| # | File | Issue | Explanation | Fix |
|---|------|-------|-------------|-----|
| 1 | `cmd/goa` | Import cycle config→tui→tools→core→config | Circular dependency prevents compilation — Go does not allow import cycles | Extract shared types to a new `internal/shared` package |

### 🟠 Correctness
| # | File | Check | Issue | Explanation | Fix |
|---|------|-------|-------|-------------|-----|
| 2 | `internal/agentic/agent.go:725` | SA4006 | Unused value `streamErr` | The value assigned to a variable is never read, making it a potential logic bug or dead store | Check the error, or use `_` to discard explicitly |
| 3 | `internal/agentic/helper/websocket_e2e_test.go:142` | SA4011 | Ineffective break | A `break` statement inside a `switch` or `select` inside a loop only breaks the inner statement, not the outer loop — likely the intent was to break the loop | Use labeled break or return |
| 4 | `internal/agentic/skillrunner/tools/restclient.go:44` | SA1019 | Deprecated `netErr.Temporary` | `net.Error.Temporary()` has been deprecated since Go 1.18 — it is not well-defined and should not be used | Replace with `errors.As` + timeout check |

### 🟡 Dead Code
| # | File | Check | Item | Explanation | Fix |
|---|------|-------|------|-------------|-----|
| 5 | `internal/agentic/agent.go:63` | U1000 | Unused field `outputClosed` | The field is never read or written anywhere in the codebase — dead code from a past refactor | Remove |
| 6 | `internal/agentic/provider/google/provider.go:279` | U1000 | Unused types `googleCandidate`, `googlePart` | These types are defined but never used — likely leftover scaffolding | Remove |

### 🔵 Complexity
| # | File | Func | Score | Explanation | Suggested Approach |
|---|------|------|-------|-------------|-------------------|
| 7 | `internal/agentic/provider/openai/parse.go:114` | `parseRootFields` | 50/30 | High cognitive complexity makes the function hard to understand and maintain | Extract switch cases into map-driven dispatch |

### ⚪ Style / Cleanup
| # | File | Check | Issue | Explanation | Occurrences |
|---|------|-------|-------|-------------|------------|
| 8 | Various providers | ST1005 | Capitalized error strings | Error strings should not be capitalized (Go style convention — they often appear after a prefix in logs) | 16 files |

Do you want me to proceed with all fixes, or skip specific items?
````

## When to Run This

| Situation | Action |
|-----------|--------|
| Before committing (`pre-commit`) | Run all three checks |
| After large refactor or rewrite | Run all three checks |
| CI fails lint/analysis | Run the failing check with `-verbose` |
| Code review with high complexity | Run `gocognit -over 0` and `gocyclo -over 0` to spot refactoring targets |
| Onboarding new developers | Run `staticcheck` to catch common Go pitfalls |
| Triaging tech debt | Run the full Analysis & Fix Plan workflow above |

## Troubleshooting

| Issue | Fix |
|-------|-----|
| `gocognit: command not found` | `go install github.com/uudashr/gocognit/cmd/gocognit@latest` |
| `gocyclo: command not found` | `go install github.com/fzipp/gocyclo/cmd/gocyclo@latest` |
| `staticcheck: command not found` | `go install honnef.co/go/tools/cmd/staticcheck@latest` |
| `staticcheck` internal error 2 | Run from project root with `go.mod`, or pass explicit `./...` |
| Complexity report is too noisy | Increase threshold with `-over 20` temporarily, then ratchet down |
| `gocognit`/`gocyclo` with `./...` fails | These tools don't accept `./...` — use `.` (recursive directory scan) or an explicit package path like `./internal/server` |
| Want to exclude generated files | Pipe through `grep -v "_mock.go"`, `grep -v "generated.go"`, etc. |

## Tool Versions

Check installed versions:

```bash
# Run with no args to see version banner
gocognit 2>&1 | head -1
gocyclo 2>&1 | head -1
staticcheck -version
go version
type gocognit gocyclo staticcheck
```
