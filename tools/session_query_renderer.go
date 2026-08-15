// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/tuirender"
)

// SessionSearchRenderer renders session_search calls and results.
type SessionSearchRenderer struct{}

var _ tuirender.ToolRenderer = (*SessionSearchRenderer)(nil)

// RenderCall renders the session_search tool call in a human-readable format.
func (r *SessionSearchRenderer) RenderCall(args map[string]any, ctx tuirender.RenderContext) string {
	query, _ := args["query"].(string)
	var b strings.Builder
	b.WriteString(rToolTitle("session_search"))
	if query != "" {
		b.WriteString(" ")
		b.WriteString(rAccent(query))
	}
	if ids, ok := args["session_ids"].([]any); ok && len(ids) > 0 {
		fmt.Fprintf(&b, " in %d session(s)", len(ids))
	}
	if n, ok := args["max_results"].(float64); ok && n > 0 {
		fmt.Fprintf(&b, " max:%d", int(n))
	}
	return b.String()
}

// RenderResult renders the search result output.
func (r *SessionSearchRenderer) RenderResult(output string, ctx tuirender.RenderContext) string {
	if output == "" {
		return ""
	}
	return rToolOutput(output)
}

func (r *SessionSearchRenderer) PreviewLines() int             { return 10 }
func (r *SessionSearchRenderer) HideResultWhenCollapsed() bool { return false }

// SessionEventReadRenderer renders session_event_read calls and results.
type SessionEventReadRenderer struct{}

var _ tuirender.ToolRenderer = (*SessionEventReadRenderer)(nil)

// RenderCall renders the session_event_read tool call.
func (r *SessionEventReadRenderer) RenderCall(args map[string]any, ctx tuirender.RenderContext) string {
	sessionID, _ := args["session_id"].(string)
	seq, _ := args["seq"].(float64)
	var b strings.Builder
	b.WriteString(rToolTitle("session_event_read"))
	if seq > 0 {
		fmt.Fprintf(&b, " seq %d", int(seq))
	}
	if sessionID != "" {
		fmt.Fprintf(&b, " in %s", shortenSessionID(sessionID))
	} else {
		b.WriteString(" (current)")
	}
	if before, ok := args["before"].(float64); ok && before > 0 {
		fmt.Fprintf(&b, " -%d", int(before))
	}
	if after, ok := args["after"].(float64); ok && after > 0 {
		fmt.Fprintf(&b, " +%d", int(after))
	}
	return b.String()
}

// RenderResult renders the event read output.
func (r *SessionEventReadRenderer) RenderResult(output string, ctx tuirender.RenderContext) string {
	if output == "" {
		return ""
	}
	return rToolOutput(output)
}

func (r *SessionEventReadRenderer) PreviewLines() int             { return 15 }
func (r *SessionEventReadRenderer) HideResultWhenCollapsed() bool { return false }

// shortenSessionID trims a long session id for display, keeping the
// timestamp prefix which is the human-meaningful part.
func shortenSessionID(id string) string {
	if len(id) <= 20 {
		return id
	}
	if idx := strings.Index(id, "_"); idx > 0 && idx < len(id)-4 {
		return id[:idx] + "…"
	}
	return id[:17] + "…"
}
