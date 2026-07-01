// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package hooks

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

const toolOrder = 500

// ToolHook normalizes tool schemas and tool call IDs.
type ToolHook struct {
	profile schema.VariantProfile
}

// Name returns the hook name.
func (h *ToolHook) Name() string { return "tools" }

// Order returns the hook order.
func (h *ToolHook) Order() int { return toolOrder }

// Init initializes the hook with the variant profile.
func (h *ToolHook) Init(profile schema.VariantProfile) error {
	h.profile = profile
	return nil
}

// ApplyRequest normalizes tool schemas and tool call IDs in the context.
func (h *ToolHook) ApplyRequest(ctx *RequestContext) error {
	ctx.Context.Tools = normalizeToolSchemas(ctx.Context.Tools, h.profile.ToolCompat.SchemaSanitizer)
	ctx.Context.Messages = normalizeToolCallIDs(ctx.Context.Messages, h.profile.ToolCompat.ToolCallIDRules)
	ctx.Context.Messages = applyToolResultFormats(ctx.Context.Messages, h.profile.ToolCompat)
	return nil
}

// ApplyResponse is a no-op for tools.
func (h *ToolHook) ApplyResponse(ctx *ResponseContext) error { return nil }

// ApplyError is a no-op for tools.
func (h *ToolHook) ApplyError(ctx *ErrorContext) error { return nil }

func normalizeToolSchemas(tools []schema.ToolSchema, sanitizer schema.SchemaSanitizer) []schema.ToolSchema {
	out := make([]schema.ToolSchema, len(tools))
	for i, t := range tools {
		out[i] = schema.ToolSchema{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: sanitizeSchema(t.InputSchema, sanitizer),
		}
	}
	return out
}

func normalizeToolCallIDs(messages []schema.Message, rules schema.ToolCallIDRules) []schema.Message {
	if rules.MaxLength == 0 && rules.Prefix == "" && !rules.HashBased {
		return messages
	}
	out := make([]schema.Message, len(messages))
	for i, m := range messages {
		out[i] = m
		if len(m.Content) == 0 {
			continue
		}
		converted := make([]schema.ContentBlock, len(m.Content))
		for j, b := range m.Content {
			converted[j] = b
			if b.Type == schema.ContentBlockToolCall || b.Type == schema.ContentBlockToolResult {
				converted[j].ToolCallID = normalizeID(b.ToolCallID, rules)
			}
		}
		out[i].Content = converted
	}
	return out
}

func normalizeID(id string, rules schema.ToolCallIDRules) string {
	if id == "" {
		return id
	}
	if rules.Prefix != "" && !strings.HasPrefix(id, rules.Prefix) {
		id = rules.Prefix + id
	}
	if rules.HashBased {
		sum := sha256.Sum256([]byte(id))
		id = fmt.Sprintf("%x", sum)[:min(rules.MaxLength, len(sum)*2)]
	}
	if rules.Alphabet != "" {
		alphabet := rules.Alphabet
		alphabet = strings.TrimPrefix(alphabet, "[")
		alphabet = strings.TrimSuffix(alphabet, "]")
		re := regexp.MustCompile("[^" + alphabet + "]")
		id = re.ReplaceAllString(id, "")
	}
	if rules.MaxLength > 0 && len(id) > rules.MaxLength {
		id = id[:rules.MaxLength]
	}
	return id
}

func applyToolResultFormats(messages []schema.Message, compat schema.ToolCompat) []schema.Message {
	if !compat.ToolResultAsUser && !compat.RequiresToolResultName && !compat.RequiresAssistantAfterToolResult {
		return messages
	}
	out := make([]schema.Message, 0, len(messages))
	for _, m := range messages {
		out = append(out, transformToolResultMessage(m, compat)...)
	}
	return out
}

func transformToolResultMessage(m schema.Message, compat schema.ToolCompat) []schema.Message {
	if m.Role != schema.RoleToolResult || len(m.Content) == 0 {
		return []schema.Message{m}
	}
	if compat.ToolResultAsUser {
		m = convertToolResultToUser(m)
	}
	if compat.RequiresToolResultName {
		m = addToolResultName(m)
	}
	result := []schema.Message{m}
	if compat.RequiresAssistantAfterToolResult {
		result = append(result, syntheticAssistantMessage())
	}
	return result
}

func convertToolResultToUser(m schema.Message) schema.Message {
	m.Role = schema.RoleUser
	for i := range m.Content {
		m.Content[i].Type = schema.ContentBlockText
		m.Content[i].Text = fmt.Sprintf("<tool_result name=\"%s\" id=\"%s\">%s</tool_result>",
			m.Content[i].ToolName, m.Content[i].ToolCallID, m.Content[i].Text)
	}
	return m
}

func addToolResultName(m schema.Message) schema.Message {
	if m.Extra != nil {
		return m
	}
	m.Extra = map[string]interface{}{"name": m.Content[0].ToolName}
	return m
}

func syntheticAssistantMessage() schema.Message {
	return schema.NewAssistantMessage([]schema.ContentBlock{
		{Type: schema.ContentBlockText, Text: " "},
	})
}

func sanitizeSchema(s map[string]any, sanitizer schema.SchemaSanitizer) map[string]any {
	if s == nil || sanitizer == schema.SchemaSanitizerNone {
		return s
	}
	out := make(map[string]any, len(s))
	for k, v := range s {
		switch {
		case sanitizer == schema.SchemaSanitizerOpenAI && k == "$ref":
			continue
		case sanitizer == schema.SchemaSanitizerMoonshot && k == "$ref":
			continue
		case sanitizer == schema.SchemaSanitizerGemini && k == "enum":
			out[k] = convertIntEnumToString(v)
		default:
			out[k] = sanitizeValue(v, sanitizer)
		}
	}
	return out
}

func convertIntEnumToString(v any) any {
	enums, ok := v.([]any)
	if !ok {
		return v
	}
	out := make([]any, len(enums))
	for i, e := range enums {
		switch x := e.(type) {
		case int:
			out[i] = fmt.Sprintf("%d", x)
		case int64:
			out[i] = fmt.Sprintf("%d", x)
		case float64:
			out[i] = fmt.Sprintf("%.0f", x)
		default:
			out[i] = e
		}
	}
	return out
}

func sanitizeValue(v any, sanitizer schema.SchemaSanitizer) any {
	switch x := v.(type) {
	case map[string]any:
		return sanitizeSchema(x, sanitizer)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = sanitizeValue(e, sanitizer)
		}
		return out
	default:
		return v
	}
}
