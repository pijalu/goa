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
  fetch_id    string  — Fetch a bounded, current chunk by id from a prior result
  start_line  integer — Optional line-range override when fetching
  end_line    integer — Optional line-range override when fetching
  output      string — `text` (default, backwards-compatible) or `json` for agents
  max_tokens  integer — Bound returned evidence (approximately four characters/token)
  context_lines integer — Maximum evidence lines per result
  language    string — Filter by detected language
  kind        string — Filter by semantic chunk kind

Best for:
  - Finding code by what it does, not by an exact pattern
  - Broad concept queries ("database migration", "error handling")
  - Exploring unfamiliar codebases

Each result includes a stable chunk id, language, symbol, exact line range, and
matching evidence. Use `fetch_id` before editing; stale or invalid ids return an
actionable error. JSON output has `query` and `results` objects and is bounded by
`max_tokens`; fetch accepts the same output and budget options.

For exact pattern matching (regex, function names, variable lookups),
  use the search tool instead.

Auto-excludes: .git, node_modules, vendor, .goa, dist, build, .venv,
__pycache__, and hidden directories.

Index is stored at .goa/smartsearch/index.gob and is automatically
refreshed when files change via edit/write tools.
