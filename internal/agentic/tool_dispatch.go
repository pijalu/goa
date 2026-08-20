// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import "context"

// ToolDispatcher re-enters the agent's guarded tool pipeline for one nested
// tool sub-call (gap TL7 run_code code-mode dispatch). It is the exact same
// entry point a direct tool call traverses — executeToolWithResult → runTool —
// including guard policy, solo policy, user confirmation, registry lookup, and
// tool execution, so a nested sub-call can never bypass the permission/jail
// path used for direct calls.
type ToolDispatcher func(ctx context.Context, name, input, callID string) (ToolResult, error)

type toolDispatcherCtxKey struct{}

// WithToolDispatcher returns a context carrying a nested tool dispatcher.
// The agent injects it into the execution context of a nested-capable tool
// (run_code) so sub-calls re-enter the complete guarded pipeline.
func WithToolDispatcher(ctx context.Context, d ToolDispatcher) context.Context {
	return context.WithValue(ctx, toolDispatcherCtxKey{}, d)
}

// ToolDispatcherFrom returns the nested tool dispatcher carried by ctx, if any.
func ToolDispatcherFrom(ctx context.Context) (ToolDispatcher, bool) {
	d, ok := ctx.Value(toolDispatcherCtxKey{}).(ToolDispatcher)
	return d, ok
}
