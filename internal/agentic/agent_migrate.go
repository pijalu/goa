// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// migrateMessage converts an old-style Message to the new provider.Message format.
func migrateMessage(m Message) provider.Message {
	blocks := []provider.ContentBlock{}
	// For assistant messages that issued tool calls, OpenAI-compatible APIs
	// require the tool_call blocks to appear before the text content block.
	var elidedNames []string
	if m.Role == Assistant && len(m.ToolCalls) > 0 {
		for _, tc := range m.ToolCalls {
			// Elided calls are NOT serialized as tool_call blocks: the
			// in-history marker is not valid JSON (strict providers 400 the
			// request, breaking /compress:summarize) and a live-looking call
			// exemplar teaches the model to imitate the placeholder as its
			// own call arguments. They become a plain-text note instead, and
			// migrateMessages drops their matching tool results so call/result
			// pairing stays consistent.
			if tc.Arguments == elidedToolCallArguments {
				elidedNames = append(elidedNames, tc.Name)
				continue
			}
			blocks = append(blocks, provider.ContentBlock{
				Type:          provider.ContentBlockToolCall,
				ToolCallID:    tc.ID,
				ToolName:      tc.Name,
				ToolArguments: tc.Arguments,
			})
		}
	}
	text := m.Content
	if len(elidedNames) > 0 {
		note := elidedToolCallNote(elidedNames)
		if text == "" {
			text = note
		} else {
			text += "\n" + note
		}
	}
	blocks = append(blocks, provider.ContentBlock{
		Type: provider.ContentBlockText, Text: text,
	})
	for _, path := range m.Images {
		blocks = append(blocks, provider.ContentBlock{
			Type:      provider.ContentBlockImage,
			ImageData: path,
		})
	}
	if m.Thinking != "" {
		blocks = append(blocks, provider.ContentBlock{
			Type: provider.ContentBlockThinking, Thinking: m.Thinking,
		})
	}
	// Preserve tool call identity so the provider can format tool results
	// correctly (e.g. Gemma/Qwen need tool_call_id and tool_name).
	if m.Role == ToolRole {
		blocks = append(blocks, provider.ContentBlock{
			Type:       provider.ContentBlockToolResult,
			ToolCallID: m.ToolCallID,
			ToolName:   m.ToolName,
			Text:       m.Content,
		})
	}
	return provider.Message{
		Role:    roleToProviderRole(m.Role),
		Content: blocks,
	}
}

func migrateMessages(msgs []Message) []provider.Message {
	elided := elidedToolCallIDs(msgs)
	result := make([]provider.Message, 0, len(msgs))
	for _, m := range msgs {
		// Drop tool results whose call was elided: the assistant-side call is
		// serialized as a plain-text note, so a tool_result block would
		// reference a call the provider never sees (400 on strict APIs).
		if m.Role == ToolRole && m.ToolCallID != "" && elided[m.ToolCallID] {
			continue
		}
		result = append(result, migrateMessage(m))
	}
	return result
}

// elidedToolCallIDs collects the IDs of assistant tool calls whose arguments
// carry the elision marker, across the whole snapshot. Collecting
// snapshot-wide (instead of per message) also pairs correctly when the
// elision boundary splits a call from its result messages.
func elidedToolCallIDs(msgs []Message) map[string]bool {
	var ids map[string]bool
	for _, m := range msgs {
		if m.Role != Assistant {
			continue
		}
		for _, tc := range m.ToolCalls {
			if tc.ID != "" && tc.Arguments == elidedToolCallArguments {
				if ids == nil {
					ids = make(map[string]bool)
				}
				ids[tc.ID] = true
			}
		}
	}
	return ids
}

// elidedToolCallNote renders the plain-text placeholder that replaces elided
// tool-call blocks in provider-bound assistant messages. The note keeps the
// tool name visible so the model retains the fact that the call happened
// without an invocable exemplar to imitate.
func elidedToolCallNote(names []string) string {
	if len(names) == 1 {
		return fmt.Sprintf("[earlier call to %s elided]", names[0])
	}
	return fmt.Sprintf("[earlier calls to %s elided]", strings.Join(names, ", "))
}

// countElidedToolCalls reports how many assistant tool calls in the snapshot
// carry the elision marker. Used for pre-request diagnostics.
func countElidedToolCalls(msgs []Message) int {
	n := 0
	for _, m := range msgs {
		if m.Role != Assistant {
			continue
		}
		for _, tc := range m.ToolCalls {
			if tc.Arguments == elidedToolCallArguments {
				n++
			}
		}
	}
	return n
}

func roleToProviderRole(r Role) provider.Role {
	switch r {
	case System:
		return provider.RoleSystem
	case User:
		return provider.RoleUser
	case Assistant:
		return provider.RoleAssistant
	case ToolRole:
		return provider.RoleToolResult
	default:
		return provider.RoleUser
	}
}

// migrateSchemas converts old ToolSchema slices to provider.ToolSchema slices.
func migrateSchemas(schemas []ToolSchema) []provider.ToolSchema {
	result := make([]provider.ToolSchema, len(schemas))
	for i, s := range schemas {
		result[i] = provider.ToolSchema{
			Name:        s.Name,
			Description: s.Description,
			InputSchema: s.Schema,
		}
	}
	return result
}

// markGenStart records the wall-clock time of the first streamed token for
// the current stream, if not already recorded. Used to compute output tok/s as
