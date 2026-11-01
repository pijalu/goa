# B44 — Manual `/compress:summarize` ignored, compaction lost on session restore

Closed: 2026-11-01

## Report

Two defects around manual summarize compaction:

1. **Explicit summarize hijacked by fresh_window.** `compressHistoryWith` routed
   the `summarize` case to `Compact`, which resolves its escalation slot from
   the hard-layer strategy. With `fresh_window.enabled` on — or a hard layer
   named `fresh_window` — the Phase 2b.3 ordering in `tryPreferredCompaction`
   upgraded the compaction to the zero-LLM window reset: a manual
   `/compress:summarize` silently dropped the conversation instead of
   summarizing it (the summarize provider was never called).
2. **Compaction lost on session restore.** The durable compaction provenance
   triple (compaction_start → summary → end) persisted to the session JSONL
   carried only shadowed-range/token facts — no strategy, no summary text, no
   replacement transcript — and `EventsToHistory` ignored the triple entirely.
   Restoring a compacted session rebuilt the FULL pre-compaction transcript:
   the checkpoint replacement was lost and the context silently grew back.

## Resolution

**Slot pinning.** `compactOrdered` gained an `explicitSummarize` flag.
`compressHistoryWith` routes the explicit summarize request through
`compactOrdered(ctx, summarize, true)`: the fresh_window upgrade is skipped
regardless of the gate or the slot name — an explicit summarize must produce an
LLM summary — while remote compaction (a server-side summarize) may still serve
it. Automatic compaction (`Compact`, escalation layers, overflow recovery)
keeps the previous semantics: the fresh_window gate still upgrades the default
summarize slot, so nothing changes for the automatic paths.

**Replayable transactions.** `CompactionTx` gained three additive replay fields
(all `omitempty`, backward compatible): `Strategy` (summarize | fresh_window |
remote_compact), `Summary` (the fitted summary text), `PreserveTurns` (the
fresh-window tail length), and `Replacement` (the server-produced transcript
for remote compaction). Every completed compaction now persists its replay
payload on the compaction_summary event.

`EventsToHistory` honors a completed transaction: a compaction_summary with a
known strategy replaces the shadowed transcript with the checkpoint the live
compaction landed — summarize: the framed checkpoint pair
(`frameCompactedSummary` user turn + raw summary assistant turn); fresh_window:
the chain-safe preserved tail via the same `findCompressionBoundary` the live
reset used; remote_compact: the persisted replacement verbatim. Post-compaction
turns append after the checkpoint. Legacy triples without the fields and
failed attempts (end with an error, no summary) leave the transcript untouched
— exactly the pre-B44 behavior.

## Validation

- RED tests (failed before the fix): manual summarize with the gate on called
  the summarize provider 0 times and reset the window; manual summarize with a
  hard layer named fresh_window behaved identically; replay of a completed
  summarize triple rebuilt the full transcript; fresh_window and remote_compact
  triples likewise ignored.
- GREEN: both bypass tests pass (summarize provider called once, checkpoint
  pair landed, EventCompact not labelled fresh_window); replay tests produce
  the checkpoint pair / preserved tail / replacement plus post-compaction
  turns; guards pin the remote-serves-summarize path, the automatic
  fresh-window upgrade, failed compactions, legacy sessions, and the JSONL
  round trip of the new fields.
- `go vet ./...`, `staticcheck` on the touched packages, complexity budgets,
  and the full race-enabled repository suite pass; the only gocognit/gocyclo
  reports are the documented pre-existing hotspots unrelated to this change.
