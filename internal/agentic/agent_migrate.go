// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import "github.com/pijalu/goa/internal/agentic/provider"

// migrateMessage converts an old-style Message to the new provider.Message format.
func migrateMessage(m Message) provider.Message {
	blocks := []provider.ContentBlock{}
	// For assistant messages that issued tool calls, OpenAI-compatible APIs
	// require the tool_call blocks to appear before the text content block.
	if m.Role == Assistant && len(m.ToolCalls) > 0 {
		for _, tc := range m.ToolCalls {
			blocks = append(blocks, provider.ContentBlock{
				Type:          provider.ContentBlockToolCall,
				ToolCallID:    tc.ID,
				ToolName:      tc.Name,
				ToolArguments: tc.Arguments,
			})
		}
	}
	blocks = append(blocks, provider.ContentBlock{
		Type: provider.ContentBlockText, Text: m.Content,
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
	result := make([]provider.Message, len(msgs))
	for i, m := range msgs {
		result[i] = migrateMessage(m)
	}
	return result
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
