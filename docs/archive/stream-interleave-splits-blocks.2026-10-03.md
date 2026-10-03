# Archived bug — live stream splits when a provider interleaves reasoning and answer deltas

Moved out of `bugs.md` on 2026-10-03 (guideline 4: closed items live here).
Fixed in `feature/webui` (web UI review, commit `2d647035`).


**Observed.** In the session exported as
`.goa/exports/goa-export-20261003-203241.zip` (session `1791051452_1pwqsa3w`,
`opencode-go` / `deepseek-v4-1-flash`), one assistant turn rendered as two
thinking blocks with a fragmented answer wedged between them:

```
  ▾ thinking...
  ▏F8 done. Let me verify the test detects the old behavior: … Let me run the full suite now


 F


  ▾ thinking...
  ▏.


 8 done. Full suite + gates now.
```

The answer "F8 done. Full suite + gates now." lost its first two characters to a
stray ` F` above a second thinking header, and the reasoning text was split
across two blocks.

**Evidence.** `session/events.jsonl` events 9976–9995 of that export show the
provider interleaving the two channels inside a single turn — `State` 1 is
reasoning, `State` 2 is the answer:

| event | State | Text |
|---|---|---|
| 9976 | 1 | `F` |
| 9977–9990 | 1 | `8 done. Let me verify … Let me run the full suite now` |
| 9991 | 2 | `F` |
| 9992 | 1 | `.` |
| 9993–9995 | 2 | `8 done`, `. Full suite + gates`, ` now.\n\n` |

Both channels are individually complete (`State 1` = the full reasoning, `State 2`
= the full answer); only their **order** alternates. Nothing is lost upstream.

**Expected.** Reasoning and answer each render as one block, in the order the
blocks first appeared, whatever order the deltas arrive in. A late reasoning
delta belongs to the reasoning block that is already on screen, not to a second
one, and must not interrupt the answer.

**Cause.** The live stream state (`internal/app/stats.go` `streamState`) models
**one** active block with **one** text buffer, and
`internal/app/stats_stream.go` `endStreamIfDifferent` closes that block whenever
the incoming delta's state differs from the active kind. The interleaved
timeline therefore closes and re-opens blocks on every alternation:

1. reasoning delta → thinking block opens, `endStreamIfDifferent(StateThinking)`
   sees a match and keeps it;
2. answer delta `F` → `endStreamIfDifferent(StateContent)` closes the thinking
   block and opens an assistant message;
3. reasoning delta `.` → `endStreamIfDifferent(StateThinking)` closes the
   assistant message and `AddThinkingBlock` opens a **second** thinking block;
4. answer deltas → a **second** assistant message, holding the rest of the
   sentence.

Result: two thinking blocks and two assistant messages for one answer, which is
exactly the pasted screen. The single-stream model is only valid for providers
that emit all reasoning before any content; `deepseek-v4-1-flash` does not.

**Fix plan.**

1. Give the live stream **one accumulator per block kind** for the current turn
   (reasoning, answer) instead of one active block: a delta is routed to its own
   accumulator and its own message, so the arrival order of the two channels no
   longer decides the block structure.
2. Route by state, not by "last message": reuse the existing
   `ChatViewport.UpdateLast([]ConsoleItemType{kind}, …)` (the same primitive
   `UpdateLastMessage` is built on) so a late reasoning delta updates the
   reasoning block that is already on screen even when it is no longer the last
   message. No new viewport API is needed.
3. Reset both accumulators on a real turn boundary (tool call/result, idle,
   stream retry, user submit), so the next turn still starts fresh blocks and a
   previous turn's reasoning can never be appended to.
4. Ordering: when the reasoning block does not exist yet but the answer block
   does, the reasoning block is appended after it. That is a cosmetic residue of
   the same interleave and is accepted for now (no insert-at-index API exists on
   `ChatViewport`); the observed case — reasoning first — is already correct.

**Test approach.**

- `internal/app/stats_stream_test.go`: feed one turn of interleaved events
  (`StateThinking` "F", `StateThinking` "8 done…", `StateContent` "F",
  `StateThinking` ".", `StateContent` "8 done…") through the real
  `handleThinkingContent`/`handleAssistantContent` path and assert the chat
  viewport holds exactly **one** thinking block plus **one** assistant message,
  with the full text of each and the reasoning block first. RED before the fix:
  the current code produces two of each.
- A second test for the non-interleaved order (all reasoning, then all answer)
  to pin that the ordinary path is unchanged, plus one for a turn boundary
  (tool result between two turns) proving the second turn's reasoning does not
  merge into the first block.
- Validation on the real symptom: replay the exported session
  (`goa --export-session 1791051452_1pwqsa3w` / the interactive filmstrip) and
  confirm the turn renders as one thinking block followed by the intact answer.

**Status: FIXED** — implemented as planned.

- `internal/app/stats.go` `streamState` now holds one accumulator per block kind
  (`parts`, `append`, `opened`, `kinds`, `resetSegment`).
- `internal/app/stats_stream.go`: `handleThinkingContent`/`handleAssistantContent`
  route each delta through `append`, so a delta always lands in its own block;
  `endStreamIfDifferent` treats only tool call/result/idle as segment
  boundaries; `endCurrentStream` resets the segment; the cancel path retracts
  every in-flight kind (popping while the tail is still one of them —
  `RemoveLast` only inspects the tail, and an empty type list means "any", so
  the loop is length-guarded or it would eat the user's own question).
- Plan step 4 (ordering when the answer arrives before the reasoning) was left as
  documented: `ChatViewport` has no insert-at-index API, and the observed order
  is already correct.

**Test approach — as executed.**

- `internal/app/stream_interleave_test.go` (new) replays the exported event order
  through the production handlers and asserts one reasoning block + one answer,
  both intact and in order; the screen carries exactly one `▾ thinking` header
  (the footer also prints the thinking level, so the bare word is the wrong
  probe); a tool call still starts fresh blocks for the next segment; and a
  cancel retracts both half-streamed bubbles.
- RED was verified: before the fix the same test produced `blocks = [thinking
  assistant thinking assistant]` and a screen with two `▾ thinking` headers and
  a stray `F` — the reported artifact, reproduced exactly.
- `TestHandleStreamContent_ThinkingAndContentAlternate` (pre-existing) asserted
  the buggy behaviour (two thinking blocks on alternation) and was updated to
  the corrected expectation: one block per segment with both fragments joined.
- Gates: `go vet ./...` clean, `go test -count=1 ./internal/app/` green,
  `staticcheck ./internal/app/...` clean, complexity gates clean.

**Validation.** The unit tests above pin the mechanism end to end (event →
handler → chat blocks → rendered screen). Replaying the exported session in a
real terminal remains the interactive confirmation step.
