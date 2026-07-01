// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package openai

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// convertMessages converts provider.Message slices to OpenAI API format.
func convertMessages(model provider.Model, messages []provider.Message, systemPrompt string, compat provider.OpenAICompletionsCompat) []map[string]interface{} {
	var out []map[string]interface{}

	// System prompt as first message.
	if systemPrompt != "" {
		role := "system"
		if provider.ToBool(compat.SupportsDeveloperRole, false) {
			role = "developer"
		}
		out = append(out, map[string]interface{}{
			"role":    role,
			"content": systemPrompt,
		})
	}

	for _, msg := range messages {
		switch msg.Role {
		case provider.RoleUser:
			out = append(out, convertUserMessage(msg))

		case provider.RoleAssistant:
			out = append(out, convertAssistantMessage(msg, compat))

		case provider.RoleToolResult:
			out = append(out, convertToolResultMessage(msg, compat))

		case provider.RoleSystem:
			out = append(out, map[string]interface{}{
				"role":    "system",
				"content": extractTextContent(msg.Content),
			})
		}
	}

	return out
}

func convertUserMessage(msg provider.Message) map[string]interface{} {
	content := buildUserContent(msg.Content)
	return map[string]interface{}{
		"role":    "user",
		"content": content,
	}
}

// buildUserContent converts content blocks into OpenAI user-message content.
// Text-only messages use a plain string; messages with images use an array of
// content parts.
func buildUserContent(blocks []provider.ContentBlock) interface{} {
	if !hasImageBlock(blocks) {
		return extractTextContent(blocks)
	}

	parts := make([]map[string]interface{}, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case provider.ContentBlockText:
			if b.Text != "" {
				parts = append(parts, map[string]interface{}{
					"type": "text",
					"text": b.Text,
				})
			}
		case provider.ContentBlockImage:
			dataURL := imagePathToDataURL(b.ImageData)
			if dataURL != "" {
				parts = append(parts, map[string]interface{}{
					"type":      "image_url",
					"image_url": map[string]interface{}{"url": dataURL},
				})
			}
		}
	}
	return parts
}

func hasImageBlock(blocks []provider.ContentBlock) bool {
	for _, b := range blocks {
		if b.Type == provider.ContentBlockImage {
			return true
		}
	}
	return false
}

func imagePathToDataURL(path string) string {
	if strings.HasPrefix(path, "data:") {
		return path
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	mime := http.DetectContentType(data)
	return fmt.Sprintf("data:%s;base64,%s", mime, base64.StdEncoding.EncodeToString(data))
}

func convertAssistantMessage(msg provider.Message, compat provider.OpenAICompletionsCompat) map[string]interface{} {
	result := map[string]interface{}{
		"role": "assistant",
	}

	// Collect text and tool calls from content blocks.
	var textContent string
	var toolCalls []map[string]interface{}

	for _, block := range msg.Content {
		switch block.Type {
		case provider.ContentBlockText:
			textContent += block.Text
		case provider.ContentBlockThinking:
			// DeepSeek-style reasoning in a separate field.
			result["reasoning_content"] = block.Thinking
		case provider.ContentBlockToolCall:
			toolCalls = append(toolCalls, map[string]interface{}{
				"id":   block.ToolCallID,
				"type": "function",
				"function": map[string]interface{}{
					"name":      block.ToolName,
					"arguments": block.ToolArguments,
				},
			})
		}
	}

	// Tool calls in separate field.
	if len(toolCalls) > 0 {
		result["tool_calls"] = toolCalls
		if textContent == "" {
			// Some providers require non-null content even for tool call messages.
			// We also need reasoning_content for DeepSeek.
			result["content"] = ""
		} else {
			result["content"] = textContent
		}
	} else {
		result["content"] = textContent
	}

	// DeepSeek: always include reasoning_content for assistant messages
	// when reasoning has been used.
	if provider.ToBool(compat.RequiresReasoningContentOnAssistantMessages, false) {
		if _, ok := result["reasoning_content"]; !ok {
			result["reasoning_content"] = ""
		}
	}

	return result
}

func convertToolResultMessage(msg provider.Message, compat provider.OpenAICompletionsCompat) map[string]interface{} {
	text := extractTextContent(msg.Content)
	toolCallID := ""
	toolName := ""
	for _, block := range msg.Content {
		if block.Type == provider.ContentBlockToolResult {
			toolCallID = block.ToolCallID
			toolName = block.ToolName
			if block.Text != "" {
				text = block.Text
			}
		}
	}

	if provider.ToBool(compat.ToolResultAsUser, false) {
		// Gemma/Qwen-style models don't reliably associate role:"tool"
		// messages with the preceding tool call.  Format the result as a
		// user message with XML markers instead.
		formatted := fmt.Sprintf("<tool_result>\n<tool_name>%s</tool_name>\n<tool_call_id>%s</tool_call_id>\n<content>\n%s\n</content>\n</tool_result>",
			toolName, toolCallID, text)
		return map[string]interface{}{
			"role":    "user",
			"content": formatted,
		}
	}

	msgMap := map[string]interface{}{
		"role":         "tool",
		"content":      text,
		"tool_call_id": toolCallID,
	}
	// Name field is intentionally omitted — not part of the OpenAI spec
	// and can cause issues with some endpoints.
	_ = toolName

	return msgMap
}

func extractTextContent(blocks []provider.ContentBlock) string {
	var text string
	for _, block := range blocks {
		if block.Type == provider.ContentBlockText {
			text += block.Text
		}
	}
	return text
}

// convertTools converts ToolSchema to OpenAI tools format.
func convertTools(tools []provider.ToolSchema) []map[string]interface{} {
	if len(tools) == 0 {
		return nil
	}
	out := make([]map[string]interface{}, len(tools))
	for i, t := range tools {
		out[i] = map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.InputSchema,
			},
		}
	}
	return out
}

// BuildOpenAIParams is an alias for buildParams, exported for external use.
func BuildOpenAIParams(model provider.Model, ctx provider.Context, opts provider.StreamOptions, compat provider.OpenAICompletionsCompat) map[string]interface{} {
	return buildParams(model, ctx, opts, compat)
}

// ConvertMessagesOpenAI is an alias for convertMessages, exported for external use.
func ConvertMessagesOpenAI(model provider.Model, messages []provider.Message, systemPrompt string, compat provider.OpenAICompletionsCompat) []map[string]interface{} {
	return convertMessages(model, messages, systemPrompt, compat)
}

// ConvertToolsOpenAI is an alias for convertTools, exported for external use.
func ConvertToolsOpenAI(tools []provider.ToolSchema) []map[string]interface{} {
	return convertTools(tools)
}

// Ensure error type is used
var _ = fmt.Sprintf
