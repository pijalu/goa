<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bug Archive — 2026-07-09

Archived from `bugs.md` after fixing all open items in the 2026-07-09 session.

## Tool call error
**Original:** Session `/Users/muaddib/dev/goatest2/.goa/exports/goa-export-20260709-084809.zip`. The model was unable to correctly call the edit tool and entered a soft-repeat loop.
**Fix:** `tools/editfile.go` now normalizes common literal escape sequences (`\n`, `\t`, `\\`, `\"`, `\'`) in `replace_pattern` patterns. If the normalized pattern spans multiple lines, it is matched as a fuzzy block via the existing `fuzzyEdit` helper before falling back to line-by-line regex/contains matching.
**Tests:** Added `TestEditFileTool_ReplacePattern_EscapedNewlinesAndQuotes` in `tools/editfile_test.go`.
**Validation:** `go test -count=1 -race -cover ./tools/...` passes; code-quality checks pass.

## ask_user_question
**Original:** The `ask_user_question` function let the model supply arbitrary title strings.
**Fix:** `tools/ask/ask_user.go` now overrides every question title to the fixed label "Clarifications needed" and removes the `title` field from the JSON schema so the model cannot waste tokens on it.
**Tests:** Added `TestExecute_TitleNormalized` in `tools/ask/ask_user_test.go`; updated `TestExecute_Series` to assert titles are normalized.
**Validation:** `go test -count=1 -race -cover ./tools/ask/...` passes; code-quality checks pass.

## Orchestrator issue
**Original:** Session `/Users/muaddib/dev/goatest2/.goa/exports/goa-export-20260709-080132.zip`. The orchestration UI mixed tool calls/thinking blocks and presented a complex tabbed layout.
**Fix:** Removed the orchestration tab bar and stats panel from the main layout entirely; the UI is now a normal chat (chat always visible). The `ctrl+x` shortcut opens a steering-target picker listing `all`, orchestrator, and each agent; a numeric key jumps to the target and updates the input prompt (e.g., "steer all", "steer coder"). Per-model stats (tokens in/out, cache hits) are shown in the footer below the input line, with no aggregate sum.
**Tests:** Updated `tui/orchestrator` tests (`view_test.go`, `view_agent_tab_test.go`, `view_tab_order_test.go`, `tabbar_test.go`, `tabpicker_test.go`) and `internal/app` orchestrator view tests (`orchestrator_adapter_integration_test.go`, `orchestrator_conversation_render_test.go`, `orchestrator_per_agent_pane_test.go`, `orchestrator_per_agent_tab_test.go`, `orchestrator_tabs_filmstrip_test.go`, `orchestrator_view_forwarder_test.go`) to reflect the no-tabs, picker-based, footer-stats layout.
**Validation:** `go test -count=1 -race -cover ./tui/orchestrator/... ./internal/app/...` passes; full `go test -count=1 -race -cover ./...` passes; code-quality checks pass.

## Orchestrator follow-up (2026-07-09 export goa-export-20260709-120318.zip)
**Original:** After the UI simplification, the orchestration session showed several remaining issues: footer cache-hit column stuck on "-"; edit tool accepted `operation: "replace"` in schema but rejected it at runtime; duplicate tool-call retries rendered as green success; thinking blocks merged across tool calls; missing-parameter errors from edit calls; delegate created a new agent profile for every sequential delegation (`coder`, `coder·2`, `coder·3`); screen tearing when the footer stats line appeared/disappeared.
**Fix:**
- `internal/app/orch_tabs.go` now computes cache-hit percentage via `metrics.CacheHitPct` and renders `CH=XX%` (or `-` only when there is no cache activity).
- `tools/editfile.go` now routes `operation: "replace"` to the same search/replace implementation as `old_string`/`new_string`, and returns a clear error when those fields are missing.
- `internal/agentic/agent_budget.go` exports guardrail prefixes and adds `IsGuardrailResult`; `internal/app/orchestrator_adapter.go` now marks repeated/loop guardrail tool results as not ok so they render as warnings/errors rather than green successes.
- `internal/app/agent_streams.go` now clears `thinkView`/`contentView` in `endSegment()` so post-tool thinking starts a fresh block, and closes the segment after tool results. Label disambiguation is now dynamic, so the base role label is reused when the previous agent of that role has finished.
- `internal/app/orchestrator_adapter.go` now keeps a per-runtime pool of idle non-orchestrator `agentic.Agent` instances. When a specialist handle is released, its agent is returned to the pool and reused on the next `Delegate` to the same role; `agent.Clear()` wipes the previous conversation before reuse and the old observer is removed.
- `tui/footer_render.go` now always renders three footer lines. The third line is a blank spacer when no orchestration stats are active, keeping the footer height constant so the compositor no longer triggers a full redraw when the stats line appears or disappears.
- `tools/editfile.long.md` updated to document the `operation: "replace"` alias and troubleshooting.
**Tests:** Added `TestIsGuardrailResult` in `internal/agentic/agent_budget_test.go`; added `TestEditFileTool_Execute_ReplaceOperation_*` in `tools/editfile_test.go`; updated `TestOrchestratorAdapterEvents_ToolResultErrorStatus` in `internal/app/orchestrator_adapter_events_test.go`; added `TestOrchestratorConversation_ThinkingSeparatedByToolCall` in `internal/app/orchestrator_conversation_render_test.go`; added `TestAgentStreamRegistry_*` in `internal/app/agent_streams_test.go`.
**Validation:** `go test -count=1 -race -cover ./...` passes; code-quality checks pass.
