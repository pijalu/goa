# B30 — Cache-export and context-growth investigation

Closed 2026-10-23.

## Disposition

The export does not demonstrate a provider-cache bust, cache-identity churn,
duplicated tool-result replay, or an output-bound defect. Its low per-request
hit ratios are explained by rapid append-only prompt growth: every completed
request after the cold start reused 98.65%–100.00% of the immediately previous
request's prompt, measured as `cache_read / previous total prompt`.

The investigation did establish a reporting optimization. The cache-report
script previously showed only current-request `hit%`, making this healthy
growth pattern look like a cache failure unless the reader did a separate
calculation. It now also reports `prev_prompt%` and token-weighted session
reuse. Production tool limits, cache identifiers, and history replay were not
changed without evidence of a defect.

## Source and method

The source was `.goa/exports/goa-export-20261023-210438.zip`, extracted to a
scratch directory and inspected read-only. Its SHA-256 before and after the
investigation was:

```text
c4e6dca0fe63dfc6210625a5c61e27bfc306a8b1e8fba6c9078bd9a8be827fc7
```

The required reports were rerun:

```text
python3 tooling/session_cache_report.py /tmp/goa-b30.JsbvNF/session/events.jsonl --full
python3 tooling/session_growth_report.py /tmp/goa-b30.JsbvNF/session/events.jsonl
```

The first command was rerun again after the reporting change. Event/call-ID
analysis also compared all tool calls with their results and with the HTTP
trace's cumulative tool-result counts. The source ZIP was not imported into
the repository or modified.

## Cache evidence

There are ten completed requests with `token_stats`. HTTP request 11 was still
pending at export time and has no token statistics, so it is excluded rather
than guessed about.

| Request | Fresh `prompt_n` | `cache_read` | Total prompt | Current hit | Previous-prompt reuse |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 5,709 | 0 | 5,709 | 0.00% | — |
| 2 | 12,725 | 5,632 | 18,357 | 30.68% | 98.65% |
| 3 | 30,117 | 18,176 | 48,293 | 37.64% | 99.01% |
| 4 | 3,963 | 48,128 | 52,091 | 92.39% | 99.66% |
| 5 | 14,656 | 51,968 | 66,624 | 78.00% | 99.76% |
| 6 | 23,428 | 66,560 | 89,988 | 73.97% | 99.90% |
| 7 | 18,215 | 89,856 | 108,071 | 83.15% | 99.85% |
| 8 | 24,592 | 108,032 | 132,624 | 81.46% | 99.96% |
| 9 | 34,049 | 132,608 | 166,657 | 79.57% | 99.99% |
| 10 | 16,654 | 166,656 | 183,310 | 90.91% | 100.00% |

Totals are 184,108 fresh and 687,616 cached prompt tokens out of 871,724, or
78.88% token-weighted reuse. `cache_read` is monotonic. The report emitted no
`MISS(cold)` after request 1, no `BUST`, and no `SHRINK`; its `GROW` flags agree
with the added fresh context. `logs/cache_miss_requests.json` is exactly `[]`.

The retained context remains consistent with one stable conversation:

- all 11 HTTP records use the same Kimi chat-completions endpoint and
  `k3.1-pro` model;
- all ten agent-log usage lines retain `tools_hash=1605d69c`;
- retained provider chunks have the same `fpv0_24af6fd9` system fingerprint;
- there are no compaction, compression, or context-reset events in the session;
- the largest completed prompt is 183,310 tokens, below the exported
  `k3-1-pro` hard-compression threshold of 25% of its 1,048,576-token window.

These measurements close the cache-bust hypothesis for this export. They do
not prove byte identity. Every retained `requestBody` is only a 1,917–2,051
character tail beginning with `...`, even for the largest prompts. Therefore
no claim is made that adjacent complete bodies were compared byte-for-byte,
and no cache-key defect is inferred from unavailable data. A future request
with an actual cache drop should be diagnosed from the complete miss journal.

The enhanced report now prints the distinction directly. Its final line on the
unchanged export is:

```text
# weighted cache reuse: 78.9% (687616 cached / 871724 prompt tokens; 184108 fresh)
```

For example, request 3 has only 37.6% current-request hits but 99.0%
previous-prompt reuse. That is growth, not evidence of eviction.

## Tool-output growth evidence

`session_growth_report.py` found 53 tool results containing 578,751 characters
(approximately 144,687 tokens by its deliberately rough four-character
estimate):

| Tool | Results | Characters |
| --- | ---: | ---: |
| bash | 7 | 66,413 |
| read | 35 | 364,843 |
| search | 11 | 147,495 |
| **Total** | **53** | **578,751** |

Every result of at least 8,000 characters was correlated with its call input.
The largest were:

| Event line | Tool | Characters | Requested operation |
| ---: | --- | ---: | --- |
| 1095 | bash | 51,419 | `go test -timeout 30s ./...` |
| 1990 | search | 33,599 | broad waiting/elapsed/status regex, `max_results: 300`, `showing: 2` |
| 1097 | search | 30,874 | repository-wide TODO/FIXME regex, `max_results: 200`, `showing: 1` |
| 1445 | search | 24,987 | broad HTTP/read regex, `max_results: 250`, `showing: 1` |
| 566 | read | 22,402 | `bugs.md`, `max_lines: 300` |
| 2028 | read | 19,254 | `tui/tool_execution.go`, `max_lines: 430` |
| 1592 | read | 18,874 | `internal/webui/server.go`, `max_lines: 420` |
| 1691 | read | 18,399 | `internal/webui/terminal.go`, `max_lines: 430` |
| 2025 | read | 17,383 | `internal/app/stats_stream.go`, `max_lines: 360` |
| 1993 | search | 16,976 | scheduler/event regex, `max_results: 300`, `showing: 2` |

The growth is usage-driven rather than an unbounded replay defect:

- The 51,419-character bash result is already bounded output. The command
  produced 171,318 bytes over 2,761 lines; bash retained approximately its
  50-KiB tail, emitted a truncation notice, and saved the full masked output to
  `/var/folders/.../goa-output-2305988694.txt`. Follow-up calls inspected that
  file with a 499-character targeted search and a 9,504-character, 140-line
  read; the full 171-KiB body was not appended again.
- Large searches were explicit broad requests with 200–300 maximum results,
  not the tool's default of 30. Search also caps rendered source lines. Their
  size is the requested result volume, not evidence that a promised bound was
  bypassed.
- Large reads used explicit ranges of 140–430 lines. Read enforces its line and
  byte bounds; 18 results carry line-limit notices. All 35 read inputs and all
  53 rendered results are unique. There is no exact repeat for read-dedup to
  suppress, so enabling or changing dedup would not have reduced this export.
- The exported `tools.max_inline_bytes: 0` disables the optional spill policy,
  but the existing per-tool bounds and agent result-size limit still apply.
  No historical result was pruned or rewritten, consistently with the absence
  of compaction and the monotonically growing cached prefix.

Call/replay correlation found 53 calls, 53 unique result call IDs, no duplicate
result IDs, and no missing results. HTTP `toolResultBlocks` advanced through
`0, 4, 8, 11, 15, 21, 27, 31, 38, 46, 53`, matching cumulative append-only
history. This provides no evidence of duplicate result replay in the retained
request summaries without claiming access to the unavailable complete bodies.

Narrower searches and ranges remain optional usage optimizations, and users
can opt into `tools.max_inline_bytes`. The export does not justify weakening
unique, explicitly requested content or changing those defaults globally.

## Reporting change and regression tests

`tooling/session_cache_report.py` now includes:

- `hit%`: `cache_read / current request total prompt`;
- `prev_prompt%`: `cache_read / immediately previous request total prompt`,
  with `-` when no non-empty previous prompt exists;
- `weighted cache reuse`: all cached tokens divided by all prompt tokens,
  including requests hidden when `--full` is not supplied.

The documentation explicitly says that these ratios do not prove request byte
identity. `tooling/README.md` and `docs/PROVIDER-CACHE.md` describe the new
fields.

The new `tooling/test_session_cache_report.py` covers rapid growth with a low
current hit but full previous-prompt reuse, an actual zero-read bust, cold
start, weighted totals, and a zero previous-prompt denominator.

Test-first reproduction before implementation:

```text
python3 -m unittest discover -s tooling -p 'test_*.py' -v
FAILED (failures=3)
```

All three tests failed against the old report: it had no `prev_prompt%` column
or weighted summary. After the minimal implementation, the same command passed:

```text
test_bust_reports_zero_previous_prompt_reuse ... ok
test_growth_distinguishes_current_hit_from_previous_prompt_reuse ... ok
test_zero_previous_prompt_has_no_reuse_denominator ... ok
Ran 3 tests in 0.002s
OK
```

`python3 -m py_compile tooling/session_cache_report.py
tooling/session_growth_report.py tooling/test_session_cache_report.py` also
passed. Running the enhanced report against the real export reproduced every
rounded measurement in the table and the 78.9% weighted summary above.

## Repository quality gates

Each required command was run separately after the reporting change:

- `go vet ./...` — passed with no diagnostics.
- `staticcheck ./...` — passed with no diagnostics.
- `go test -count=1 -race -cover -timeout 180s ./...` — passed all packages in
  55.77s.
- `gocognit -over 15 .` — exit 1 only for the same unrelated pre-existing
  findings recorded by B29: `core.(*GoalDriver).Drive` 21,
  `core.(*AgentManager).executeRunner` 16, and `app.initGoalSystem` 16.
- `gocyclo -over 12 .` — exit 1 only for unrelated pre-existing
  `core.(*GoalDriver).Drive` 13.

The change adds no Go function and introduces no new complexity finding.
`git diff --check` passed. No server, browser, live-model process, or external
provider request was started, so no runtime cleanup was required. No TUI code
changed; the terminal-validation requirement belongs to B29 and is recorded in
`b29-multi-tool-waiting-elapsed.2026-10-23.md`.

## Residual diagnostic limit

This disposition is specific to the retained export. Provider-side eviction or
request divergence outside these ten completed requests cannot be ruled out
retroactively. The evidence supports closing this report as healthy prefix
reuse plus usage-driven growth, while the improved report makes any future
low-current-hit/high-previous-reuse pattern distinguishable without manual
arithmetic.
