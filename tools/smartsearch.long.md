<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Search for relevant code files using BM25Okapi relevance ranking.

Unlike the regex-based search tool, smartsearch accepts natural language
queries and returns files ranked by their topical relevance. It builds and
maintains a persistent BM25 index under .goa/smartsearch/ that refreshes
incrementally as files change.

Parameters:
  query       (required) string — Natural language query describing what
                you're looking for (e.g. "user authentication middleware")
  glob        string — File glob pattern to filter results (e.g. "*.go")
  path        string — Root directory to search (default: project root)
  max_results integer — Maximum number of results to return (default: 20)
  min_score   float   — Minimum relevance score threshold (0.0–1.0)

Best for:
  - Finding code by what it does, not by an exact pattern
  - Broad concept queries ("database migration", "error handling")
  - Exploring unfamiliar codebases

For exact pattern matching (regex, function names, variable lookups),
use the search tool instead.

Auto-excludes: .git, node_modules, vendor, .goa, dist, build, .venv,
__pycache__, and hidden directories.

Index is stored at .goa/smartsearch/index.gob and is automatically
refreshed when files change via edit/write tools.
