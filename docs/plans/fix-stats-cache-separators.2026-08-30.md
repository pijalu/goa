<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<!-- Copyright (C) 2026 Pierre Poissinger -->

# Fix plan — /stats:cache readability: horizontal rules between goal sections, blank lines between sections (bugs.md 2026-08-30)

## Report

The `/stats:cache` report should draw a horizontal rule between each goal
section and include blank lines between sections to make it easier to read.

## Current behavior

`writeCacheView` (`core/commands/stats_cache.go`) emits for every group
`# <alias>` followed immediately by `writeCacheGroupSections`, which writes the
five `##` sections back-to-back with no separators: section headings, tables
and lines abut with no blank line between them, and multi-goal sessions render
one group's `Cache drops` table directly followed by the next group's `#`
header. Raw output (exports, clipboard) is a dense wall; in the TUI goal
sections are separated only by the next `#` heading line.

## Expected behavior

- A `---` horizontal rule between consecutive goal sections (renders as a
  visible full-width faint rule through the MD pipeline).
- A blank line between every section (raw readability; paragraph separation).

## Fix (smallest diff — `core/commands/stats_cache.go` only)

- F1 `writeCacheView`: when rendering more than one group, emit
  `"---\n\n"` before every group EXCEPT the first (no leading rule; no
  trailing rule after the last group).
- F2 `writeCacheGroupSections`: emit a blank line (`"\n"`) before each
  `##` section header after the first, so the five sections never abut.

Single-group sessions keep their current header-less shape; they gain only the
inter-section blank lines.

## Test approach (test-first)

Table-driven additions in `core/commands/stats_cache_test.go`:

- T1 Multi-group layout: two groups → assert `---` appears exactly once,
  between the groups (not leading, not trailing); assert each `##` header is
  preceded by a blank line.
- T2 Single-group layout: no `---` anywhere; blank lines still separate the
  five sections.
- T3 Update `TestStatsCommand_CacheView` / `TestWriteCacheView_FriendlyGoalHeaders`
  expectations if they assert exact section adjacency.

## Validation steps

1. `go test ./core/commands/ -run 'Cache' -count=1 -timeout 30s`.
2. Render the produced markdown through the real MD pipeline (pattern already
   used by `TestWriteCacheMDOutput_RendersThroughMarkdownPipeline`) and verify
   the rule line (`────`) appears between goal sections in the ANSI output —
   actual terminal-visible rendering, per the bugs.md guideline.
3. Full quality gates, each run separately: `go vet ./...`, `staticcheck
   ./...`, `gocognit -over 15 .`, `gocyclo -over 12 .`,
   `go test -count=1 -race -cover ./...`.

## Closure

On green gates: move this plan to `docs/archive/`, empty the bugs.md entry,
commit with a descriptive message.
