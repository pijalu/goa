// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"encoding/json"
	"strings"

	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/tuirender"
)

// DelegateRenderer displays delegate_to and request_review calls and their
// results. Before it existed, a delegate_to bubble showed a generic tool box
// with a raw JSON ack — the user could not tell WHICH agent was spawned or
// what task it received, and a silent sub-agent failure looked identical to a
// success (bugs.md: "delegate_to reports success without clear UI feedback").
type DelegateRenderer struct{}

var _ tuirender.ToolRenderer = (*DelegateRenderer)(nil)

// RenderCall renders the delegation header: target agent + task preview.
func (DelegateRenderer) RenderCall(args map[string]any, ctx tuirender.RenderContext) string {
	agent := stringArg(args, "agent")
	if agent == "" {
		agent = "companion" // request_review has no agent param
	}
	label := stringArg(args, "task")
	if label == "" {
		label = stringArg(args, "content") // request_review carries content
	}
	if ansi.Width(label) > 72 {
		label = ansi.Truncate(label, 69) + "..."
	}
	return rToolTitle("⇒ delegate") + rMuted(" · ") + rAccent(agent) +
		rMuted(" — ") + rToolOutput("\""+label+"\"")
}

// RenderResult renders the delegation ack / review result. The async
// delegate_to ack is a small JSON object; surface it as a one-line status
// instead of a raw blob so the user sees the delegation was accepted.
func (DelegateRenderer) RenderResult(output string, ctx tuirender.RenderContext) string {
	out := strings.TrimSpace(output)
	if out == "" {
		return ""
	}
	var ack struct {
		Status  string `json:"status"`
		Agent   string `json:"agent"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(out), &ack); err == nil && ack.Status != "" {
		switch ack.Status {
		case "delegated":
			target := ack.Agent
			if target == "" {
				target = "sub-agent"
			}
			return rToolTitle("✓ delegated") + rMuted(" · ") + rAccent(target) +
				rMuted(" — running in background")
		case "review_complete":
			return rToolTitle("✓ review complete")
		default:
			return rToolTitle(ack.Status)
		}
	}
	return rToolOutput(out)
}

// RenderError is intentionally absent: tool errors already render through the
// generic ToolError path (red ✗ box), so a spawn failure is visibly distinct
// from a success ack without a renderer hook.

func (DelegateRenderer) PreviewLines() int             { return 4 }
func (DelegateRenderer) HideResultWhenCollapsed() bool { return false }
