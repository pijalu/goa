<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<!-- Copyright (C) 2026 Pierre Poissinger -->

# Archived: TUI colors wrong during streaming/scroll — partial row updates dropped active SGR state

Closed 2026-08-23. Moved from `bugs.md` (entry: "BUG: TUI colors wrong during
streaming/scroll — partial row updates drop active SGR state"). Regression
introduced on `feature/ratatui` by G1 column-range dirty-row emission
(`docs/research/ratatui-tui-enhancements.md` §0.3); first fix attempt `2541649`
guarded only the untouched-suffix side. `main` (full-row emission) was unaffected.

Evidence: `.goa/exports/goa-export-20260823-140941.zip` + `~/dev/term.log`
(raw TTY trace 2026-08-23 14:07–14:13).

## Symptoms (all reproduced, one root cause)

1. Streamed Markdown heading `## What it is` not fully blue — appended text
   rendered default (term.log ~14:09:11).
2. Input top line (titled editor border) half white after a mid-row title change.
3. Arch tree partially colored — partial CUP writes on `│` rows, some missing
   their fg SGR.
4. Tool widget rows partially green during scroll — `\e[2;2HTook 0.04s…`
   emitted with no `\e[48;2;42;50;41m` while the untouched leading cell kept it.

## Root cause

`planTailRow` (`tui/compositor_rowdiff.go`) emitted `cur[p:]` verbatim from the
longest shared byte prefix `p`. A cut landing mid-styled-run (SGR opened before
the cut, still active at that column) did not re-establish that state, and the
terminal pen at the start of a row update is DEFAULT (canvas rows are
style-closed), so repainted cells rendered with default fg/bg next to correctly
colored untouched neighbours. Head mode was already sound (escape-free heads
imply a default pen at the suffix boundary).

## Fix

- `internal/ansi/sgrstate.go` — `SGRStateAt(row, cut) (state, ok)`: replays the
  prefix through `AnsiState` and returns the canonical SGR sequence restoring
  the active attributes at the cut; `ok=false` for anything unmodellable
  (non-SGR CSI, OSC, colon sub-parameters, lone/split ESC) so callers fall back
  to a full-row rewrite. 100% line coverage (`internal/ansi/sgrstate_test.go`).
- `tui/compositor_rowdiff.go` `planTailRow` — prepends the restored state to the
  tail segment; rejects the partial plan when the prefix is unmodellable.
- Gate upgrade: the filmstrip visual-equality suite
  (`tui/compositor_filmstrip_regression_test.go`) now compares per-cell fg/bg
  against a full-repaint reference emulator instead of visible text only (the
  text-only gate was color-blind; TermEmulator models extended colors only, so
  scenarios use truecolor SGRs). New `TestFilmstrip_ColorVisualEquality` covers
  all four symptom shapes; the streaming scenario was switched to truecolor.
- Planner-level regression cases in `tui/compositor_rowdiff_partial_test.go`
  (`TestPlanTailRow_RestoresCutPointSGRState`,
  `TestPlanTailRow_RejectsUnmodellablePrefix`).

## Validation

- Without the fix, both filmstrip gates fail at the exact corrupted cells
  ("streaming step 1 row 17 col 11 fg mismatch: got '', want 38;2;88;166;255";
  "colors step 1 row 19 col 2") — the tests would have caught the regression.
- With the fix: `go vet ./...` clean; `staticcheck` — one pre-existing warning
  (`core/commands/model_test.go` SA1019, untouched by this change); `gocognit
  -over 15` clean for touched packages; `gocyclo -over 12` — one pre-existing
  hit (`tui/goal_list_stream_perf_test.go:154` =13, untouched);
  `go test -count=1 -race -cover ./...` all green (tui 74.8%, internal/ansi
  78.4%, `SGRStateAt`/`sgrSequenceAt` 100%).
- Optimization preserved: `TestCompositor_PartialRowByteReduction` — 54.8%
  emitted bytes saved on the partial path with the state prefix included.

## Known pre-existing edge (not addressed, out of scope)

`ansi.Truncate` drops a row's trailing reset only when visible content exceeds
the terminal width (canvas rows are normally padded to exact width and keep it);
symmetric across full-row and partial emission paths.
