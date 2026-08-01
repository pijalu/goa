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
