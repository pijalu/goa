<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bug fix report: tool-call auto-heal missed the whitespace DSML dialect and dropped the call silently

Date: 2026-09-26 · Status: CLOSED

## Symptom (bugs.md `# To fix`)

The model emitted a tool call as TEXT (not a native `tool_calls` block) in a
whitespace variant of the DeepSeek DSML dialect and the session stopped with no
execution and no explanation ("unexpected stop"). Evidence, export
`goa-export-20260926-101445.zip`:

- `logs/agent.log` (~lines 3937-4403) — content deltas carrying
  `<｜｜DSML｜｜ invoke name="goal">`, `<｜｜DSML｜｜ parameter name="action"…>`
  and `</｜｜DSML｜｜ invoke>` (space between the delimiter and the keyword);
- `session/events.jsonl` for that turn — content deltas → `token_stats` →
  `context_stats` → `end` → `progress`: no tool call, no execution and no
  "Decoding tool calls..." progress event;
- `config/project.yaml` + `config/user.yaml` — `auto_heal_tool_calls: true`, so
  healing was ON, not off.

**Expected:** DSML recognition tolerates whitespace after the `｜｜DSML｜｜`
delimiter (detection, parsing, stripping agree); a text tool call that is
recognized but not executed always produces actionable feedback — a notice to
the user naming the tool and durable guidance telling the model to re-issue the
call natively — never a silent drop, and never markup finalized as the answer.

## Root cause

Every DSML recognizer hard-coded the keyword immediately after the delimiter:

- `hasDSMLSignal` matched only `<｜｜DSML｜｜invoke` / `<｜｜DSML｜｜tool_calls>`;
- `dsmlInvokeRE` / `dsmlParamRE` and `dsmlInvokeClose` were equally strict;
- `toolXMLSignals` had no DSML-with-space entry, so `hasToolSignal` was false too.

Both signals false → `tryAutoHealToolCalls` took its "no signal" branch →
`warnUnrecoveredInvokeCall`, which returned immediately when healing was
**enabled**. Exactly the configuration meant to recover the call therefore
produced zero diagnostics: no execution, no user notice, no model guidance, and
the raw markup was finalized as the assistant's answer. The
`len(calls) == 0` path (recognized but unrecoverable) returned silently as well.

## Resolution

1. **One normalization step, shared by every DSSML path**
   (`internal/agentic/toolcallparser.go`): `normalizeDSMLMarkup` collapses
   `<(/?) *｜｜ *DSML *｜｜ *` onto the canonical delimiter, so whitespace inside
   the delimiter (`< ｜｜ DSML ｜｜`) and after it (`<｜｜DSML｜｜ invoke`) are handled
   identically. It is applied by `newToolCallScanner` (used by both
   `parseToolCallsFromText` and `parseDSMLToolCallsFromText`), by
   `hasDSMLSignal`, and by `stripToolMarkup` — detection can no longer be
   stricter than parsing or stripping.
2. **Tolerant patterns as a second line of defence**: `\s*` after the delimiter
   in the invoke/parameter/close patterns, block and orphan strip patterns, and
   a new final-pass pattern for bare delimiter tokens (the export ends with a
   lone `</｜｜DSML｜｜`). `dsmlSignalRE` matches open or close markup.
3. **Never-silent reporting** (`internal/agentic/agent_stream_heal.go`):
   `warnUnrecoveredInvokeCall` became `reportUnrecoveredTextToolCall`. For a
   CLOSED invoke-dialect block (DSML, whitespace DSML or Anthropic-legacy)
   naming a REGISTERED tool, it now
   - strips the markup from the stream buffers, so it can never be finalized as
     the answer;
   - emits a warning that differentiates "healing is off → enable
     auto_heal_tool_calls" from "healing is on but recovery failed → re-issue as
     a native tool call";
   - injects ephemeral system guidance ("Re-issue that call now as a native tool
     call…") so the next round fixes the call instead of the user having to type
     "continue";
   - fires at most once per turn (`Agent.callDroppedReported`, reset with
     `autoContinueCount` at turn start).
   Both the signal-miss branch and the recognized-but-unparsed branch
   (`len(calls) == 0`) report. `closedInvokeCallNames` scans both dialects and
   ignores unterminated blocks (a stream may still complete them).

## Test approach & validation

Regression tests (each documents the RED condition with the pre-fix predicate):

- `internal/agentic/toolcallparser_test.go`
  - `TestHasDSMLSignal_WhitespaceDialect` — asserts the pre-fix strict predicate
    does NOT match the export fixture, then that `hasDSMLSignal` does (plus the
    spaced-delimiter variant and the prose negative);
  - `TestParseDSMLToolCalls_WhitespaceDialect` — the export shape recovers one
    call with `action=create`, multi-line `objective`, `freshContext=false` and
    `verifyCommand` intact (asserts the old strict patterns matched nothing);
  - `TestParseToolCalls_DSMLWhitespaceSpacedDelimiter`;
  - `TestStripToolMarkup_WhitespaceDialect`, `TestClosedInvokeCallNames_WhitespaceDialect`
    (closed blocks detected, unterminated blocks ignored).
- `internal/agentic/agent_autoheal_test.go`
  - `TestAutoHeal_WhitespaceDSMLRecoveredWithAutoHealOff` /
    `…WithAutoHealOn` — end-to-end delta replay of the export: the tool executes
    and no markup leaks into the output;
  - `TestUnrecoveredInvokeCall_WarnsWithHealingDisabled` /
    `…_WarnsWithHealingOn` — the report fires in both configurations (the
    healing-on case is the export regression), with the right wording;
  - `TestUnrecoveredInvokeCall_GuidesModelToReissue` — the model gets the
    re-issue guidance in history;
  - `TestUnrecoveredInvokeCall_StripsMarkupFromAnswer`,
    `…_ReportsOncePerTurn`, `…_SilentOnProse`;
  - `TestFinalize_StripsOrphanDSMLMarkup` — an unrecovered call is reported and
    its markup never reaches the finalized answer.
- Existing DSML/invoke tests (canonical spelling) unchanged and green — the
  byte-stable dialect did not regress.

Gate (run separately, post-change):

- `go vet ./...` — clean;
- `staticcheck ./...` — clean (the previously noted `probe.go` S1008 is gone);
- `gocognit -over 15 internal/agentic/` — clean;
- `gocyclo -over 12 internal/agentic/` — clean;
- `go test -count=1 -timeout 120s ./internal/agentic/` — ok;
- `go test -count=1 -race -timeout 300s -cover ./internal/agentic/` — ok,
  coverage 88.0%.

## Closure

The whitespace DSML dialect is recovered by the same code path as the canonical
one, and a recognized-but-unrecovered text tool call can no longer disappear
silently: the user is told which tool was dropped and the model is told to
re-issue it. Closed 2026-09-26.
