<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Fast parallel regex search across files. This is the PREFERRED way to search
the codebase — use it instead of `bash`+grep/rg for any file/code search. It
auto-excludes .git, vendor, node_modules, dist, build, runs in parallel across
files, and returns ranked, structured output (file → match count → line
numbers → content). Reserve `bash` with grep/rg for features this tool cannot
express (filtering command output, pipes, find -exec, PCRE-only constructs).

Parameters:
  pattern        (required) string — Go RE2 regex (case-insensitive by default)
  path           string — Root directory or a single file (default: project root)
  glob           string — Comma-separated file glob filter (e.g. "*.go,*.ts")
  exclude_glob   string — Glob pattern to exclude files (e.g. "*_test.go")
  recursive      boolean — Search subdirectories recursively (default: true)
  case_sensitive boolean — Case-sensitive search (default: false)
  max_results    integer — Max total matched lines to return
  context_lines  integer — Context lines around each match
  showing        integer — Content lines per file (default: 10% of max_results, 0 = line numbers only)

Output format (per file):
  <path>: N matches
    <line numbers, "/"-separated>
    <lineno>: <matched line content>

Auto-excludes: .git, vendor, node_modules, dist, build
