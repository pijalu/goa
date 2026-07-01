<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Search for regex patterns in files with parallel execution.

Parameters:
  pattern       (required) string — Pattern to search for (supports regex)
  path          string — Root directory to search (default: project root)
  glob          string — File glob pattern to filter (e.g. "*.go")
  exclude_glob  string — Glob pattern to exclude files (e.g. "*_test.go")
  recursive     boolean — Search subdirectories recursively (default: true)
  case_sensitive  boolean — Case-sensitive search (default: false)
  max_results   integer — Maximum number of results to return
  context_lines integer — Number of context lines around each match
  showing       integer — Content lines per file (default: 10% of max_results, 0 = line numbers only)

Auto-excludes: .git, vendor, node_modules, dist, build
