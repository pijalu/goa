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
// (read/write/edit/bash/search/python/ask/goal/run_skill) stay eager.

func (*TerminalsTool) Deferred() bool       { return true }
func (*WebFetchTool) Deferred() bool        { return true }
func (*BGExecTool) Deferred() bool          { return true }
func (*MementoTool) Deferred() bool         { return true }
func (*SmartSearchTool) Deferred() bool     { return true }
func (*SSHBashTool) Deferred() bool         { return true }
func (*SessionSearchTool) Deferred() bool   { return true }
func (*SessionEventReadTool) Deferred() bool { return true }
