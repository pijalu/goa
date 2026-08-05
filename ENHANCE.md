# ENHANCE.md — Lowering Unexpected Token Usage

## Executive summary

**Problem.** Goa sessions run on an append-only context: every byte added (file reads, tool output,
bookkeeping) is re-read on *every subsequent turn*. Two debug exports quantify the result:

- **Session A** (`goa-export-20260804-194712.zip`, z.ai/glm-5.2, "Sudden cost explosion"): 379 real
  calls in 104 min → **557K fresh + 126K out + 64M cache-read tokens**; ended at the provider's
  5-hour quota (429).
- **Session B** (`goa-export-20260805-104318.zip`, kimi/k3-256k, "Unexpected stops"): model ended a
  turn mid-plan ("…Let me check these.") with zero tool calls and no error — a detection gap,
  **not** a connection error (all HTTP 200, clean `finish_reason=stop`).

**Mental model (read before the proposals).**

1. **Fresh tokens are expensive; cache-read tokens are cheap.** Session A averaged ~1.5K fresh vs
   ~170K cached per turn. The bill is made of fresh tokens.
2. **A cache bust converts cheap into expensive.** When the provider cache expires (server-side
   TTL, e.g. after a 44-min idle), the next turn re-charges the whole context as fresh: +138K in
   one turn (bust #1), +56K (bust #2). Cache state **cannot be probed in advance** — it is only
   visible in the response's `cache_read`.
3. **Compaction always costs one bust.** Any context rewrite changes the prefix → next turn is
   fresh-charged. "Compact when cold" cannot work: after a bust the cache **re-warms within one
   turn** (9s in Session A: `cache_read=0 → 138,176`), so you would pay the cold-cache bust *and
   then* the compression bust — **double fresh spend in two turns**. Compaction is justified only
   when *measured dead content* outweighs the one-time cost, and the user approves the spend.
4. **The 429 is normal provider pacing, not the enemy** (window resets; Session A recovered by
   switching models). The enemy is avoidable fresh spend and dead-content re-reads.

**Where the waste was (Session A).** ~25% of the 64M cache-read bought nothing: duplicate file
reads (`registry.go` ×10) ~5–6M, stale reads after edits ~4–5M, one 12.5K-token truncated bash blob
re-read 195× (~2.4M), goal bookkeeping (~2.6M). Plus a usage.db double-logging bug inflating *all*
metrics ~2×.

**Proposals at a glance** (details E1–E7 inside; priority table at the end):

| Area | One-liner | Saves / fixes |
|---|---|---|
| **E1** Tool safeguards | read-once dedup guard · progressive bash cap · 8KB inline head · stale-read tagging | ~10–14M re-reads, zero bust risk |
| **E2** Bust observability | post-hoc bust notice + `/usage` bust stats (prediction impossible) | visibility |
| **E3** Spend display | fresh-vs-cached per-turn ratio + session fresh accumulator | visibility |
| **E4** Measured-waste compaction | waste-meter → user-gated zero-LLM prune; never auto, never cache-gated | ~10M+ when fired (costs one bust) |
| **E5** Bookkeeping | batch goal-tool chatter; dedupe status spam | ~2.6M |
| **E6** usage.db dedupe | one row per turn | metrics integrity (~2× today) |
| **E7** Premature stop | `/continue` escape hatch (zero parsing); optional narrow reorder of existing checks | UX correctness |

**Explicitly rejected:** predictive cache-state compaction (cache not probeable) · compact-when-cold
(double cost) · 429 avoidance (normal pacing) · deeper message-parsing heuristics for premature stop
(fragile — escape hatch instead).

---

## Design constraints (accepted)

1. **Cache state is not observable pre-request.** The only cache signal available is `cache_read`
   in the *response* — a bust can only be detected **after** it happened. Therefore:
   - No predictive "compact while cache is cold" is possible. Any preemptive compaction **causes**
     the very bust it tries to avoid.
   - "Compact when cold" cannot work: after a bust you pay the fresh re-read, and the busting
     request itself **re-populates the cache** (Session A: bust at 19:01:45 → next turn 9s later
     `cache_read=138,176`, warm again). Compressing then busts *that* cache → **double fresh spend
     in two turns** (full-context bust + compacted-context bust).
2. **Fresh tokens are the expensive currency; cache-read tokens are cheap.** The bust and the quota
   window are **unrelated** phenomena: busts convert cheap cached re-reads into expensive fresh
   input; the 5h-window 429 is the provider's normal pacing and is **acceptable/expected** (Session
   A recovered fine by switching models). The optimization target is therefore **minimizing fresh
   spend** (redundant additions + busts), not avoiding 429s.
3. **Compaction cost is a sensitive item with a 1M window.** Compacting at 300K costs ≈ one full
   fresh re-read of the compacted context + the summarizer call. With `AutoMax` windows,
   UsagePercent (30% at 300K) is meaningless as a trigger — compaction must be justified by
   **measured dead content vs. its future re-read cost**, and gated on explicit user approval.

---

## Findings recap (Session A, quantified)

| Waste source | Content added | Re-read tax (append-only context) |
|---|---|---|
| Duplicate file reads (18/32 files read 2–10×) | 34.5K tok redundant | ~5–6M tok |
| Stale reads (39 copies dead after edits) | 33.8K tok | ~4–5M tok |
| 50K truncated bash blob (3.47MB output) | 12.5K tok, one call | ~2.4M tok (195 turns) |
| `goal` tool bookkeeping (44 calls) | 17K tok | ~2.6M tok |
| usage.db double-logging | — | inflates all metrics ~2× |
| Idle > provider cache TTL (44 min) | 138.2K fresh in ONE turn | bust #1 |
| Partial bust at 19:37 (no client change) | 56.5K fresh in one turn | bust #2 |

**~22–25% of the 64M cache-read bought nothing.** The rest was "read once, re-read 280×" —
structural, not behavioral.

---

## E1 — Tool output safeguards (bash cap / read limit) — *top priority*

Current caps (`tools/common/truncate.go`): `DefaultMaxLines=2000`, `DefaultMaxBytes=50*1024`.
Readfile (`tools/readfile.go`): default 500 lines / 50KB, max 4096 lines.

**Problem:** the caps are *per-call* only. A 12.5K-token truncated blob entered context once and was
re-read 195 times (2.4M tokens). No guard considers *cumulative* or *context-lifetime* cost.

Proposals:

1. **Progressive bash cap.** Lower `DefaultMaxBytes` for long sessions: e.g. 50KB for the first N
   calls, dropping to 16KB once context exceeds a configurable token count. Rationale: late-session
   outputs cost N× their size in re-reads. Config: `tools.bash.max_output_bytes`, new
   `tools.bash.context_aware_shrink: true`.
2. **Re-truncation on overflow, not just on capture.** When output exceeds the cap, the current
   "Full output saved to: <path>" mechanism is right — but the *truncated head* (50KB) is still
   huge. Cap the inline head at ~8KB and always point to the file; the model can `read` exact
   ranges if needed.
3. **Read-once guard (dedup).** Track `(path, mtime, size)` of files already read this session
   (in-memory, per-agent). On a repeated `read` of an unchanged file, return a short notice
   ("unchanged since turn N, <path>; use start_line/end_line for a specific range") instead of the
   full content — or return only the requested range with a dedup marker. Kills the 34.5K redundant
   additions (registry.go read 10×, tui.go 6×, modelsdev.go 6×). No context rewrite → **no bust
   risk**. Must be bypassable (`force: true`) and visible in the TUI renderer.
4. **Stale-read tagging on edit.** After `edit(path)`, mark prior read results of `path` as stale
   in session metadata. Do **not** rewrite history (bust). Use the tags for: (a) a one-line hint to
   the model ("note: earlier reads of <path> predate edits"), and (b) input to E4's selective prune.

Expected saving (Session A replay): ~10–14M cache-read tokens (~20%).

## E2 — Bust observability (post-hoc only — cache cannot be checked in advance)

**Problem:** both busts were pure provider-side TTL expiries — the forensics journal proved the
request prefix was byte-identical (224/224 messages, same tools/system). Bust #1 followed a
**44-minute idle**; bust #2 hit at 19:37 with no client change. Per constraint #1, none of this is
predictable; only post-hoc accounting is possible.

Proposals:

1. **Bust notification (factual, after the fact).** When the forensics journal records a bust
   (`prev_cache_read > 0 → cache_read ≈ 0`), emit one INFO line + TUI notice: "provider cache bust:
   ~X tokens re-charged as fresh (likely TTL expiry after NN min idle)". Teaches the user the real
   cost driver without pretending we can predict it.
2. **Record busts in usage analytics.** The journal (`cache_forensics.go`) already captures busts;
   surface a per-session bust count + fresh-token cost in `/usage`, separate from quota usage
   (constraint #2: bust ≠ quota).
3. ~~Idle-bust prediction / cache warm-up~~ — dropped: cache state cannot be probed pre-request,
   and warm-up traffic would itself spend fresh tokens.

## E3 — Spend observability (429 is normal pacing, not an error)

**Reframed per constraint #2:** the 429 was acceptable — the provider's window resets, and Session
A recovered by switching models. The goal is not to *prevent* 429s but to make the **fresh-token
spend** (the expensive part) visible so the user can decide.

Proposals:

1. **Cost-per-turn display.** Footer already shows ctx%; add rolling "fresh K tok/turn" and
   "cache K tok/turn" averages. In Session A: ~1.5K fresh vs ~170K cached per turn — the ratio is
   the health metric.
2. **Fresh-spend session accumulator.** Total fresh tokens this session (incl. bust events from
   E2), shown in `/usage` next to the cached totals. Fresh is what the bill is made of.
3. ~~Burn-rate monitor / degrade ladder~~ — dropped: quota pacing is the provider's job and the
   429 is acceptable.

## E4 — Measured-waste compaction (user-gated; always costs one bust)

Per constraints #1–#3 there is **no cache-state trick that makes compaction free** — and no
"cold window" to exploit (post-bust the cache re-warms in one turn; compacting then = double fresh
spend). Every compaction costs exactly one bust of the compacted size + the summarizer call. The
only honest trigger is economic, from *measured* dead content (E1.4 tags), not cache state and not
UsagePercent:

```
compact_iff:  fresh_cost(compacted_ctx) + summary_call
            <  dead_content_bytes × expected_remaining_turns × cache_read_price
```

Proposals:

1. **Waste-meter, not cache-meter.** Track tagged dead bytes (stale reads E1.4, oversized blobs
   E1.1/2, resolved goal chatter E5). When `dead_bytes ≥ threshold` (default ~50K tokens,
   configurable), show a one-line TUI proposal: "~X K dead tokens being re-read every turn — compact
   now? costs ~Y K fresh". User confirms; never auto-fire on 1M windows.
2. **Zero-LLM soft tier first** (existing `tierSoft` in `compression_thresholds.go`): prune tagged
   items only — deterministic, no summary hallucination. Full LLM compaction stays manual
   (/compact).
3. **Ignore UsagePercent with AutoMax windows** (constraint #3): a 300K context at 30% usage was
   already ruinous per-turn while every percentage trigger slept. Absolute dead-bytes is the only
   meaningful unit.

## E5 — Reduce bookkeeping chatter

- **`goal` tool:** 44 calls / 17K tokens in one session (todo transitions resurface each turn).
  Batch goal status into a single system-note per turn instead of per-transition emissions; strip
  resolved items after N turns. Est. saving: ~2.6M re-read in Session A.
- **Duplicate `handleToolCall` status spam** (Session B log: 13 identical "Calling bash…" status
  lines in 0.4s): dedupe identical status transitions before they reach the event pipeline
  (cosmetic, but it also pollutes logs used for forensics).

## E6 — usage.db double-logging bug (metrics integrity)

Every turn in Session A was written **twice** (identical values, +0s; 337 of 379 distinct calls
duplicated). Only one call site exists — `recordTurnUsageLocked` (`internal/app/stats.go:836`),
triggered from `handleTokenStats`. Likely two `token_stats` emissions per turn reach the handler
(mid-turn + final, or re-emission on the UI event path).

- **Fix:** dedupe at the sink — `recordTurnUsageLocked` should no-op when `(promptN, predictedN,
  cacheRead, cacheWrite)` equal the previously recorded values within the same turn id.
- **Why it matters:** all quota/burn analytics (E3) and this very investigation read ~2× inflated
  numbers. Real Session A totals: 379 calls, ~557K fresh, ~126K out, ~64M cache-read — not the
  doubled 716/1.1M/241K/122M.
- Add a regression test: one turn → exactly one `usage_events` row.

## E7 — Premature-stop blind spot (Session B root cause)

**Observed:** k3-256k emitted `finish_reason=stop` after text ending "…**Let me check these.**" —
a stated intent to call tools, then **zero tool calls**. Round ended cleanly (EventEnd, no error,
session idle). All HTTP 200s; not a connection error.

**Why the existing auto-continue did NOT trigger (verified in code):**

- `prepareTurn()` (`agent_streaming.go:1499`) resets `turnHadToolExecution=false` **once per turn**,
  and `executeBufferedToolCalls()` sets it `true` when a real tool runs (`agent_budget.go:477`).
  Session B turn 10 executed bash in round 2 ("Re-streaming after tool call (round 2)"), so the
  flag **was true** — the guard was reached.
- `contentBuf` is likewise turn-scoped, so `looksTruncated()` saw the full text ending in
  "…Let me check these."
- `looksTruncated()` (`agent_streaming.go:705`) checks **terminal punctuation first**: a trailing
  `.` returns `false` ("complete") immediately, *before* the intent-phrase list ("let me", "I'll",
  …) is ever evaluated. The punctuation gate — added after a 2026-08-04 false positive — over-corrected.

**On avoiding message parsing:** agreed that deeper text heuristics are fragile. But note that
without *any* content signal, "stop after tool work" is structurally indistinguishable from a
legitimate final answer (both: `finish_reason=stop`, no tool calls) — an always-continue rule would
burn a round on every good answer. Realistic options, least-parsing first:

1. **Escape hatch only (zero parsing, recommended):** when a turn with tool work ends, the TUI
   already shows the answer; add a persistent one-key "continue" affordance (and `/continue`) so the
   user recovers in one keystroke. No heuristics, no false positives, no wasted rounds.
2. **Narrow reorder (one-line change, minimal parsing):** in `looksTruncated`, evaluate the
   existing intent-phrase list *before* the punctuation gate **only when `turnHadToolExecution`**.
   Fixes this exact case; keeps the punctuation fast-path for no-tool turns where the false
   positive originally happened. Regression test in `agent_premature_stop_test.go`:
   content `"…Let me check these."` + `turnHadToolExecution=true` → `shouldAutoContinue()==true`.
3. ~~Sentence-level parsing / larger intent windows~~ — rejected: more parsing for diminishing
   returns.

---

## Priority / effort

| # | Proposal | Est. saving (Session A) | Effort | Bust risk |
|---|---|---|---|---|
| E1.3 | Read-once dedup guard | ~5–6M | M | none (no rewrite) |
| E1.1/2 | Bash progressive cap + 8KB inline head | ~2.4M+ | S | none |
| E6 | usage.db dedupe | metrics integrity | S | none |
| E7.1 | /continue escape hatch | UX correctness | S | none |
| E3.1/2 | Fresh-vs-cached spend display | visibility | M | none |
| E2.1/2 | Bust notification + /usage bust stats | informed user | S | none (post-hoc) |
| E1.4 | Stale-read tagging | enabler for E4 | M | none |
| E4 | Waste-meter + user-gated soft compact | ~10M+ when fired | L | always busts — user decides |
| E7.2 | Intent-before-punctation reorder (tool turns only) | UX correctness | S | none |
| E5 | Goal chatter batching | ~2.6M | M | none |

*Verification data: `~/.goa/usage.db` (dedup query in `.goa/exports/session-1785858876-turn-stats.csv`),
forensics journals in both exports, event streams `session/events.jsonl`.*
