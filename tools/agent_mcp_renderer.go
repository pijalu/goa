// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/tuirender"
)

// AgentMCPRenderer renders agent_mcp tool calls and results for the TUI.
type AgentMCPRenderer struct{}

// NewAgentMCPRenderer creates a new AgentMCPRenderer.
func NewAgentMCPRenderer() *AgentMCPRenderer {
	return &AgentMCPRenderer{}
}

// RenderCall renders the agent_mcp tool call header.
func (r *AgentMCPRenderer) RenderCall(args map[string]any, ctx tuirender.RenderContext) string {
	action, _ := args["action"].(string)
	parts := []string{rToolTitle("agent_mcp"), rAccent(action)}
	if server, _ := args["server"].(string); server != "" {
		parts = append(parts, server)
	}
	parts = append(parts, renderCallTarget(args, action)...)
	parts = append(parts, renderCallFilters(args)...)
	return strings.Join(parts, " ")
}

// renderCallTarget returns the action-specific target (tool, uri, prompt or
// server name) for the call header.
func renderCallTarget(args map[string]any, action string) []string {
	switch action {
	case "call_tool":
		return renderCallField(args, "tool", nil)
	case "read_resource":
		return renderCallField(args, "uri", shortenHome)
	case "get_prompt":
		return renderCallField(args, "prompt", nil)
	case "config_add":
		return renderCallField(args, "name", nil)
	}
	return nil
}

// renderCallField extracts one string arg, optionally transforms it, and
// returns it as a single-element slice when present.
func renderCallField(args map[string]any, key string, transform func(string) string) []string {
	v, _ := args[key].(string)
	if v == "" {
		return nil
	}
	if transform != nil {
		v = transform(v)
	}
	return []string{v}
}

// renderCallFilters renders the discovery filters shared by list actions.
func renderCallFilters(args map[string]any) []string {
	var parts []string
	if search, _ := args["search"].(string); search != "" {
		parts = append(parts, fmt.Sprintf("search:%s", search))
	}
	if limit, ok := args["limit"].(float64); ok && limit > 0 {
		parts = append(parts, fmt.Sprintf("limit:%.0f", limit))
	}
	if raw, _ := args["raw"].(bool); raw {
		parts = append(parts, "raw")
	}
	return parts
}

// RenderResult summarizes the agent_mcp result output.
func (r *AgentMCPRenderer) RenderResult(output string, ctx tuirender.RenderContext) string {
	if output == "" {
		return ""
	}
	return rToolOutput(r.summarize(output))
}

// summarize folds the JSON payload (plus any daemon notice) into a few
// human-readable lines; anything unparseable passes through truncated.
func (r *AgentMCPRenderer) summarize(output string) string {
	body := strings.TrimSpace(output)
	notice := ""
	if idx := strings.Index(body, "\n\n"); idx >= 0 {
		head, rest := body[:idx], body[idx+2:]
		if strings.Contains(rest, "auto-starting") {
			notice = strings.TrimSpace(rest)
			body = head
		}
	}

	summary := summarizeAgentMCPPayload(body)
	if summary == "" {
		summary = firstLine(body)
	}
	if notice != "" {
		summary += "\n" + rAccent(notice)
	}
	return summary
}

// agentMCPResultView mirrors the shaped payload shapes the renderer folds.
type agentMCPResultView struct {
	Servers []struct {
		Name  string `json:"name"`
		State string `json:"state"`
	} `json:"servers"`
	Tools          []any  `json:"tools"`
	Resources      []any  `json:"resources"`
	Prompts        []any  `json:"prompts"`
	Total          int    `json:"total"`
	Truncated      bool   `json:"truncated"`
	Action         string `json:"action"`
	Path           string `json:"path"`
	Name           string `json:"name"`
	Shutdown       bool   `json:"shutdown"`
	SessionsClosed int32  `json:"sessions_closed"`
}

// summarizeAgentMCPPayload renders the common result shapes: lists carry a
// total, config_add reports its action, shutdown reports whether it ran.
func summarizeAgentMCPPayload(body string) string {
	var v agentMCPResultView
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		return ""
	}
	if s := summarizeServers(body, &v); s != "" {
		return s
	}
	if s := summarizeListShape(body, &v); s != "" {
		return s
	}
	if v.Action != "" {
		return fmt.Sprintf("%s: %s (%s)", v.Name, v.Action, v.Path)
	}
	if s := summarizeShutdown(body, &v); s != "" {
		return s
	}
	// Tool calls, resources, prompts: show the content text when present.
	return agentMCPContentText(body)
}

// summarizeServers renders list-servers output; "" when not a servers shape.
func summarizeServers(body string, v *agentMCPResultView) string {
	if len(v.Servers) == 0 && !hasKey(body, "servers") {
		return ""
	}
	lines := make([]string, 0, len(v.Servers))
	for _, s := range v.Servers {
		lines = append(lines, fmt.Sprintf("%s (%s)", s.Name, s.State))
	}
	if len(lines) == 0 {
		return "no servers configured"
	}
	return strings.Join(lines, "\n")
}

// summarizeListShape renders tools/resources/prompts counts; "" when the
// payload carries none of those keys.
func summarizeListShape(body string, v *agentMCPResultView) string {
	switch {
	case hasKey(body, "tools"):
		return listSummary(len(v.Tools), v.Total, v.Truncated, "tools")
	case hasKey(body, "resources"):
		return listSummary(len(v.Resources), v.Total, v.Truncated, "resources")
	case hasKey(body, "prompts"):
		return listSummary(len(v.Prompts), v.Total, v.Truncated, "prompts")
	}
	return ""
}

// summarizeShutdown renders shutdown output; "" when not a shutdown shape.
func summarizeShutdown(body string, v *agentMCPResultView) string {
	if !hasKey(body, "shutdown") {
		return ""
	}
	if !v.Shutdown {
		return "daemon not running"
	}
	return fmt.Sprintf("daemon stopped (%d session(s) closed)", v.SessionsClosed)
}

// listSummary renders a capped-or-not count line.
func listSummary(kept, total int, truncated bool, what string) string {
	if truncated {
		return fmt.Sprintf("%d %s (of %d matched; raise limit for more)", kept, what, total)
	}
	if total > 0 {
		return fmt.Sprintf("%d %s", total, what)
	}
	return fmt.Sprintf("%d %s", kept, what)
}

// hasKey reports whether the JSON object has a top-level key.
func hasKey(body, key string) bool {
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		return false
	}
	_, ok := m[key]
	return ok
}

// agentMCPContentText joins text entries of an MCP content array.
func agentMCPContentText(body string) string {
	var m struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal([]byte(body), &m); err != nil || len(m.Content) == 0 {
		return ""
	}
	parts := make([]string, 0, len(m.Content))
	for _, c := range m.Content {
		if c.Text != "" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// firstLine returns the first line of s, truncated to 200 runes.
func firstLine(s string) string {
	line := s
	if idx := strings.Index(s, "\n"); idx >= 0 {
		line = s[:idx]
	}
	runes := []rune(line)
	if len(runes) > 200 {
		return string(runes[:200]) + "..."
	}
	return line
}

// PreviewLines shows a few result lines when collapsed.
func (r *AgentMCPRenderer) PreviewLines() int { return 6 }

// HideResultWhenCollapsed reports whether the result hides entirely when
// collapsed; discovery summaries are useful at a glance, so keep them.
func (r *AgentMCPRenderer) HideResultWhenCollapsed() bool { return false }
