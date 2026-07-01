// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package hooks

import (
	"strings"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

const messageOrder = 600

// MessageHook performs message-level transformations such as image downgrade,
// system updates, and stop-reason filtering.
type MessageHook struct {
	profile schema.VariantProfile
}

// Name returns the hook name.
func (h *MessageHook) Name() string { return "messages" }

// Order returns the hook order.
func (h *MessageHook) Order() int { return messageOrder }

// Init initializes the hook with the variant profile.
func (h *MessageHook) Init(profile schema.VariantProfile) error {
	h.profile = profile
	return nil
}

// ApplyRequest transforms messages before they reach the protocol converter.
func (h *MessageHook) ApplyRequest(ctx *RequestContext) error {
	ctx.Context.Messages = downgradeImages(ctx.Context.Messages, h.profile)
	ctx.Context.Messages = filterStopReasons(ctx.Context.Messages)
	ctx.Context.Messages = keepSignatureOnlyThinking(ctx.Context.Messages)
	ctx.Context.Messages = convertThinkingToTextWhenRequired(ctx.Context.Messages, ctx.Model, h.profile)
	ctx.Context.Messages = wrapSystemUpdates(ctx.Context.Messages, ctx.Model)
	return nil
}

// ApplyResponse is a no-op for messages.
func (h *MessageHook) ApplyResponse(ctx *ResponseContext) error { return nil }

// ApplyError is a no-op for messages.
func (h *MessageHook) ApplyError(ctx *ErrorContext) error { return nil }

func downgradeImages(messages []schema.Message, profile schema.VariantProfile) []schema.Message {
	if profile.Compat.ImageURLScheme != "" {
		return messages
	}
	out := make([]schema.Message, len(messages))
	for i, m := range messages {
		out[i] = m
		if len(m.Content) == 0 {
			continue
		}
		converted := make([]schema.ContentBlock, 0, len(m.Content))
		previousPlaceholder := false
		for _, b := range m.Content {
			if b.Type == schema.ContentBlockImage {
				if !previousPlaceholder {
					converted = append(converted, schema.ContentBlock{Type: schema.ContentBlockText, Text: "(image omitted)"})
				}
				previousPlaceholder = true
				continue
			}
			converted = append(converted, b)
			previousPlaceholder = b.Type == schema.ContentBlockText && b.Text == "(image omitted)"
		}
		out[i].Content = converted
	}
	return out
}

func filterStopReasons(messages []schema.Message) []schema.Message {
	out := make([]schema.Message, len(messages))
	for i, m := range messages {
		out[i] = m
		if m.StopReason == schema.StopReasonError ||
			m.StopReason == schema.StopReasonContentFiltered {
			out[i].StopReason = ""
		}
	}
	return out
}

func keepSignatureOnlyThinking(messages []schema.Message) []schema.Message {
	out := make([]schema.Message, len(messages))
	for i, m := range messages {
		out[i] = m
		converted := make([]schema.ContentBlock, 0, len(m.Content))
		for _, b := range m.Content {
			if b.Type == schema.ContentBlockThinking && b.Thinking == "" && b.ThinkingSignature != "" {
				converted = append(converted, b)
				continue
			}
			converted = append(converted, b)
		}
		out[i].Content = converted
	}
	return out
}

func convertThinkingToTextWhenRequired(messages []schema.Message, model schema.Model, profile schema.VariantProfile) []schema.Message {
	sourceFormat := model.ThinkingFormat
	if sourceFormat == "" && len(model.ThinkingLevelMap) > 0 {
		sourceFormat = schema.ThinkingFormatThinkingContent
	}
	targetFormat := schema.ThinkingFormat(profile.Compat.ThinkingFormat)
	if !requiresThinkingAsText(sourceFormat, targetFormat) {
		return messages
	}
	return transformThinkingToText(messages)
}

func requiresThinkingAsText(sourceFormat, targetFormat schema.ThinkingFormat) bool {
	if sourceFormat == "" || sourceFormat == schema.ThinkingFormatNone {
		return false
	}
	if targetFormat == "" || targetFormat == schema.ThinkingFormatNone {
		return true
	}
	return !strings.Contains(string(targetFormat), "thinking") &&
		!strings.Contains(string(targetFormat), "reasoning")
}

func wrapSystemUpdates(messages []schema.Message, model schema.Model) []schema.Message {
	if model.ID == "claude-opus-4-8" {
		return messages
	}
	out := make([]schema.Message, len(messages))
	for i, m := range messages {
		out[i] = m
		if m.Role != schema.RoleSystem || len(m.Content) == 0 {
			continue
		}
		text := m.Content[0].Text
		if strings.Contains(text, "<system-update>") {
			continue
		}
		out[i].Content[0].Text = "<system-update>\n" + text + "\n</system-update>"
	}
	return out
}
