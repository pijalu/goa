// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

// Deferred marker methods (P1 deferred tool loading): these tools' schemas
// are withheld from the per-request eager block and exposed only through the
// tool_search loader. The model pulls them on demand; before that, calls are
// rejected with a clear redirect error. See docs/plans/context-usage-tool-
// skills-review.md P1.
//
// The set is the "opt-in-heavy" tool family: tools that are rarely needed on
// every request yet still ship multi-hundred-byte schemas. Core tools
// (read/write/edit/bash/search/python/ask/goal) stay eager; heavier optional
// tools, including lsp, verify, and run_skill, load on demand.

func (*TerminalsTool) Deferred() bool        { return true }
func (*WebFetchTool) Deferred() bool         { return true }
func (*BGExecTool) Deferred() bool           { return true }
func (*MementoTool) Deferred() bool          { return true }
func (*SmartSearchTool) Deferred() bool      { return true }
func (*SSHBashTool) Deferred() bool          { return true }
func (*SessionSearchTool) Deferred() bool    { return true }
func (*SessionEventReadTool) Deferred() bool { return true }
func (*VerifyTool) Deferred() bool           { return true }
func (*LSPTool) Deferred() bool              { return true }

// Schedule tools (schedule_create/delete/list): rarely used — reminders are
// created at most a few times per session — yet their schemas ship on every
// request. Deferred per the 2026-08-21 bugs.md feature request; the model
// loads them on demand via tool_search (select:schedule_create,...). This
// deliberately reverses the 2026-08-17 NOT-A-BUG decision that pinned them
// eager.
func (*ScheduleCreateTool) Deferred() bool { return true }
func (*ScheduleDeleteTool) Deferred() bool { return true }
func (*ScheduleListTool) Deferred() bool   { return true }
