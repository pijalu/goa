<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Tool Call Budget & Duplicate Detection Specification

## Overview

The agent protects against runaway tool-call loops using a **three-tier consecutive-duplicate detection** system layered with a **global turn budget**. Every tool call always receives a reply — calls are never dropped silently.

## Key Concepts

### Call Key
Each tool call is identified by a **call key**: `tool_name + "::" + tool_arguments` (full JSON-serialized arguments). This means reading 50 different files with different paths/offsets produces 50 unique call keys — no flag. Reading the same file with the same offset 3 times consecutively produces 3 identical call keys — flagged.

### Consecutive Tracking
The system tracks **consecutive identical call keys** across a turn:
- If the current call key matches the **immediately previous** call key: `consecutiveCount++`
- If the current call key differs from the previous one: `consecutiveCount = 1` (reset)
- A single different call between two identical calls fully resets the streak
  - Example: `read(foo), read(bar), read(foo)` → all three are treated as first-occurrence reads
  - Example: `read(foo), read(foo), read(bar), read(foo)` → third `read(foo)` has count=1 (bar interrupted)

### Sliding Window
A sliding window of the last `MaxToolCalls/10` calls (minimum 1) is maintained for diagnostics/visibility. The window is NOT used for consecutive counting — that uses the dedicated `lastCallKey`/`consecutiveCount` fields.

### Global Turn Budget
`turnToolCallCount` tracks the total number of tool calls in the current turn (all keys combined). This is used for the total `MaxToolCalls` budget.

---

## Three-Tier Response

Every tool call produces exactly one `EventToolResult` — the LLM always receives a reply.

| Consecutive Count | Classification | Result Message | Tool Executed? |
|---|---|---|---|
| 1 | **First occurrence** (or reset) | Normal tool result | ✅ Yes |
| 2 | **Soft repeat** | `toolRepeatedMessage`: *"already executed this turn"* | ❌ No |
| ≥3 | **Hard loop** | `toolLoopMessage`: *"Loop guardrail: repeated too many times"* | ❌ No |

### Budget Exceeded
When the total `turnToolCallCount` exceeds `MaxToolCalls`, ALL further calls in the turn are classified as **budget exceeded** regardless of consecutive count:

| Condition | Classification | Result Message | Tool Executed? |
|---|---|---|---|
| `totalCalls > MaxToolCalls` | **Budget exceeded** | `toolBudgetMessage`: *"tool call budget exceeded"* | ❌ No |

The priority order for classification:
1. Budget exceeded (global total)
2. Hard loop (≥3 consecutive)
3. Soft repeat (2 consecutive)
4. First occurrence (execute normally)

---

## Turn Continuation

After processing a batch of tool calls, the agent decides whether to continue the turn:

| What happened in the batch | Turn continues? | Rationale |
|---|---|---|
| At least one call was executed for real | ✅ Yes | LLM needs to see real results |
| All calls were soft-repeat or hard-loop skipped | ✅ Yes | LLM needs to see the hint and respond |
| Any call was budget-exceeded | ❌ No | Budget message instructs LLM to stop calling tools |

This ensures the LLM ALWAYS sees tool results (real or synthetic) before the turn ends, unless the global budget is exhausted.

---

## Configuration

| Config Field | Purpose | Default |
|---|---|---|
| `MaxToolCalls` | Global per-turn budget (total calls across all keys) | 50 |
| `ToolCallLimitResetWindow` | Sliding window size (max/10 if 0) | `MaxToolCalls / 10` (min 1) |
| `MaxToolRepeat` | Legacy total-repeat guard (non-consecutive) | 0 (disabled) |

---

## Edge Cases

1. **50 unique calls with MaxToolCalls=50**: All 50 execute (each has count=1, total ≤ budget).
2. **50 identical calls with MaxToolCalls=50**: Call 1 executes, call 2 = soft repeat, call 3 = hard loop, call 4+ = budget exceeded.
3. **Alternating calls**: `read(A), read(B), read(A)` — all three execute because B resets the streak.
4. **Budget exactly equal to max**: Call with `totalCalls == MaxToolCalls` executes. `totalCalls > MaxToolCalls` triggers budget.
