// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package catalog holds protocol-level helpers shared by Goa's MCP transports
// and the Manager: cursor pagination, tool-name sanitization, MCP content →
// text conversion, and tolerant tools/list decoding. Keeping these here (per
// SRP) lets the transports and the manager stay free of wire-schema minutiae.
package catalog

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Content is one MCP content block in a tool result.
type Content struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// Result is the decoded shape of a tools/call response.
type Result struct {
	Content           []Content       `json:"content"`
	IsError           bool            `json:"isError"`
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
}

// ToolErrorDetail is the error category used when an MCP tool reports
// isError:true. Callers wrap it in internal.ToolError.
const ToolErrorDetail = "mcp_call_failed"

// Text flattens a tool result into a single string: it concatenates the text
// content blocks; when there are none but structuredContent is present, it
// returns the JSON-encoded structured content (OpenCode convertTool parity).
func Text(res *Result) string {
	var b strings.Builder
	for _, c := range res.Content {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	if b.Len() > 0 {
		return b.String()
	}
	if len(res.StructuredContent) > 0 && string(res.StructuredContent) != "null" {
		return string(res.StructuredContent)
	}
	return ""
}

// Err returns a non-nil error when the result is an MCP tool error
// (isError:true), carrying the flattened text as the message.
func Err(res *Result) error {
	if !res.IsError {
		return nil
	}
	msg := Text(res)
	if msg == "" {
		msg = "MCP tool returned an error"
	}
	return fmt.Errorf("%s", msg)
}
