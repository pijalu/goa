# Fix plan — /stats:cache report (bugs.md 2026-08-30)

## Evidence

Export `goa-export-20260830-084611.zip` (frigolite, LM Studio-style provider,
OpenAI branch: `prompt_n` = uncached tokens, `cache_read_tokens` = cached
prefix; no cache-write field). 865 token_stats events. The cache largely
worked (most calls 99%+); events 477 / 725 / 851 are genuine busts (e.g.
read 239,531 → 3,841 after a goal context reset).

## Root causes

1. **Dual-series divergence (the contradictions).** `writeCacheGlobalStatistics`
   computes missed tokens from the per-API-call completion log, but the CM
   counters, the misses table and the drops table scan the per-turn series.
   Turn records keep only the LAST call's usage (`RecordTokenStats`
   overwrites; `agentmanager_events.go` "last call wins"), so intra-turn busts
   are invisible to turn-based scans: the headline reported "3,713 missed
   across 1 exchange (0 full, 1 partial)" while the same report showed
   `CM 0-0`, "No cache misses detected." and "No cache drops detected." —
   despite a 99.97%→1.63% collapse in the completion log.
2. **Misleading session total (the "1.81%").** The token-weighted total ran
   over last-call-wins turn snapshots and was labeled "over N turns": for a
   goal group with one (multi-call) turn whose snapshot was a context-reset
   bust, the whole section displayed that single call's 1.81%. The provider
   cached 99%+ elsewhere — the figure was not token-weighted over traffic and
   read as "no cache at all".
3. **Inverted heading hierarchy.** Group headers rendered `## main ·
   sunny.toad` while their sections rendered `# Last 10 exchanges` etc.
4. **Completion-only groups skipped Global statistics.** The section gate
   checked `cacheActiveTurns(g.turns)` only; a group created from the
   completion log (turn series empty after a history reset) rendered nothing.
5. **In-progress turn snapshot mis-grouped.** `CurrentTurn()` stamps no
   AgentRole/GoalID, so mid-turn the snapshot joined the unlabeled "main"
   group while its calls joined "main · goal" (double-count across groups).
6. **Cache-less provider indistinguishable from a healthy cache.** A group
   with LLM traffic but zero cache tokens rendered no Global statistics and
   "No cache misses detected." — misleading when nothing was ever cached.

## Fixes (smallest diff; `core/commands/stats_cache.go`, `core/turnrecorder.go`)

- F1 Single source of truth: `cacheGroupSeries(g)` = completions when
  non-empty, else turns (legacy sessions). Session total, missed-tokens
  headline, CM counters, misses table, drops table and last-10 all read it.
- F2 Weighted session total over LLM-active calls of the unified series —
  bust rounds dilute the rate, matching the footer fold
  (`foldCacheHitGlobalLocked` weights every prompt-side round). Label
  "token-weighted over N LLM calls" (legacy turns fallback keeps "turns").
- F3 Per-turn table aggregates each turn's own calls when completions exist:
  kT = Σ(prompt+read+write), CH% = CacheHitPct(Σread, Σwrite, Σprompt), CM =
  cumulative unified-scan counters at end of turn.
- F4 Headings: group `#`, sections `##`; "Cache drops" section always headed
  (symmetric with misses).
- F5 Global statistics gate on the unified series; when the group made LLM
  calls but the provider reported zero cache tokens, print an explicit
  "no prompt-cache activity" line instead of silence.
- F6 `TurnRecorder` remembers the current turn's (role, goalID) from
  `RecordCompletion` and stamps them on `CurrentTurn()` snapshots.

## Test approach (test-first)

Regression tests in `core/commands/stats_cache_test.go` +
`core/turnrecorder_test.go`:

- T1 Multi-call bust consistency: one turn, calls warm→bust→recovery ⇒ CM
  counters, misses table, drops table and headline ALL report the bust
  (today: only the headline does). This is the exact reported contradiction.
- T2 Heading levels: `# group`, `## sections`, drops header always present.
- T3 Weighted total includes bust calls and is labeled "over N LLM calls".
- T4 Per-turn row aggregated from a multi-call completion log.
- T5 Completion-only group renders Global statistics.
- T6 LLM-traffic-but-zero-cache group prints the no-cache line.
- T7 `CurrentTurn()` carries role/goal tags (core/turnrecorder_test.go).
- Legacy turns-only paths stay green (existing tests updated only where the
  series source legitimately changed numbers/labels).

## Validation steps

1. `go test -count=1 -race -cover ./core/... ./internal/app/...` then the
   full `./...` suite (30s timeout).
2. Quality gates run separately (no chaining): `go vet ./...`,
   `staticcheck ./...`, `gocognit -over 15 .`, `gocyclo -over 12 .`.
3. Terminal output: feed the export's real bust sequence through
   `showCacheStats` + the TUI markdown renderer and inspect the rendered
   frame; then run the real binary against the local LM and issue
   `/stats:cache` to verify live output.
4. Move bug + this plan to `docs/archive/`, reduce `bugs.md` to the
   guideline, commit.

## Results (2026-08-30)

- RED first: all new regression tests failed against the old code —
  `TestStatsCommand_CacheView_MultiCallBustConsistency` reproduced the exact
  reported contradiction (headline "7,900 missed (1 partial)" next to
  `CM 0-0`, "No cache misses detected.", "No cache drops detected.",
  "Session total: 100.00% … over 1 turns").
- GREEN: full `go test -count=1 -race -cover ./...` passes; `go vet`,
  `staticcheck`, `gocognit` clean; `gocyclo` reports one pre-existing,
  unrelated warning (`TestRetryConfigSetters`, core/commands/config_test.go
  — test function, untouched by this change; noted per the guideline).
- Live terminal validation: `e2e/mockllm/server.py` extended with a
  deterministic prefix-cache simulation (`prompt_tokens_details.cached_tokens`,
  every-Nth-request bust via `MOCK_BUST_EVERY`, terminal SSE usage chunk);
  the real goa TUI was driven via `e2e/ptydrive` against it and the rendered
  `/stats:cache` frame inspected. Every surface agreed on the bust:
  exchanges T4 4.9% Lost 1,763; headline "Missed cache tokens: 1,763 across
  1 exchange(s) (0 full, 1 partial)"; per-turn `CM 0-1`; misses table T4
  partial 92.8% 1,763; drops T4 79.2%→4.9% Lost 1,763; session total 41.88%
  token-weighted over 4 LLM calls (3685 reads / 8800 prompt-side — exact);
  footer `CM:1` and last-call `▸4.9%` agree. (The footer's global CH uses
  its own documented cached-volume fold — a different, separately pinned
  weighting, not part of this bug.)
