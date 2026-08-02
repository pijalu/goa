# Bug and feature Tracking

## Guideline
1. Create a detailed fix plan for each bug - the plan must contain test approach and validation steps - execute the plan and validate the fix when all elements are in place.
2. Any issues found must be fixed and the fix plan must be updated accordingly.
3. Issues found during testing must be fixed and the fix plan must be updated accordingly.
4. Each bug should be moved to archive when tested and closed as the associated plan.
5. Use interactive shell to validate the output of the tool - you must verify the actual terminal output.
6. Check code quality with each tool run separately (do not chain them with `;` or `&&`):
   - `go vet ./...`
   - `staticcheck ./...`
   - `gocognit -over 15 .`
   - `gocyclo -over 12 .`
   - `go test -count=1 -race -cover ./...`
   Fix any new issues introduced by the change. Pre-existing warnings are acceptable only if they are unrelated to the change and explicitly noted.

At the end of the session - the bug list should be empty and this file should only contain the guidelines for bug reporting.
If new items are added, restart the process.

## Workflow for bugs
1. Reproduce the failure before editing — ideally a command or script that triggers it on demand.
2. State the observed failure exactly (command + output).
3. Localize to the smallest region — ideally the specific lines — before editing. Precise localization is the strongest predictor of a correct fix.
4. Change one hypothesis at a time.
5. Prefer the minimal fix over a broad refactor.
6. Verify against the original failing command before declaring done.
7. Run the code-quality checks from guideline #6 separately and confirm the fix does not introduce new violations.
8. Move the bug list to `docs/archive/bugs.<fixdate>.md` when all items are closed.

---

## python tool `re` module missing DOTALL / MULTILINE / VERBOSE flags — OPEN

- **Observed** (2026-08-02, parsing `internal/parse/sql_tables.go` in the
  `python` tool REPL):
  ```
  ✗ python
  >>> import re
  ...
  ... m = re.search(r'var yyRuleInfoNRhs = \[\]int\{(.*?)\n\}', src, re.DOTALL)
  ...
  Error: [python error: execution_error]
  Traceback (most recent call last):
    File "<python>", line 6, in <module>
  AttributeError: 'module' has no attribute 'DOTALL'
  ```
  `re.S`, `re.M`, `re.X` (and their long names) fail the same way; only
  `re.I` / `re.IGNORECASE` exist.
- **Localization**: `internal/python/stdlib/re.go` —
  - Flag bits reserved but never assigned: lines 16-21 (`reFlagIgnoreCase = 1
    << iota` followed by three reserved slots for M/S/X).
  - Module `Globals` registers only `"I"` / `"IGNORECASE"` (~line 116); no
    `S`/`DOTALL`, `M`/`MULTILINE`, `X`/`VERBOSE` → the AttributeError above.
  - `compileRegexp` (~line 513) maps only IGNORECASE to an inline `(?i)`
    prefix; no other flag handling.
  - The module doc already admits it: "Unsupported in the first pass: M
    (MULTILINE), S (DOTALL), X (VERBOSE)".
- **Fix plan**:
  1. Assign the reserved bits (MULTILINE `1<<1`, DOTALL `1<<2`, VERBOSE
     `1<<3`) and register both short and long names in the module `Globals`
     dict.
  2. `compileRegexp`: build one combined inline prefix from the flags —
     IGNORECASE→`i`, DOTALL→`s` (`.` matches `\n`), MULTILINE→`m` (`^`/`$`
     match at line boundaries). RE2 supports all three.
  3. VERBOSE has no RE2 equivalent (`(?x)` unsupported): either preprocess
     the pattern (strip unescaped whitespace and `#`-to-EOL comments outside
     character classes) or reject with a clear `ValueError` stating the
     limitation. Pick one, implement it, and document it in the module doc
     and the TOOLS.md python section.
  4. Flag combination via `|` (e.g. `re.S | re.I`) must work — bit tests
     already support it once the bits exist.
  5. Update the module doc string (drop "Unsupported in the first pass" for
     the implemented flags).
- **Test approach**: table-driven tests in `internal/python/stdlib/re_test.go`:
  - `re.DOTALL` / `re.S` attribute access returns the constant (regression
    test for this exact report).
  - `search(r'a.b', 'a\nb', re.DOTALL)` matches; without the flag it doesn't.
  - `findall(r'^b', 'a\nb', re.MULTILINE)` matches `b` after the newline.
  - Combined `re.S | re.I` semantics in one call.
  - VERBOSE per the chosen implementation (strip-and-match or documented
    error).
  - All existing `re` tests keep passing.
- **Validation steps**:
  1. `go test ./internal/python/... -count=1 -race` green.
  2. Interactive validation per guideline 5: run the `python` tool in the TUI
     and re-execute the reported snippet (`re.search(..., re.DOTALL)`); it
     must return a Match instead of AttributeError.
  3. Quality gates (run separately): `go vet ./...`, `staticcheck ./...`,
     `gocognit -over 15 .`, `gocyclo -over 12 .`,
     `go test -count=1 -race -cover ./...` — no new violations.
- **Workaround until fixed**: inline RE2 flags work today, e.g.
  `re.search(r'(?s)var yyRuleInfoNRhs = \[\]int\{(.*?)\n\}', src)`.

---

## Micro-compaction cache gate fails open during the entire first turn — OPEN

- **Observed** (2026-08-02, session export goa-export-20260802-104717.zip,
  kimi-code/k3-256k): a micro-compaction fired at round 58 of the session's
  FIRST turn (context at 50.04%, `min_context_ratio: 0.5` crossed — that part
  is configured behavior). The in-place tool-result truncation busted the
  demonstrably hot provider prefix cache: the next request reprocessed
  51,133 uncached tokens with cache_read collapsing from 113,408 (round 57)
  to 5,376 (round 58); rounds 59+ re-warmed at the shrunk size. This is the
  exact cache-churn the `cache_miss_threshold: 1h` gate exists to prevent
  (compaction.go:63-71).
- **Localization**: `internal/agentic/compaction.go` —
  - `microCompactForced` defers mutation only when `contextRatio <
    deferCeiling && !a.cacheAssumedCold()`.
  - `cacheAssumedCold()` (lines 106-116) returns true whenever
    `lastTurnEnd.IsZero()` — "no previous turn has completed yet (first turn
    / fresh resume)". `lastTurnEnd` is written only in `finishProcessing`
    (turn END), so the cache is presumed cold for the ENTIRE first turn:
    every per-round gate check in turn 1 (agent_streaming.go:165) sees it
    cold even though the cache has been hot since round 2.
  - The presumption is only sound for the session's first request; by round
    58 it is stale and the gate fails open.
- **Fix plan**: make the cold presumption expire once the session has
  evidence of a warm cache. Concretely: treat the cache as hot when any
  completed request in this agent observed `cache_read_tokens > 0` (track a
  `cacheWarmObserved` bool, set under a.mu where token_stats are recorded),
  OR expire the first-turn presumption after the first completed stream
  round (lastRoundEnd rather than lastTurnEnd). Prefer the observed-cache-hit
  signal: it also hardens fresh-resume cases where the provider cache is
  actually warm (short resume gap). Keep `lastTurnEnd` idle-gap logic for
  later turns unchanged.
- **Test approach** (`internal/agentic/compaction_test.go` /
  `agent_compression_cache_gate_test.go`):
  - Regression: first turn, round 2+, usage ≥ min_context_ratio, provider
    reports cache_read > 0 on round 1 → micro compaction must DEFER (no
    EventCompact, history untouched).
  - First turn with no cache hits reported (cache_read == 0) → compaction
    may still run (genuine cold cache).
  - Manual `/compress` (force) still bypasses the gate.
  - Idle ≥ threshold between turns → compaction runs (existing behavior
    preserved).
- **Validation steps**:
  1. `go test ./internal/agentic/ -run 'Compaction|Compression' -count=1
     -race` green, then full package suite.
  2. Re-run a long single-turn session against a cache-reporting provider
     and confirm no `compact` event fires while cache_read > 0 below the
     deferral ceiling.
  3. Quality gates (separately): `go vet ./...`, `staticcheck ./...`,
     `gocognit -over 15 .`, `gocyclo -over 12 .`,
     `go test -count=1 -race -cover ./...` — no new violations.
- **Note**: unrelated second cache miss in the same export (request at
  10:45:39, full cold) is expected provider TTL expiry after a 34-min idle
  gap — no action needed.