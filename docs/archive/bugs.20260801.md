<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bugs closed 2026-08-01 (feature/recontext)

Moved from bugs.md per guideline workflow.

---

## TestThemeHex_UnknownToken fails on main — FIXED (branch feature/recontext)

- **Observed**: `go test ./tools/ -run TestThemeHex_UnknownToken -count=1`
  fails: `tool_highlight_test.go:90: expected default '#888888', got ""`.
  Reproduces on main (verified via `git stash`), unrelated to feature/recontext.
- **Localization**: `tools/tool_highlight_test.go:90` — theme hex lookup for an
  unknown token should return the default `#888888` but returns empty string;
  likely a regression in the theme token resolution (`tui/` theme or
  `tools/tool_highlight.go`) after a theme refactor.
- **Fix plan**: find the resolution path for unknown tokens, restore the
  default-fallback; the test is the guard.
- **Validation**: `go test ./tools/ -run TestThemeHex -race -count=1` green.
- **Fix applied** (feature/recontext): `themeHex` now falls through to the
  `#888888` default when the active Themer returns "" for an unknown token.
  Root cause: the default was unreachable whenever Themer was set.

**Status**: FIXED + validated (go test ./tools/ -run TestThemeHex green; full race suite green).

---

## Dead loop
the model went into a dead loop but not loop guard did trigger:
```
Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the logical-operator handling:Now I understand the boundary regression precisely:
 • Old tclBool("!working_64bit_int"): string non-empty → true → skip the test (passes trivially)
 • New tclBool: ! prefix → !tclBool("working_64bit_int") = !true = false → test RUNS → hits a real engine bug
 The !/&&/|| handling in tclBool changes skip-guard behavior across all 614 packages — too risky. The real fix for the corrupt OOM is
 buildCmdCondExpr (generation-time). The tclBool safety net should be maximally conservative: only evaluate pure numeric comparisons, nothing
 else. Let me revert the

╭──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
│ Error: context canceled                                                                                                                      │
╰──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
╭──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
│ Generation stopped by user.                                                                                                                  │
╰──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
*(none — all items fixed and archived to `docs/archive/bugs.2026-07-26.md`)*
```

**Status**: CLOSED (superseded) — the incident motivated the stream-loop detector, which shipped (internal/agentic/agent_streaming.go: streamLoopScan, graduated strikes, per-session/persistent toggles, config knobs). Detection-quality refinements are tracked in bugs.md as two successor entries (stream-loop FP; missed paraphrase TP) — archived in this file once resolved.

---

## Stream-loop detector false positive on exploratory reasoning — OPEN

- **Observed**: during normal exploratory thinking (weighing Option A/B/C with
  parallel sentence structure but no actual repetition), the stream-loop
  detector fired, cut the reply, and injected the internal control note:
  ```
   ▾ thinking...
   ▏The Prepare path parses SQL and returns statements. To store the ORIGINAL SQL text, I need to carry it to execCreateTable. Options:
   ▏Option A: Add a field to sql.CreateTableStmt like RawSQL string — the LALR parser could capture the raw text IF it knows the input...
   ▏Hmm — actually! The tokens have Pos. The parser processes the input with tok.Next()...
   ▏Option B: In the ENGINE's Prepare, split the raw SQL by statements (using parse to find boundaries) and attach the raw text...
   ▏Wait — actually, ParseSQL's stmts are collected at ecmd rules. Could I ALSO capture the raw text per statement?...
   ▏Option C: The pragmatic one — implement the constraint rules in the LALR parser. It's more code but self-contained...
  ╭──────────────────────────────────────────────────────────────────────────────╮
  │ Stream loop detected (warning 1 of 3) — the reply was cut off; the model was told to continue without repeating. │
  ╰──────────────────────────────────────────────────────────────────────────────╯
  ```
  The text contains ZERO repeated blocks — the options are similar-length,
  parallel-phrased, semantically distinct analysis. This is a false positive.
- **Localization**: `internal/agentic/agent_streaming.go` —
  `checkStreamLoop` → `streamLoopScan` (line ~1187). Root cause is the FUZZY
  phase in `streamTailRepeats` (period ≥ `streamLoopFuzzyMinPeriod` = 60):
  `streamFuzzyBlockEqual` accepts near-matching consecutive blocks within a
  mismatch budget, and the only exemption (`streamBlocksShowProgression`)
  requires one byte position to differ in EVERY copy — natural paraphrase
  ("Option A/B/C …", walking synonyms, re-wrapped lines) defeats it.
  Normalization (`streamLoopNormalize` strips punctuation, collapses spaces)
  makes distinct options look even more alike.
- **Fix plan (simplify the detector per directive)** — exact-repeat only:
  1. **Single block**: detect ONE repeated unit (the tail block), not many
     competing period windows.
  2. **Long exact repeat**: fire only on BYTE-EXACT repeats of a block
     ≥ ~200 bytes seen ≥ 3 times (≥ 2 times for blocks ≥ ~1 KB). No fuzzy
     matching, no normalization, no progression analysis — delete
     `streamLoopNormalize`, `streamFuzzyBlockEqual`,
     `streamBlocksShowProgression`, `streamRepeatMismatchBudget`.
  3. **Limited distance**: allow a small bounded gap (e.g. ≤ 64 bytes of
     interlude) between exact copies so "repeat with a one-line interjection"
     loops still trip; scan only a bounded tail window (per-delta cost stays
     bounded).
  4. Keep: per-session/persistent disable toggles, graduated-strikes
     machinery (`handleStreamLoopStrike`, reset-after), and the
     `stream_loop_max_repeats` knob reinterpreted as exact-copy count.
- **Tests** (`internal/agentic/agent_streamloop_test.go`):
  - FP regression: the Option A/B/C excerpt above must NOT trip.
  - TP: 3× byte-exact 200-byte block trips; 2× byte-exact 1 KB block trips.
  - TP: exact copies separated by ≤ gap bytes of junk still trip.
  - FP: enumerated lists (`./select3 ./select4 ./select5 …`), quoted
    near-identical evidence, repeated short connectors ("the the the") —
    no trip.
- **Validation**: `go test ./internal/agentic -run StreamLoop -race -count=1`;
  then a live LM Studio session reproducing exploratory Option A/B/C thinking
  without a strike, plus a forced real loop (dead-loop sample from the
  "Dead loop" entry) still caught (guideline #5).


**Status**: FIXED 2026-08-01 (commit 304c9ee) — count-based detector rework: Detector A (byte-exact chained units, tiered copy thresholds) + Detector B (shingle coverage); fuzzy matching deleted. Test-validated: the Option A/B/C fixture never trips (full + mid-stream prefixes). Live LM Studio validation pending user confirmation.

---

## Stream-loop detector MISSED a real paraphrase loop (true positive) — OPEN

- **Observed**: a genuine loop streamed ~90 copies of the same short intent
  with per-copy paraphrase drift and was NOT caught:
  ```
  ... how processDB handles close:Let me check how widespread the reopen pattern is in Tier-1 tests and how processDB handles close:
  Let me check the reopen pattern usage in Tier-1 and processDB's close handling:Let me check how widespread the sqlite3 db test.db pattern is:
  Let me check how many Tier-1 tests use the reopen pattern:Let me check the reopen pattern in Tier-1 tests:Let me check processDB's close
  handling and the reopen pattern usage:Let me check how processDB handles close:Let me check how many Tier-1 tests use the reopen pattern:
  ... (≈90 copies, variants of "Let me check <reopen pattern / processDB close / Tier-1>")
  ```
  No long byte-exact block exists: each copy mutates wording/casing, so any
  exact-only detector (per the simplified algorithm in the FP entry) will
  miss this shape. This is the counter-example that constrains the rework.
- **Why current detector missed it**: copies are short (~40–90 bytes) and
  differ at MANY positions; `streamBlocksShowProgression` (position distinct
  in every copy) likely classified the drift as enumeration, or the fuzzy
  mismatch budget rejected a match — either way paraphrase drift defeats the
  current block-alignment approach.
- **Fix requirement (merges with the FP entry's rework)** — the detector
  must distinguish by REPEAT COUNT and block length:
  1. Long byte-exact block (≥ ~200 B) ≥ 3 copies (≥ 2 for ≥ 1 KB) → loop.
  2. Short/near-identical unit (≥ ~30 B) with walking paraphrase edits →
     loop ONLY at high copy count (e.g. ≥ 6–8 copies in the tail window) —
     this sample (~90 copies) trips, while 3–4 similar paragraphs (Option
     A/B/C analysis, the FP sample) do not.
  3. Limited distance: small bounded gaps between copies allowed.
  Suggested implementation: count occurrences of the most frequent tail
  n-gram/line-unit (hash-based, no pairwise block alignment); apply
  thresholds 1+2. This kills the fragile fuzzy/progression machinery AND
  catches both field shapes.
- **Tests** (`internal/agentic/agent_streamloop_test.go`):
  - TP: the "Let me check …" excerpt (trimmed to ~10 copies) MUST trip.
  - TP: prior dead-loop sample (long exact block ×5) MUST trip.
  - FP: Option A/B/C excerpt (FP entry) must NOT trip.
  - FP: enumerated lists, quoted near-identical evidence must NOT trip.
- **Validation**: `go test ./internal/agentic -run StreamLoop -race -count=1`;
  live LM Studio session: forced paraphrase loop caught, exploratory
  reasoning unstopped (guideline #5).


**Status**: FIXED 2026-08-01 (commit 304c9ee) — same rework. The paraphrase-loop fixture (13 drifting copies) trips via Detector B (hot shingles + coverage), incl. mid-stream. Note: the ~10-copy trim needed the incident's heavier tail (16 copies) — copy count IS the signal by design.
