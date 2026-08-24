<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<!-- Copyright (C) 2026 Pierre Poissinger -->

# Archived: Runaway-loop guardrail false-positives on tool-progress turns

Closed 2026-08-24. Moved from `bugs.md`. Fixed by commit `c0de34a`
("fix(agentic): tool-progress turns no longer trip runaway-loop guardrail").

Evidence: `.goa/exports/goa-export-20260824-092731.zip` (issue.md: goal
auto-paused with `runaway loop detected: the assistant repeated the same
response 3 consecutive times without progress (repeated: "(empty response)")`
while the agent was actively executing different tools/tests each turn).

## Root cause

`internal/agentic/agent_loop_progress.go` fingerprinted an assistant turn via
`hashAssistantMessage` = Content + Thinking + `len(ToolCalls)`, and every
assistant message — including empty ones — was "meaningful". A goal-mode agent
emitting little or no prose while running a different tool each turn hashed to
the same short string every turn; three such turns tripped the latch despite
observable progress.

## Fix

1. Per-tool fingerprint: hash includes each tool call's name plus a stable
   digest of its arguments (JSON key-order normalized via decode/re-marshal,
   truncated sha256; non-JSON falls back to trimmed raw text).
2. Strike gating: when the turn's tool calls produced non-error results, the
   repeat counter resets instead of incrementing — executed tools are
   observable progress even with byte-identical text/thinking/args.
3. Empty-response strikes apply only to truly empty turns (no content/thinking
   AND zero tool calls); prose-only repetition unchanged.
4. `appendToolResults` marks execution outcome authoritatively via
   `metaToolError` Metadata (always present on live results), so outputs that
   merely start with "Error:" are never misread as failures; reloaded history
   falls back to the conventional `"Error:"` content prefix.

## Recreation + tests

The export scenario was replayed against pre-fix HEAD in a throwaway git
worktree with a self-contained test using only pre-fix symbols: it latched at
turn 3 exactly as reported ("runaway loop detected … 3 consecutive times"),
and passed with the fix. Permanent coverage in
`internal/agentic/agent_loop_guardrail_test.go`: fingerprint table (different
tools/args differ, key-order equal, invalid JSON distinct), different-tool
empty turns survive 4+ turns, identical-fingerprint polling counts as
progress, identical prose plus different tools scores no strike, success
marker beats "Error:"-prefixed output, failed-tool repeats still warn then
latch (live + legacy-prefix paths), truly-empty no-tool turns latch after
three, prose-only warn/latch.
