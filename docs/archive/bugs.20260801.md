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

---

## Goal tool result line floods the timeline with the full objective — should show the goal short name — FIXED (commit 5d34156)

- **Observed**: every `goal` tool result carrying a goal snapshot
  (create / get / update_todo) renders the full objective, which floods the
  timeline and truncates mid-word:
  ```
  ✓ ◆ Updated todo t2 → done
  Goal active: Wire the go-lemon generated TCL parser into tcl2go as a drop-in replacement for the hand-written tcl.ParseCommands parser, complet
  ```
  Expected — the goal's short friendly name is used instead:
  ```
  ✓ ◆ Updated todo t2 → done
  goal active: honest.zebra
  ```
- **Localization**: `tui/goal/tool_renderers.go`:
  - `renderGoalSnapshotLine` (line ~222) builds
    `"Goal %s: %s · %d turns · %s tokens · %s"` from
    `goalSummaryJSON.Objective` — the raw, unbounded objective string.
  - `goalSummaryJSON` (line ~166) does NOT decode the snapshot's `"name"`
    field, even though the goal tool result carries it
    (e.g. `"name":"honest.zebra"`).
  - The prefer-name pattern already exists for queued goals:
    `upcomingGoalJSON.Name` + `goalLabel()` (line ~300) — reuse it.
- **Fix plan**: add `Name string \`json:"name"\`` to `goalSummaryJSON`; in
  `renderGoalSnapshotLine` render the short name when non-empty, falling back
  to a truncated objective (same rule as `goalLabel`). Keep the
  turns/tokens/elapsed/todos stats suffix (useful signal) — only the
  objective → short-name swap changes.
- **Tests**: table-driven cases in `tui/goal/tool_renderers_test.go`:
  snapshot with name → line shows `<status>: <name>` and NOT the objective;
  snapshot without name → truncated-objective fallback; stats suffix intact.
- **Validation**: `go test ./tui/goal -race`; then run a TUI session with a
  long-objective goal, update a todo, and verify the timeline shows the
  one-line short-name summary (guideline #5 — verify real terminal output).

**Status**: FIXED 2026-08-01 (commit 5d34156) — goalSummaryJSON decodes 'name'; summaryLabel prefers the friendly short name (fallback: truncated objective) in snapshot + list Active lines; stats suffix unchanged. Table-driven tests cover named/unnamed/todos/list.


## Context compression not triggering at 92.9% (auto)

- **Observed**: footer shows `CH99.1% TC:290 92.9%/262.1K (auto)` — context
  at 92.9% of a 262.1K window, cache 99.1% hot, compression mode (auto), yet
  no (micro) compression has fired. Expected: some micro compression well
  before the wall.
- **Second sample (2026-08-01) — worse**: compression still had not fired
  PAST 100% of the window; the oversized request went out and the provider
  hard-rejected it, pausing the goal:
  ```
  │ Error: 401 - k3-256k supports only 256K context. │
  ◦ Goal paused by the system
  Paused after provider authentication error
  ↑545.5K ↓182.1K 19.6 tok/s CH99.0% TC:436 115.5%/262.1K (auto) c:1m-0  (kimi-code) k3-256k • high • [17%|14%]
  ```
  At 115.5%/262.1K with (auto), neither soft nor main compression fired
  BEFORE the call; the overflow surfaced as a provider 401. The safety net
  failed open: usage ≥ 100% must force compression (or block) BEFORE the
  request is built — never send an oversized request. (`c:1m-0` in the
  footer = compression counters: 1 micro-compact, 0 main compacts.)
- **Root cause (confirmed)**: compression gates run ONCE per user turn
  (`prepareTurn` → `maybeCompress` + `enforceContextCeiling`,
  internal/agentic/agent_streaming.go:1438-1441), but
  `processTurnWithStream` then loops `runStreamRound` per tool round — each
  round appends assistant+tool messages and re-streams with NO compression
  check between rounds. A long turn (TC:436) climbs past trigger → hard →
  >100% unchecked until the provider rejects the request. The 92.9% sample:
  the last turn-start check ran below 85% (deferralCeiling), the turn grew
  mid-flight; the 115.5% sample proves no mid-turn gate exists.
- **Design directive (user, 2026-08-01) — 3 layers / 3 thresholds**:
  compression must be three independently configurable layers:
  | Layer | Default level | Default strategy |
  |-------|---------------|------------------|
  | Soft | 80% | micro |
  | Medium (trigger) | 90% | tool_elision |
  | Hard / Error | 95% | hybrid |
  The user can set each level from 10% to 95% in 5% increments and select
  any strategy per layer (soft stays zero-LLM: micro/elision only). Footer
  label must reflect what will actually fire (e.g. `auto+micro`).
- **Design directive (user, 2026-08-01) — cache management configurable**:
  the `/config` command must allow changing the cache-management behavior
  (the prefix-cache gate that defers compression while the provider cache
  is presumed hot: CacheMissThreshold, cache-hot deferral, deferralCeiling).
  Configurable GLOBALLY with a PER-MODEL override — some models/providers
  need specific rules (e.g. providers without a prefix cache, or local
  models where cache-hot readings like CH99% are meaningless and the gate
  should simply be off). Precedence: model-level > global > built-in
  default. A per-model "cache gate: off" must be expressible (gate
  disabled = compression never deferred for cache).
- **Fix plan**:
  1. Per-round gate: run `maybeCompress` + `enforceContextCeiling` in
     `startStreamRound`'s round>0 branch so every API request is preceded by
     a fresh compression check (covers tool rounds and error-retry re-streams).
  2. Three-layer config: per-layer strategy selection with the defaults
     above (SoftPercent default becomes 80 with micro; TriggerPercent 90
     with tool_elision; HardPercent 95 with hybrid), levels validated to
     10–95 in 5% steps; footer shows the effective tiers.
- **Tests**: multi-round scripted turn where tool results push usage past
  the trigger mid-turn; assert the stream AFTER the gate went out
  compressed (elided payload absent), i.e. the test fails without the
  per-round gate. Gate-decision tests at 80/85/90/93% cache hot/cold
  (extend agent_compression_cache_gate_test.go). Layer-config validation
  tests (5% steps, per-layer strategy).
- **Validation**: `go test ./internal/agentic -race -count=1`; scripted
  over-trigger session compresses before the wall; footer reflects reality.


- **Status**: FIXED on branch feature/recontext.
  - Root cause (per-round gap): commit 941cc55 — startStreamRound's round>0
    branch now runs maybeCompress + enforceContextCeiling before every
    re-stream; regression test TestAgent_CompressionGateBetweenRounds drives
    an 8-round tool turn past the trigger and asserts the final request went
    out elided (fails without the gate).
  - 3-layer redesign: commit 6ef7ebb — soft (default 80%, micro),
    trigger (90%, tool_elision), hard (95%, hybrid; new tierHard fires at the
    emergency ceiling with cache gate bypassed); levels user-settable 10-95%
    in 5% steps (0 = SDK default, soft -1 = disabled); per-layer strategies
    configurable globally and per_model; cache gate on/off global and
    per_model (off = never defer for hot cache); /config menu gained
    Soft/Hard strategy pickers, cache-gate toggle, and 5%-step pickers.
  - Footer label: commit c828c75 — "(auto+micro)" reflects the soft layer.
  - Tests: TestResolveThresholdsStrategies, TestZeroLLMStrategy,
    TestProactiveTier (incl. tierHard, DisableCacheGate, soft -1),
    TestAgent_CompressionGateBetweenRounds, overlay merge test, config
    validation cases, /config menu tests. go test ./internal/agentic
    ./core/... ./config -race green.
  - Live validation against LM Studio at >90% usage: pending user
    confirmation (test-validated only).


## Cache miss (CM) counter in stats and status bar

- **Request (user, 2026-08-01)**: using stats reported by the model, show a
  cache-miss "CM" counter next to the existing CH (cache hit) indicator:
  in the stats display and in the status bar, e.g.
  `... CH99.0% CM:3 TC:436 ...`. Only shown when non-zero (no noise when
  the cache behaves).
- **Notes**: CH today derives from provider usage (cached tokens vs input).
  CM should count cache MISSES (requests where the cached-prefix share
  dropped / cache was not used) — define from the same provider usage
  stats; a miss is interesting only when the provider supports caching
  (hide entirely when it does not).
- **Tests**: footer/stats rendering with miss count 0 (hidden) and >0
  (shown); provider without cache stats (hidden).

- **Status**: FIXED on branch feature/recontext (commit c828c75): CM counts
  cache busts (zero-cache-read requests after establishment; cold starts and
  cache-less providers never count), rendered next to CH only when non-zero,
  plus cm=N in the stats log line. Tests: TestHandleTokenStats_CacheMissCounter,
  TestBuildFooterStatParts_CacheMiss. Validated: go test ./internal/app green.
## TUI shows unexpected repetition on normal (non-thinking) messages — RESOLVED (model-origin paraphrase loop; capture option added)

- **Resolution**: review verdict — the repetition is **model-origin**, not a TUI double-draw: the repeated blocks carry casing/punctuation *variants* ("is never called" / "is NEVER called" / "is NEVER CALLED!") which only a model produces; a TUI duplication would be byte-identical. The sample is the same missed-paraphrase class fixed by the stream-loop detector rework (archived above); regression test `TestStreamLoop_TUIRepetitionSampleDetected` feeds this exact text to the production scan and it trips (internal/agentic/agent_streamloop_test.go).
- **Capture option (entry's diagnostic fallback)**: `--capture-stream <path>` CLI flag (config `logging.capture_stream`) records the exact agent stream flow as unbuffered JSONL at the agent-output boundary (internal/app/stream_capture.go, commit 386a235) — every output event (ts/type/state/delta/text/tool fields) for replay/diff of model-origin vs TUI-origin in future reports.
- **Tests**: detector sample test + stream_capture writer tests (record shape, ordering, open-failure).
- **Validation**: `go vet` + `go test` internal/app + internal/agentic green; gocognit/gocyclo clean. Live replay of a captured session is the manual follow-up.

- **Observed**: assistant message text renders with near-identical sentences
  repeated back to back, with small casing/punctuation variations:
  ```
  ... Let me search for all callers of checkConstraints.isIgnoreableConflict is never called — the error comes from a different path.
  Let me find all callers of checkConstraints:isIgnoreableConflict is NEVER called! The error must come from a different path. Let me find all
  callers of checkConstraints:isIgnoreableConflict is NEVER CALLED. The error must come from a different path. Let me find all callers of
  checkConstraints:isIgnoreableConflict is NEVER called — ... (repeats ~7× with casing variations)
  ```
  Two candidate root causes, must be distinguished before fixing:
  (a) TUI/stream-accumulation bug: stream deltas are appended twice or a
      re-render overlaps prior text (model output is actually clean).
  (b) Genuine model loop with per-copy variations that the stream-loop
      detector misses (see the "Stream-loop detector false positive" entry —
      the detector rework must ALSO still catch this true-positive shape).
- **Localization pointers**:
  - Stream delta → chat path: `internal/app/stats.go` (delta handling),
    `tui/` chat viewport append logic; note `tui/user_message_double_draw_test.go`
    exists — double-draw bugs have happened in this area before.
  - Detector side: `internal/agentic/agent_streaming.go` `streamLoopScan`
    (the repeated unit above is ~120 bytes with per-copy casing edits).
- **Investigation plan**:
  1. Review the streaming TUI code end to end (delta accumulation, buffer
     flush on tool-call boundaries, viewport append vs. re-render) for a
     duplication path.
  2. If the root cause cannot be found by review: add a **stream capture**
     option (command line, e.g. `goa --capture-stream <file>`) that records
     the exact inbound provider stream (raw deltas with event sequence) to a
     log file, plus a replay path (e.g. `--replay-stream <file>` feeding the
     recorded deltas through the TUI headlessly) so the exact flow can be
     replayed and bisected deterministically.
- **Tests**: once root cause is known — regression test at the failing layer
  (filmstrip test for TUI duplication; streamloop test if detector-side).
- **Validation**: reproduce with the same prompt class on LM Studio; captured
  stream replay shows identical rendering; fix removes duplication (guideline
  #5 — verify real terminal output).


## Option: move the busy spinner from in-chat line to the status bar — RESOLVED (tui.spinner_location option, commit ea33f83)

- **Resolution**: implemented as specced with one simplification — the footer busy indicator already existed (`FormatModelPart` renders the selected spinner's animated frame next to the model when busy), so the change is: config `tui.spinner_location: chat|statusbar` (default chat) + `assembleEngine` skips the `StatusMsg` engine child in statusbar mode (the component still ticks, feeding the shared frame the footer consumes). Non-busy status bubbles (⟡ errors/infos) are a separate path, unchanged.
- **Tests**: `TestSpinnerLocation` helper table (nil/unset/statusbar/unknown→chat fail-safe) + `TestFilmstrip_SpinnerLocation` driving request events in both modes (status line "⬡ Processing…" present in chat mode, suppressed in statusbar mode, shared spinner frame still live) + `/config` menu root test updated for the new entry.
- **Validation**: `go vet` + `go test` internal/app + core/commands + config + tui all green. Live TUI check in both modes is the manual follow-up (guideline #5).

- **Request**: add an option to switch the in-chat spinner line
  ("⬣ Sending request...") to a simple animated spinner in the status bar
  (footer), next to the model, e.g.:
  ```
  ⬣ (kimi-code) k3-256k • high • [7%|10%]
  ```
  The animation must use the user's selected spinner style
  (`internal/spinner/spinners.json` + spinner selection), just rendered in
  the footer instead of the chat timeline. Benefits: chat timeline stays
  clean (no transient spinner lines in scrollback/export), busy state is
  visible at a fixed location.
- **Localization**:
  - In-chat spinner: `internal/app/stats.go` (`a.subs.statusMsg.Show("Sending
    request...")`, label at ~line 680), `internal/app/submithandler.go:456`,
    `internal/app/toolcall_footer.go:81` — all drive `subs.statusMsg`.
  - Footer/status bar: `tui/footer.go` (`Footer`, `FooterData` with Model,
    Provider, ThinkingLevel, context %, `SetModelBusy`) — a spinner frame
    field would be added to `FooterData` and rendered left of the provider/
    model block.
  - Spinner styles/selection: `internal/spinner/spinner.go` +
    `spinners.json` (frame sets already user-selectable).
  - Config surface: new setting e.g. `tui.spinner_location: chat|statusbar`
    (default `chat` = current behavior), exposed via `/config` like the other
    TUI options.
- **Fix plan**:
  1. Config: add `tui.spinner_location` (enum chat|statusbar, default chat),
     merged/cascaded like other settings + `/config` entry.
  2. Footer: add `BusySpinner string` (current frame) to `FooterData`; render
     it as an animated prefix next to the provider/model when busy; frame
     advances on the existing spinner tick (reuse the statusMsg tick source
     so both paths share timing).
  3. App: when `statusbar` mode, suppress the in-chat `statusMsg.Show` busy
     line and instead push spinner frames to the footer; keep in-chat for
     non-busy status messages (errors, infos) unchanged.
  4. Keep behavior identical for `chat` mode (default) — no visual change.
- **Tests**:
  - `tui/footer_test.go`: busy + spinner frame set → footer line contains the
    frame next to the model; not busy → no frame.
  - Filmstrip test (tui-test skill pattern): drive request events in both
    modes — `statusbar` mode shows no "Sending request..." chat line, footer
    carries the spinner; `chat` mode unchanged.
  - Config: default = chat; `/config` set/get round-trip for
    `tui.spinner_location`.
- **Validation**: `go test ./tui/... ./internal/app/... -race`; then live TUI
  session in both modes, watching the real terminal (guideline #5): in
  statusbar mode the footer animates "⬣ (provider) model • …" while the chat
  shows no spinner line; switching mode via /config takes effect on the next
  request.

---

## Skills config: select which embedded + startup (file-based) skills are enabled/disabled — FIXED 2026-08-01

- **Request**: `skills` config must allow selecting which embedded skills are
  enabled/disabled, and the same selection must also apply to skills found
  during startup (file-based skills from `~/.agents/skills/`, `.agents/skills/`,
  `.goa/skills/`, configured `skills.dirs`, plugin dirs).
- **Current state**: `skills.disabled: [names]` exists (commit c0bf90d) but is
  embedded-only — `SkillRegistry.SetDisabled` gates `scanEmbeddedFS` only;
  `scanDir` (file-based) is explicitly unaffected, so a disabled built-in can
  be shadowed by a same-named file skill and file skills can never be disabled.
  There is no allowlist (`skills.enabled`) to select which skills are active.
- **Localization**:
  - `skills/loader.go`: `SetDisabled` (line ~166), `scanEmbeddedFS` gate
    (line ~245), `scanDir` (line ~289, no gate), `scanSubSkills`/`scanEmbeddedSubSkills`.
  - `config/config.go` `SkillsConfig` (line ~566): `Disabled []string` only.
  - `config/config_merge.go` (~line 328): `Disabled` merged; no `Enabled`.
  - Wiring: `internal/app/subsystems.go` `newSkillRegistry` (~line 843),
    `internal/app/app.go` (~line 616), `internal/app/helpers.go` (~line 70).
- **Fix plan**:
  1. Add `Enabled []string \`yaml:"enabled,omitempty"\`` to `SkillsConfig`
     (allowlist; empty = all enabled) + cascade merge with dedup.
  2. `skills/loader.go`: add `SetEnabled(names)`; gate BOTH `scanEmbeddedFS`
     and `scanDir` (and sub-skill scans) on disabled ∩ enabled — a skill loads
     iff not disabled AND (enabled list empty OR in enabled list). `disabled`
     wins over `enabled` on conflict.
  3. Wire `SetEnabled(cfg.Skills.Enabled)` next to `SetDisabled` at the three
     call sites.
- **Tests**:
  - `skills/loader_test.go`: enabled allowlist keeps only listed embedded
    skills; disabled now filters file-based skills; conflict (both lists) →
    disabled wins; sub-skills of a disabled parent not loaded.
  - `config/config_merge_test.go`: `Skills.Enabled` deep-merge with dedup
    (mirror of the existing `Disabled` merge test).
- **Validation**: `go test ./skills ./config ./internal/app -race`; then
  `go vet`, `staticcheck`, `gocognit`, `gocyclo` (guideline #6, separate runs);
  live check: enable only one skill in a project config and confirm the skills
  banner lists only it (guideline #5).

**Status**: FIXED 2026-08-01 — `skills.enabled` allowlist added (empty = all);
`skills.disabled` now gates ALL sources (embedded + file-based + sub-skills);
disabled wins over enabled on conflict; wired at all three registry build
sites (subsystems/app/helpers). Tests: `TestSkillRegistrySetEnabled`,
`TestSkillRegistryEnabledDisabledConflict`, updated
`TestSkillRegistrySetDisabled` (file-based now gated), `TestDeepMergeSkillsEnabled`.
