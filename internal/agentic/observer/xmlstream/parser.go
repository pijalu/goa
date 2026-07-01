// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package xmlstream provides tools for parsing XML conversation streams
// produced by XMLStreamingObserver back into agentic.Message slices.
// This is the inverse operation of the observer — it converts XML to Messages
// for LLM context restoration on session resume.
package xmlstream

import (
	"encoding/xml"
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/agentic"
)

// ---- Internal XML structs matching the observer's output schema ----

type xmlMessage struct {
	Role   string           `xml:"role"`
	Blocks xmlMessageBlocks `xml:"blocks"`
}

type xmlMessageBlocks struct {
	Thinking  []xmlBlock    `xml:"thinking"`
	Content   []xmlBlock    `xml:"content"`
	ToolCall  []xmlToolCall `xml:"toolcall"`
	Stats     []xmlBlock    `xml:"stats"`
	SkillCall []xmlBlock    `xml:"skillcall"`
}

type xmlBlock struct {
	Text string `xml:",chardata"`
}

type xmlToolCall struct {
	Name   string `xml:"name"`
	Input  string `xml:"input"`
	Output string `xml:"output"`
}

// ---- Public API ----

// ParseConversationXML parses an XML conversation stream (as produced by
// XMLStreamingObserver) into a slice of agentic.Message suitable for use as
// LLM conversation history (agentic.Agent.SetHistory).
//
// The parser is the inverse of the XML observer it:
//   - Converts <message role="..."> back to typed agentic.Message values
//   - Splits <toolcall> into a ToolCall message + a ToolRole result message
//     with synthetic tool_call_id values
//   - Excludes <thinking> blocks (the assistant's internal reasoning is never
//     sent back to the LLM)
//   - Excludes <stats> / <skillcall> blocks that are metadata or transient
//
// Input can be a full <conversation> document or bare <messages> content.
func ParseConversationXML(xmlContent string) ([]agentic.Message, error) {
	if strings.TrimSpace(xmlContent) == "" {
		return []agentic.Message{}, nil
	}

	// Normalise: extract the inner <message> elements from whatever wrapper
	// we have (<conversation> or bare <messages>).
	inner := extractMessageElements(xmlContent)

	// Parse as flat <message> elements.
	// The XML stream may be incomplete (last message not yet closed during streaming).
	// encoding/xml returns an error for unterminated input but still decodes
	// complete messages before the error point — we use those partial results.
	var wrapper struct {
		Messages []xmlMessage `xml:"message"`
	}
	if err := xml.Unmarshal([]byte(inner), &wrapper); err != nil {
		// If we got some messages despite the error, use them.
		// This handles the streaming case where the last message is incomplete.
		if len(wrapper.Messages) == 0 {
			return nil, fmt.Errorf("xmlstream: parse message elements: %w", err)
		}
	}

	return convertToAgenticMessages(wrapper.Messages), nil
}

// ---- conversion logic ----

func convertToAgenticMessages(xmlMessages []xmlMessage) []agentic.Message {
	var out []agentic.Message
	tcCounter := 0

	for _, m := range xmlMessages {
		role := agentic.Role(strings.TrimSpace(m.Role))

		switch role {
		case agentic.User, agentic.System:
			out = append(out, buildUserOrSystem(role, m.Blocks.Content)...)

		case agentic.Assistant:
			// 1. Tool calls (each becomes ToolCall + ToolRole pair)
			for _, tc := range m.Blocks.ToolCall {
				tcID := fmt.Sprintf("tc-%d", tcCounter)
				tcCounter++

				out = append(out, agentic.Message{
					Type:       agentic.ToolCall,
					Role:       agentic.Assistant,
					ToolName:   tc.Name,
					ToolInput:  tc.Input,
					ToolCallID: tcID,
				})

				if strings.TrimSpace(tc.Output) != "" {
					out = append(out, agentic.Message{
						Type:       agentic.Content,
						Role:       agentic.ToolRole,
						Content:    tc.Output,
						ToolCallID: tcID,
					})
				}
			}

			// 2. Content blocks (skip thinking / stats / skillcall)
			contentText := collectText(m.Blocks.Content)
			if contentText != "" {
				out = append(out, agentic.Message{
					Type:    agentic.Content,
					Role:    agentic.Assistant,
					Content: contentText,
				})
			}

		// role "tool" is handled inside <toolcall><output> above; skip standalone.
		default:
			continue
		}
	}

	return out
}

func buildUserOrSystem(role agentic.Role, contentBlocks []xmlBlock) []agentic.Message {
	text := collectText(contentBlocks)
	if text == "" {
		return nil
	}
	return []agentic.Message{{
		Type:    agentic.Content,
		Role:    role,
		Content: text,
	}}
}

func collectText(blocks []xmlBlock) string {
	var parts []string
	for _, b := range blocks {
		if s := strings.TrimSpace(b.Text); s != "" {
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n")
}

// extractMessageElements extracts the raw <message>…</message> elements from
// either a <conversation> document or a bare <messages> wrapper. Returns
// the elements wrapped in a synthetic root so xml.Unmarshal can find them.
func extractMessageElements(xml string) string {
	if strings.TrimSpace(xml) == "" {
		return "<root></root>"
	}

	// Full <conversation> document — extract <messages> inner content.
	if strings.Contains(xml, "<conversation>") {
		start := strings.Index(xml, "<messages>")
		if start == -1 {
			// No <messages> at all — wrap everything in root
			return "<root>" + xml + "</root>"
		}
		// Extract everything after <messages> up to optional </messages>
		contentStart := start + len("<messages>")
		end := strings.LastIndex(xml, "</messages>")
		var inner string
		if end != -1 && end > contentStart {
			inner = xml[contentStart:end]
		} else {
			// Incomplete XML — stream hasn't been flushed yet.
			// Take everything after <messages>.
			inner = xml[contentStart:]
		}
		return "<root>" + inner + "</root>"
	}

	// Bare <messages> wrapper — remove the wrapper tags.
	if strings.HasPrefix(strings.TrimSpace(xml), "<messages>") {
		inner := strings.TrimSpace(xml)
		inner = strings.TrimPrefix(inner, "<messages>")
		inner = strings.TrimSuffix(inner, "</messages>")
		return "<root>" + inner + "</root>"
	}

	// Already bare, wrap in synthetic root.
	return "<root>" + xml + "</root>"
}
